package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/sean-reid/interviews/internal/debug"
	"github.com/sean-reid/interviews/internal/grading"
	"github.com/sean-reid/interviews/internal/interview"
	"github.com/sean-reid/interviews/internal/session"
	"github.com/sean-reid/interviews/internal/taxonomy"
)

// cmdStart brings up everything one debugging interview needs and records
// it. The sequence it replaces is four commands, each repeating a seed the
// interviewer had to invent and keep.
func cmdStart(args []string, stdout, stderr io.Writer) int {
	fs, contentRoot := newFlagSet("start", stderr)
	seedFlag := fs.String("seed", "", "use this interview id instead of generating one")
	levelFlag := fs.String("level", "", "level this interview is calibrated for; the sheet prints its band")
	noBreak := fs.Bool("no-break", false, "leave the environment healthy (authoring)")
	baseURL := fs.String("base-url", "", "public base URL fronting the session ports")
	var sets repeatedFlag
	fs.Var(&sets, "set", "pin a parameter (name=value, repeatable)")
	pos, err := parsePermuted(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) != 1 {
		fmt.Fprintln(stderr, "usage: interviews start <problem-id> [--level senior] [--seed id] [--no-break]")
		return 2
	}
	problemID := pos[0]

	level, err := parseLevel(*levelFlag)
	if err != nil {
		fmt.Fprintf(stderr, "interviews start: %v\n", err)
		return 2
	}
	seed := *seedFlag
	if seed == "" {
		if seed, err = interview.NewSeed(time.Now()); err != nil {
			fmt.Fprintf(stderr, "interviews start: %v\n", err)
			return 1
		}
	} else if err := interview.ValidSeed(seed); err != nil {
		fmt.Fprintf(stderr, "interviews start: %v\n", err)
		return 2
	}

	// Build the engine before announcing anything: an unknown problem, a
	// take-home, or a bad --set has to fail before a cluster exists.
	e, err := engineFor(*contentRoot, problemID, seed, "", sets, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "interviews start: %v\n", err)
		return 1
	}
	if err := warnLevelUnsupported(*contentRoot, problemID, level, stderr); err != nil {
		fmt.Fprintf(stderr, "interviews start: %v\n", err)
		return 1
	}

	rec := &interview.Session{
		Seed: seed, Problem: problemID, Type: taxonomy.Debugging, Level: level,
		Mode: interview.Local, CreatedAt: time.Now(),
		Workdir: e.Workdir, Evidence: e.Workdir,
	}
	if abs, err := filepath.Abs(*contentRoot); err == nil {
		rec.ContentRoot = abs
	}
	// Record before building. A start that dies partway through a ten minute
	// cluster build still leaves something that names the workdir and can be
	// cleaned up, which is the case that stranded resources come from.
	if err := interview.Save(rec); err != nil {
		fmt.Fprintf(stderr, "interviews start: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "session %s: %s\n", seed, problemID)

	ctx := context.Background()
	if err := e.Up(ctx); err != nil {
		fmt.Fprintf(stderr, "interviews start: %v\n", err)
		return 1
	}
	if !*noBreak {
		fmt.Fprintln(stdout, answerKeyNotice)
		if err := e.Break(ctx); err != nil {
			fmt.Fprintf(stderr, "interviews start: %v\n", err)
			return 1
		}
	}
	info, err := session.NewManager(e, stdout).Start(ctx, session.StartOptions{BaseURL: *baseURL})
	if err != nil {
		fmt.Fprintf(stderr, "interviews start: %v\n", err)
		return 1
	}
	rec.CandidateURL, rec.ObserverURL = info.CandidateURL, info.ObserverURL
	if err := interview.Save(rec); err != nil {
		fmt.Fprintf(stderr, "interviews start: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "\nlog hints with: interviews hint \"what you said\"\nend with:      interviews end\n")
	return 0
}

// cmdEnd stops the session, takes the last evidence pass, and tears the
// environment down, leaving the evidence and the record.
func cmdEnd(args []string, stdout, stderr io.Writer) int {
	fs, contentRoot := newFlagSet("end", stderr)
	seedFlag := fs.String("seed", "", "session to end (default: the current one)")
	purge := fs.Bool("purge", false, "delete the evidence too")
	pos, err := parsePermuted(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) > 1 {
		fmt.Fprintln(stderr, "usage: interviews end [<seed>] [--purge]")
		return 2
	}
	seed := *seedFlag
	if len(pos) == 1 {
		seed = pos[0]
	}
	rec, err := currentOr(seed, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "interviews end: %v\n", err)
		return 1
	}
	root := rec.ContentRoot
	if root == "" || *contentRoot != DefaultContentRoot {
		root = *contentRoot
	}
	e, err := engineFor(root, rec.Problem, rec.Seed, rec.Workdir, nil, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "interviews end: %v\n", err)
		return 1
	}
	ctx := context.Background()
	// Stop before down: the last evidence pass has to read the environment
	// while it is still there.
	if err := session.NewManager(e, stdout).Stop(ctx); err != nil {
		fmt.Fprintf(stderr, "interviews end: stopping the session: %v\n", err)
	}
	kept, err := e.Down(ctx, *purge)
	if err != nil {
		fmt.Fprintf(stderr, "interviews end: %v\n", err)
		return 1
	}
	reportTeardown(stdout, e, kept, *purge)
	rec.EndedAt = time.Now()
	if *purge {
		rec.Evidence = ""
	}
	if err := interview.Save(rec); err != nil {
		fmt.Fprintf(stderr, "interviews end: %v\n", err)
		return 1
	}
	return 0
}

// cmdHint logs a hint against the current session, working out the minute
// from when that session started. Both of those were arguments, typed
// while talking to a candidate.
func cmdHint(args []string, stdout, stderr io.Writer) int {
	fs := newBareFlagSet("hint", stderr)
	seedFlag := fs.String("seed", "", "session to log against (default: the current one)")
	minuteFlag := fs.Int("minute", -1, "override the minute (default: measured from the session start)")
	pos, err := parsePermuted(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) != 1 || strings.TrimSpace(pos[0]) == "" {
		fmt.Fprintln(stderr, `usage: interviews hint "what you told them" [--minute n] [--seed id]`)
		return 2
	}
	rec, err := currentOr(*seedFlag, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "interviews hint: %v\n", err)
		return 1
	}
	minute := *minuteFlag
	if minute < 0 {
		minute = int(time.Since(rec.CreatedAt).Minutes())
		if minute < 0 {
			minute = 0
		}
	}
	if rec.Workdir == "" {
		fmt.Fprintf(stderr, "interviews hint: session %s has no workdir recorded; use interviews grade hint --workdir\n", rec.Seed)
		return 1
	}
	if err := os.MkdirAll(rec.Workdir, 0o755); err != nil {
		fmt.Fprintf(stderr, "interviews hint: %v\n", err)
		return 1
	}
	if err := grading.AppendHint(rec.Workdir, grading.Hint{Minute: minute, Text: pos[0], At: time.Now()}); err != nil {
		fmt.Fprintf(stderr, "interviews hint: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "minute %d: %s\n", minute, pos[0])
	return 0
}

// cmdSessions answers what exists, what it cost, and what the URLs were.
func cmdSessions(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "show" {
		return sessionsShow(args[1:], stdout, stderr)
	}
	fs := newBareFlagSet("sessions", stderr)
	all := fs.Bool("all", false, "include sessions that have ended")
	if _, err := parsePermuted(fs, args); err != nil {
		return 2
	}
	list, err := interview.List()
	if err != nil {
		fmt.Fprintf(stderr, "interviews sessions: %v\n", err)
		return 1
	}
	if !*all {
		list = slices.DeleteFunc(list, func(s *interview.Session) bool { return !s.EndedAt.IsZero() })
	}
	if len(list) == 0 {
		fmt.Fprintln(stdout, "no sessions (interviews start <problem> begins one)")
		return 0
	}
	w := tabwriter.NewWriter(stdout, 2, 8, 2, ' ', 0)
	fmt.Fprintln(w, "SEED\tPROBLEM\tMODE\tAGE\tSTATE\tEVIDENCE")
	for _, s := range list {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			s.Seed, s.Problem, s.Mode, age(time.Since(s.CreatedAt)), sessionState(s), evidenceNote(s))
	}
	if err := w.Flush(); err != nil {
		return 1
	}
	if !*all {
		fmt.Fprintln(stdout, "\n--all includes sessions that have ended")
	}
	return 0
}

func sessionsShow(args []string, stdout, stderr io.Writer) int {
	fs := newBareFlagSet("sessions show", stderr)
	seedFlag := fs.String("seed", "", "session to show (default: the current one)")
	pos, err := parsePermuted(fs, args)
	if err != nil {
		return 2
	}
	seed := *seedFlag
	if len(pos) == 1 {
		seed = pos[0]
	}
	// No announce line here: the whole point of this output is to say which
	// session it is.
	rec, err := currentOr(seed, io.Discard)
	if err != nil {
		fmt.Fprintf(stderr, "interviews sessions show: %v\n", err)
		return 1
	}
	w := tabwriter.NewWriter(stdout, 2, 8, 2, ' ', 0)
	rows := [][2]string{
		{"seed", rec.Seed},
		{"problem", rec.Problem},
		{"mode", string(rec.Mode)},
		{"state", sessionState(rec)},
		{"started", rec.CreatedAt.Format(time.RFC1123)},
	}
	if rec.Level != "" {
		rows = append(rows, [2]string{"level", string(rec.Level)})
	}
	if !rec.EndedAt.IsZero() {
		rows = append(rows, [2]string{"ended", rec.EndedAt.Format(time.RFC1123)})
	}
	rows = append(rows,
		[2]string{"candidate url", rec.CandidateURL},
		[2]string{"observer url", rec.ObserverURL})
	if rec.AppURL != "" {
		rows = append(rows, [2]string{"app url", rec.AppURL})
	}
	rows = append(rows,
		[2]string{"workdir", rec.Workdir},
		[2]string{"evidence", rec.Evidence})
	if rec.TerraformDir != "" {
		rows = append(rows, [2]string{"terraform", rec.TerraformDir})
	}
	for _, r := range rows {
		if r[1] == "" {
			continue
		}
		fmt.Fprintf(w, "%s\t%s\n", r[0], r[1])
	}
	if err := w.Flush(); err != nil {
		return 1
	}
	fmt.Fprintf(stdout, "\ngrade with: interviews grade sheet %s --seed %s -o sheet.md\n", rec.Problem, rec.Seed)
	return 0
}

// currentOr resolves a session by seed, or the current one when no seed is
// given, announcing which so nothing acts on an assumption in silence.
func currentOr(seed string, stderr io.Writer) (*interview.Session, error) {
	if seed != "" {
		return interview.Load(seed)
	}
	rec, err := interview.Current()
	if errors.Is(err, interview.ErrNoCurrent) {
		return nil, errors.New("no session yet: run interviews start <problem>")
	}
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(stderr, "session %s (%s)\n", rec.Seed, rec.Problem)
	return rec, nil
}

// sessionState is derived, never trusted from the record: a session whose
// state file is gone is over whether or not anyone ran end.
func sessionState(s *interview.Session) string {
	if !s.EndedAt.IsZero() {
		return "ended"
	}
	if s.Workdir == "" {
		return "unknown"
	}
	st, err := debug.LoadState(s.Workdir)
	switch {
	case err != nil:
		return "gone"
	case !st.Live():
		return "torn down"
	case len(st.Injected) == 0:
		return "healthy"
	default:
		return "broken"
	}
}

// evidenceNote says whether there is anything to grade, since that is the
// question a list of old sessions is being asked.
func evidenceNote(s *interview.Session) string {
	if s.Evidence == "" {
		return "-"
	}
	if strings.HasPrefix(s.Evidence, "s3://") {
		return s.Evidence
	}
	if _, err := os.Stat(filepath.Join(s.Evidence, session.EvidenceFile)); err == nil {
		return "bundled"
	}
	if _, err := os.Stat(filepath.Join(s.Evidence, session.CastFile)); err == nil {
		return "recording"
	}
	if _, err := os.Stat(s.Evidence); err == nil {
		return "workdir"
	}
	return "-"
}

func age(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m"
	case d < 48*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h"
	default:
		return strconv.Itoa(int(d.Hours()/24)) + "d"
	}
}

func parseLevel(v string) (taxonomy.Level, error) {
	if v == "" {
		return "", nil
	}
	l := taxonomy.Level(v)
	if !taxonomy.ValidLevel(l) {
		return "", fmt.Errorf("--level %q: want one of %v", v, taxonomy.Levels)
	}
	return l, nil
}

// warnLevelUnsupported says so when a problem does not claim to grade the
// level it is being run for. It is a warning, not a refusal: the
// interviewer knows something the manifest does not.
func warnLevelUnsupported(contentRoot, problemID string, level taxonomy.Level, stderr io.Writer) error {
	if level == "" {
		return nil
	}
	reg, err := openRegistry(contentRoot, false, stderr)
	if err != nil {
		return err
	}
	entry, ok := reg.Get(problemID)
	if !ok {
		return nil
	}
	if !slices.Contains(entry.Problem.Manifest.Levels, level) {
		fmt.Fprintf(stderr, "warning: %s grades %v, not %s\n", problemID, entry.Problem.Manifest.Levels, level)
	}
	return nil
}
