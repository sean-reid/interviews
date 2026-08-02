package cli

import (
	"cmp"
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
	remote := fs.Bool("remote", false, "provision a disposable host instead of running here")
	ttl := fs.Int("ttl", 120, "minutes before a provisioned host destroys itself")
	instanceType := fs.String("instance-type", "", "EC2 instance type (default: the module's)")
	infra := fs.String("infra", "", "path to the terraform modules")
	baseURL := fs.String("base-url", "", "public base URL fronting the session ports")
	var sets repeatedFlag
	fs.Var(&sets, "set", "pin a parameter (name=value, repeatable)")
	pos, err := parsePermuted(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) != 1 {
		return usageErr("start", stderr)
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

	// Interviewing against last month's problems is a silent failure, and the
	// content is a checkout of its own now. Warn, never block: this is the
	// command that runs with a candidate waiting.
	warnStale(*contentRoot, stderr)

	if *remote {
		// The host builds its own environment from the bundle, so nothing local
		// is needed beyond knowing the problem is real.
		if err := warnLevelUnsupported(*contentRoot, problemID, level, stderr); err != nil {
			fmt.Fprintf(stderr, "interviews start: %v\n", err)
			return 1
		}
		return startRemote(problemID, seed, level, remoteOptions{
			TTLMinutes: *ttl, InstanceType: *instanceType, Infra: *infra,
		}, stdout, stderr)
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
	// This is now the session every unqualified command means.
	if err := interview.SetCurrent(seed); err != nil {
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
	rec.CandidateURL, rec.ObserverURL, rec.AppURL = info.CandidateURL, info.ObserverURL, info.AppURL
	if err := interview.Save(rec); err != nil {
		fmt.Fprintf(stderr, "interviews start: %v\n", err)
		return 1
	}
	// The timeline is a foreground sampler, so it cannot be part of this
	// command, and nothing else says that skipping it leaves the timeline in
	// the evidence empty.
	fmt.Fprintf(stdout, "\nlog hints with:  interviews hint \"what you said\"\n"+
		"fault timeline:  interviews session timeline %s --for 70m (own window; empty without it)\n"+
		"end with:        interviews end\n", problemID)
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
		return usageErr("end", stderr)
	}
	seed := *seedFlag
	if len(pos) == 1 {
		seed = pos[0]
	}
	rec, err := currentOr(seed, stderr)
	if err != nil {
		// sessions --remote can see a host another machine started, or one
		// whose record this machine lost. Refusing to end it would leave
		// hand-run terraform as the only way to stop it billing, so ask the
		// account whether the seed names something real before giving up.
		if adopted, aerr := adoptOrNot(seed); adopted != nil {
			fmt.Fprintf(stderr, "no record of %s here; ending it from the account\n", seed)
			return endRemote(adopted, *purge, stdout, stderr)
		} else if aerr != nil {
			fmt.Fprintf(stderr, "interviews end: %v\n%s\n", err, aerr)
			return 1
		}
		fmt.Fprintf(stderr, "interviews end: %v\n", err)
		return 1
	}
	if rec.Mode == interview.AWS {
		return endRemote(rec, *purge, stdout, stderr)
	}
	root := contentRootFor(rec.ContentRoot, *contentRoot, passed(fs, "content"))
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
	_, existed := purgeTarget(e)
	kept, err := e.Down(ctx, *purge)
	if err != nil {
		fmt.Fprintf(stderr, "interviews end: %v\n", err)
		return 1
	}
	reportTeardown(stdout, e, kept, *purge, existed)
	rec.EndedAt = time.Now()
	if *purge {
		rec.Evidence = ""
	}
	if err := interview.Save(rec); err != nil {
		fmt.Fprintf(stderr, "interviews end: %v\n", err)
		return 1
	}
	// An ended session is not the one you are working in.
	if err := interview.ClearCurrent(rec.Seed); err != nil {
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
		return usageErr("hint", stderr)
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
	// A provisioned host keeps its workdir on the host and a take-home has
	// none, so for those the ledger lives here, keyed by seed. start --remote
	// prints "log hints with: interviews hint", and this is what makes that
	// true: it used to name a flag with no value the interviewer could know.
	dir := rec.Workdir
	if dir == "" {
		if dir, err = interview.HintsDir(rec.Seed); err != nil {
			fmt.Fprintf(stderr, "interviews hint: %v\n", err)
			return 1
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(stderr, "interviews hint: %v\n", err)
		return 1
	}
	if err := grading.AppendHint(dir, grading.Hint{Minute: minute, Text: pos[0], At: time.Now()}); err != nil {
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
	if len(args) > 0 && args[0] == "log" {
		return sessionsLog(args[1:], stdout, stderr)
	}
	fs := newBareFlagSet("sessions", stderr)
	all := fs.Bool("all", false, "include sessions that have ended")
	waiting := fs.Bool("waiting", false, "only sessions that need something from you")
	remote := fs.Bool("remote", false, "ask AWS what is running, including hosts other machines started")
	if _, err := parsePermuted(fs, args); err != nil {
		return 2
	}
	list, err := interview.List()
	if err != nil {
		fmt.Fprintf(stderr, "interviews sessions: %v\n", err)
		return 1
	}
	// Every seed this machine has ever recorded, kept before the filter below
	// so a discovered host can be told from one that merely ended here.
	known := make(map[string]*interview.Session, len(list))
	for _, s := range list {
		known[s.Seed] = s
	}
	if !*all {
		list = slices.DeleteFunc(list, func(s *interview.Session) bool { return !s.EndedAt.IsZero() })
	}
	rows := make([]sessionRow, 0, len(list))
	for _, s := range list {
		state, rank := sessionStatus(s)
		rows = append(rows, sessionRow{s: s, state: state, rank: rank})
	}
	stranded := 0
	if *remote {
		cfg := interview.LoadConfig().AWS
		if cfg == nil {
			fmt.Fprintln(stderr, "interviews sessions: this machine has no aws setup (interviews setup aws)")
			return 1
		}
		ctx := context.Background()
		hosts, err := describeHosts(ctx, cfg)
		if err != nil {
			fmt.Fprintf(stderr, "interviews sessions: %v\n", err)
			return 1
		}
		rows = reconcile(rows, known, hosts)
		if stranded, err = strandedAddresses(ctx, cfg); err != nil {
			fmt.Fprintf(stderr, "interviews sessions: %v\n", err)
			return 1
		}
	}
	if *waiting {
		rows = slices.DeleteFunc(rows, func(r sessionRow) bool { return r.rank > waitingCutoff })
	}
	// Stable, so List's newest-first order breaks ties within a rank.
	slices.SortStableFunc(rows, func(a, b sessionRow) int { return cmp.Compare(a.rank, b.rank) })
	if len(rows) == 0 {
		switch {
		case *waiting:
			fmt.Fprintln(stdout, "nothing is waiting on you")
		default:
			fmt.Fprintln(stdout, "no sessions (interviews start <problem> begins one)")
		}
		reportStranded(stdout, stranded)
		return 0
	}
	w := tabwriter.NewWriter(stdout, 2, 8, 2, ' ', 0)
	fmt.Fprintln(w, "SEED\tPROBLEM\tMODE\tAGE\tSTATE\tEVIDENCE")
	elsewhere := false
	for _, r := range rows {
		seed := r.s.Seed
		if r.elsewhere {
			seed, elsewhere = seed+" *", true
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			seed, r.s.Problem, r.s.Mode, age(time.Since(r.s.CreatedAt)), r.state, evidenceNote(r.s))
	}
	if err := w.Flush(); err != nil {
		return 1
	}
	if elsewhere {
		fmt.Fprintln(stdout, "\n* running in the account, with no record on this machine")
	}
	reportStranded(stdout, stranded)
	switch {
	case *waiting:
		fmt.Fprintln(stdout, "\nwaiting on you, most urgent first")
	case !*all:
		fmt.Fprintln(stdout, "\n--waiting narrows this to what needs you, --all includes sessions that have ended")
	}
	return 0
}

func reportStranded(stdout io.Writer, n int) {
	switch {
	case n == 1:
		fmt.Fprintln(stdout, "\n1 elastic ip is attached to nothing and billing; interviews end releases it")
	case n > 1:
		fmt.Fprintf(stdout, "\n%d elastic ips are attached to nothing and billing; interviews end releases them\n", n)
	}
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
	if !rec.DueAt.IsZero() {
		rows = append(rows, [2]string{"due", rec.DueAt.Format(time.RFC1123)})
	}
	if !rec.EndedAt.IsZero() {
		rows = append(rows, [2]string{"ended", rec.EndedAt.Format(time.RFC1123)})
	}
	if !rec.ReviewedAt.IsZero() {
		rows = append(rows, [2]string{"reviewed", rec.ReviewedAt.Format(time.RFC1123)})
	}
	// The two questions asked of an offline session weeks later: where did the
	// bundle go, and where did their submission land.
	rows = append(rows,
		[2]string{"bundle", rec.BundlePath},
		[2]string{"submission", rec.SubmissionPath})
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

// sessionState says where a session is. For debugging it is derived from the
// workdir and never trusted from the record, because a session whose state
// file is gone is over whether or not anyone ran end. For the offline types
// there is no substrate to read, so the recorded stage is the answer.
// sessionRow is one line of a listing: a session, what it is doing, and
// where that sorts. Discovered hosts get one too, with a session assembled
// from tags rather than read from the registry.
type sessionRow struct {
	s         *interview.Session
	state     string
	rank      int
	elsewhere bool
}

// Listing order, by who a session is blocked on. Everything before
// waitingCutoff needs the interviewer to do something; everything after it is
// waiting on the candidate, on the clock, or on nobody.
const (
	// rankStranded is where AWS and the record disagree: a host past its ttl,
	// one the record does not know about, or a record whose host is gone.
	// First, because every one of them either bills or hides something.
	rankStranded = iota
	rankToReview
	rankOverdue
	rankNotSent
	waitingCutoff
	rankSent
	rankLive
	rankReviewed
	rankClosed
)

func sessionState(s *interview.Session) string {
	state, _ := sessionStatus(s)
	return state
}

// sessionStatus reports what a session is doing and where it sorts. Both come
// from one pass so a listing's order can never disagree with the text it shows.
func sessionStatus(s *interview.Session) (string, int) {
	if s.Mode == interview.Offline {
		return offlineStatus(s)
	}
	if !s.EndedAt.IsZero() {
		return "ended", rankClosed
	}
	if s.Mode == interview.AWS {
		// A provisioned host has no workdir here to read, and asking EC2 per
		// row would make a listing wait on the network. What the record knows
		// is whether the provision got as far as an address.
		if s.Host == "" {
			return "provisioning", rankLive
		}
		if s.TTLMinutes > 0 {
			left := time.Until(s.CreatedAt.Add(time.Duration(s.TTLMinutes) * time.Minute))
			if left <= 0 {
				// Sorted first because it is the only state that bills by the
				// hour until someone runs end.
				return "past its ttl", rankStranded
			}
			return "up, " + age(left) + " of ttl left", rankLive
		}
		return "up", rankLive
	}
	if s.Workdir == "" {
		return "unknown", rankClosed
	}
	st, err := debug.LoadState(s.Workdir)
	switch {
	case err != nil:
		return "gone", rankClosed
	case !st.Live():
		return "torn down", rankClosed
	case len(st.Injected) == 0:
		return "healthy", rankLive
	default:
		// What the state file knows is which faults went in, not which are
		// still there: reading that needs the check scripts, and a listing
		// must not run seven of them per session.
		return fmt.Sprintf("%d injected", len(st.Injected)), rankLive
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

// offlineState reads the stage, and says what it is waiting on: a list of
// take-homes is being asked which ones need the interviewer, not which ones
// exist.
func offlineState(s *interview.Session) string {
	state, _ := offlineStatus(s)
	return state
}

func offlineStatus(s *interview.Session) (string, int) {
	switch s.Stage {
	case interview.Returned:
		return "returned, to review", rankToReview
	case interview.Reviewed:
		return "reviewed", rankReviewed
	case interview.Sent:
		if !s.DueAt.IsZero() && time.Now().After(s.DueAt) {
			return "overdue by " + age(time.Since(s.DueAt)), rankOverdue
		}
		if !s.DueAt.IsZero() {
			return "sent, due in " + age(time.Until(s.DueAt)), rankSent
		}
		return "sent", rankSent
	case interview.Created:
		return "not sent", rankNotSent
	default:
		return string(s.Stage), rankClosed
	}
}

// cmdStage moves an offline interview along. Three one-word commands,
// because these are the ones that get forgotten: a take-home that came back
// and was never marked is a take-home nobody is waiting on.
func cmdStage(stage interview.Stage) command {
	return func(args []string, stdout, stderr io.Writer) int {
		name := string(stage)
		fs := newBareFlagSet(name, stderr)
		seedFlag := fs.String("seed", "", "session to mark (default: the current one)")
		pos, err := parsePermuted(fs, args)
		if err != nil {
			return 2
		}
		// returned takes the submission path, since that is what grading reads.
		wantPath := stage == interview.Returned
		if (wantPath && len(pos) != 1) || (!wantPath && len(pos) != 0) {
			return usageErr(name, stderr)
		}
		rec, err := currentOr(*seedFlag, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "interviews %s: %v\n", name, err)
			return 1
		}
		if rec.Mode != interview.Offline {
			fmt.Fprintf(stderr, "interviews %s: %s is a %s session, which has no stages; use interviews end\n",
				name, rec.Seed, rec.Mode)
			return 1
		}
		if wantPath {
			abs, err := filepath.Abs(pos[0])
			if err != nil {
				fmt.Fprintf(stderr, "interviews %s: %v\n", name, err)
				return 1
			}
			if _, err := os.Stat(abs); err != nil {
				fmt.Fprintf(stderr, "interviews %s: %v\n", name, err)
				return 1
			}
			rec.SubmissionPath = abs
		}
		rec.Stage = stage
		if stage == interview.Reviewed {
			// The review is the end of an offline interview, so it leaves the
			// open list the way a torn-down environment does.
			rec.ReviewedAt = time.Now()
			rec.EndedAt = rec.ReviewedAt
		}
		if err := interview.Save(rec); err != nil {
			fmt.Fprintf(stderr, "interviews %s: %v\n", name, err)
			return 1
		}
		fmt.Fprintf(stdout, "%s: %s\n", rec.Seed, offlineState(rec))
		switch stage {
		case interview.Sent:
			fmt.Fprintf(stdout, "when it comes back: interviews returned <path>\n")
		case interview.Returned:
			fmt.Fprintf(stdout, "grade it: interviews grade sheet %s --seed %s -o sheet.md\n", rec.Problem, rec.Seed)
		}
		return 0
	}
}

// sessionsLog prints what a provisioned host has said about its own boot.
// There is no ssh and no console access, so the log the host uploads is the
// only way to watch one come up, and --follow makes it a live view.
func sessionsLog(args []string, stdout, stderr io.Writer) int {
	fs := newBareFlagSet("sessions log", stderr)
	seedFlag := fs.String("seed", "", "session to read (default: the current one)")
	follow := fs.Bool("follow", false, "keep printing as the host says more")
	pos, err := parsePermuted(fs, args)
	if err != nil {
		return 2
	}
	seed := *seedFlag
	if len(pos) == 1 {
		seed = pos[0]
	}
	rec, err := currentOr(seed, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "interviews sessions log: %v\n", err)
		return 1
	}
	if rec.Mode != interview.AWS {
		fmt.Fprintf(stderr, "interviews sessions log: %s runs %s, and only a provisioned host reports a boot\n",
			rec.Seed, rec.Mode)
		return 1
	}
	cfg := interview.LoadConfig()
	env := os.Environ()
	if cfg.AWS != nil && cfg.AWS.Profile != "" {
		env = append(env, "AWS_PROFILE="+cfg.AWS.Profile)
	}
	body, err := fetchProvisionLog(env, rec.Evidence)
	if err != nil && body == "" {
		fmt.Fprintf(stderr, "interviews sessions log: nothing uploaded yet for %s\n", rec.Seed)
		fmt.Fprintf(stderr, "the host writes this at milestones, so a fresh provision has not reached one\n")
		return 1
	}
	fmt.Fprint(stdout, body)
	if !*follow {
		return 0
	}
	seen := len(body)
	deadline := time.Now().Add(bootTimeout)
	for time.Now().Before(deadline) {
		time.Sleep(bootPoll)
		next, err := fetchProvisionLog(env, rec.Evidence)
		if err != nil || len(next) <= seen {
			continue
		}
		fmt.Fprint(stdout, next[seen:])
		seen = len(next)
		if strings.Contains(next, "provisioning finished") {
			return 0
		}
	}
	return 0
}
