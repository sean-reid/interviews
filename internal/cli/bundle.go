package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/sean-reid/interviews/internal/bundle"
	"github.com/sean-reid/interviews/internal/interview"
	"github.com/sean-reid/interviews/internal/registry"
	"github.com/sean-reid/interviews/internal/taxonomy"
	"github.com/sean-reid/interviews/internal/variant"
)

func cmdBundle(args []string, stdout, stderr io.Writer) int {
	fs, contentRoot := newFlagSet("bundle", stderr)
	seedFlag := fs.String("seed", "", "interview id selecting the variant (default: a new one)")
	outPath := fs.String("o", "", "output directory, or a .tar.gz/.tgz path")
	levelFlag := fs.String("level", "", "level this interview is calibrated for")
	dueFlag := fs.Duration("due", 0, "how long the candidate has, recorded with the session")
	var sets repeatedFlag
	fs.Var(&sets, "set", "override a parameter (name=value, repeatable)")
	pos, err := parsePermuted(fs, args)
	if err != nil {
		return parseExit(err)
	}
	if len(pos) != 1 || *outPath == "" {
		return usageErr("bundle", stderr)
	}
	level, err := parseLevel(*levelFlag)
	if err != nil {
		fmt.Fprintf(stderr, "interviews bundle: %v\n", err)
		return 2
	}
	// A generated seed that nothing records is the seed-you-have-to-keep
	// problem the registry exists to remove, so bundling registers the
	// session it just handed out.
	seed, code, err := seedOrNew(*seedFlag)
	if err != nil {
		fmt.Fprintf(stderr, "interviews bundle: %v\n", err)
		return code
	}
	return deliver("bundle", handout{
		contentRoot: *contentRoot, problemID: pos[0], seed: seed,
		out: *outPath, level: level, due: *dueFlag, sets: sets,
	}, stdout, stderr)
}

// handout is everything one candidate drop needs: which problem, which
// variant of it, where the drop lands, and the clock the candidate is on.
type handout struct {
	contentRoot string
	problemID   string
	seed        string
	out         string
	level       taxonomy.Level
	due         time.Duration
	sets        []string
}

// deliver writes the drop and records the session behind it, which together
// are the whole of what an offline interview is. start and bundle both call
// it, so which verb the interviewer typed cannot change what the candidate
// gets or what the registry ends up knowing. cmd names the caller in errors.
func deliver(cmd string, h handout, stdout, stderr io.Writer) int {
	overrides, err := parseOverrides(h.sets)
	if err != nil {
		fmt.Fprintf(stderr, "interviews %s: %v\n", cmd, err)
		return 2
	}
	reg, err := openRegistry(h.contentRoot, true, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "interviews %s: %v\n", cmd, err)
		return 1
	}
	entry, ok := reg.Get(h.problemID)
	if !ok {
		fmt.Fprintf(stderr, "interviews %s: no problem %q (try interviews list)\n", cmd, h.problemID)
		return 1
	}
	// A problem that does not validate is not safe to hand out. A visibility
	// glob the classifier rejected, for instance, leaves the problem with no
	// candidate files at all, and the bundle would be a front page and
	// nothing else.
	if findings := reg.FindingsFor(entry.Dir); registry.Errors(findings) {
		fmt.Fprintf(stderr, "interviews %s: %s does not validate:\n", cmd, h.problemID)
		for _, f := range findings {
			if !f.Warning {
				fmt.Fprintf(stderr, "  %s\n", f)
			}
		}
		return 1
	}
	v, err := variant.Resolve(h.problemID, entry.Problem.Manifest.Params, h.seed, overrides)
	if err != nil {
		fmt.Fprintf(stderr, "interviews %s: %v\n", cmd, err)
		return 1
	}
	if err := bundle.Write(entry.Problem, v, h.out); err != nil {
		fmt.Fprintf(stderr, "interviews %s: %v\n", cmd, err)
		return 1
	}
	abs, err := filepath.Abs(h.out)
	if err != nil {
		abs = h.out
	}
	rec := &interview.Session{
		Seed: h.seed, Problem: h.problemID, Type: entry.Type, Level: h.level,
		Mode: interview.Offline, Stage: interview.Created,
		CreatedAt: time.Now(), BundlePath: abs, Evidence: abs,
	}
	if h.due > 0 {
		rec.DueAt = rec.CreatedAt.Add(h.due)
	}
	if root, aerr := filepath.Abs(h.contentRoot); aerr == nil {
		rec.ContentRoot = root
	}
	if err := interview.Save(rec); err != nil {
		fmt.Fprintf(stderr, "interviews %s: %v\n", cmd, err)
		return 1
	}
	// Only if nothing is live. Preparing the next candidate's drop beside a
	// running interview must not repoint that interview's hints.
	if err := interview.SetCurrentIfIdle(h.seed); err != nil {
		fmt.Fprintf(stderr, "interviews %s: %v\n", cmd, err)
		return 1
	}
	fmt.Fprintf(stdout, "session %s: %s\nbundle:  %s\n", h.seed, h.problemID, abs)
	if !rec.DueAt.IsZero() {
		fmt.Fprintf(stdout, "due:     %s\n", rec.DueAt.Format("Mon 2 Jan 15:04"))
	}
	fmt.Fprintf(stdout, "\nsend it, then: interviews sent\n")
	return 0
}
