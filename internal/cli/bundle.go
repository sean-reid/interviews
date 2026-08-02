package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/sean-reid/interviews/internal/bundle"
	"github.com/sean-reid/interviews/internal/interview"
	"github.com/sean-reid/interviews/internal/registry"
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
		return 2
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
	seed := *seedFlag
	if seed == "" {
		if seed, err = interview.NewSeed(time.Now()); err != nil {
			fmt.Fprintf(stderr, "interviews bundle: %v\n", err)
			return 1
		}
	} else if err := interview.ValidSeed(seed); err != nil {
		fmt.Fprintf(stderr, "interviews bundle: %v\n", err)
		return 2
	}
	overrides, err := parseOverrides(sets)
	if err != nil {
		fmt.Fprintf(stderr, "interviews bundle: %v\n", err)
		return 2
	}
	reg, err := openRegistry(*contentRoot, true, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "interviews bundle: %v\n", err)
		return 1
	}
	entry, ok := reg.Get(pos[0])
	if !ok {
		fmt.Fprintf(stderr, "interviews bundle: no problem %q (try interviews list)\n", pos[0])
		return 1
	}
	// A problem that does not validate is not safe to hand out. A visibility
	// glob the classifier rejected, for instance, leaves the problem with no
	// candidate files at all, and the bundle would be a front page and
	// nothing else.
	if findings := reg.FindingsFor(entry.Dir); registry.Errors(findings) {
		fmt.Fprintf(stderr, "interviews bundle: %s does not validate:\n", pos[0])
		for _, f := range findings {
			if !f.Warning {
				fmt.Fprintf(stderr, "  %s\n", f)
			}
		}
		return 1
	}
	v, err := variant.Resolve(pos[0], entry.Problem.Manifest.Params, seed, overrides)
	if err != nil {
		fmt.Fprintf(stderr, "interviews bundle: %v\n", err)
		return 1
	}
	if err := bundle.Write(entry.Problem, v, *outPath); err != nil {
		fmt.Fprintf(stderr, "interviews bundle: %v\n", err)
		return 1
	}
	abs, err := filepath.Abs(*outPath)
	if err != nil {
		abs = *outPath
	}
	rec := &interview.Session{
		Seed: seed, Problem: pos[0], Type: entry.Type, Level: level,
		Mode: interview.Offline, Stage: interview.Created,
		CreatedAt: time.Now(), BundlePath: abs, Evidence: abs,
	}
	if *dueFlag > 0 {
		rec.DueAt = rec.CreatedAt.Add(*dueFlag)
	}
	if root, aerr := filepath.Abs(*contentRoot); aerr == nil {
		rec.ContentRoot = root
	}
	if err := interview.Save(rec); err != nil {
		fmt.Fprintf(stderr, "interviews bundle: %v\n", err)
		return 1
	}
	// Only if nothing is live. Preparing the next candidate's drop beside a
	// running interview must not repoint that interview's hints.
	if err := interview.SetCurrentIfIdle(seed); err != nil {
		fmt.Fprintf(stderr, "interviews bundle: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "session %s: %s\nbundle:  %s\n", seed, pos[0], abs)
	if !rec.DueAt.IsZero() {
		fmt.Fprintf(stdout, "due:     %s\n", rec.DueAt.Format("Mon 2 Jan 15:04"))
	}
	fmt.Fprintf(stdout, "\nsend it, then: interviews sent\n")
	return 0
}
