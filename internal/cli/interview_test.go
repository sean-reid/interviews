package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sean-reid/interviews/internal/grading"
	"github.com/sean-reid/interviews/internal/interview"
	"github.com/sean-reid/interviews/internal/taxonomy"
)

// record puts a session in a registry of this test's own and returns it.
func record(t *testing.T, s *interview.Session) *interview.Session {
	t.Helper()
	if os.Getenv(interview.HomeEnv) == "" {
		t.Setenv(interview.HomeEnv, t.TempDir())
	}
	if s.Workdir == "" {
		s.Workdir = t.TempDir()
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now()
	}
	if s.Mode == "" {
		s.Mode = interview.Local
	}
	if err := interview.Save(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func hintsIn(t *testing.T, workdir string) []grading.Hint {
	t.Helper()
	hints, err := grading.LoadHints(workdir)
	if err != nil {
		t.Fatal(err)
	}
	return hints
}

// The minute and the seed were both arguments, typed mid-conversation. The
// session knows when it started, so the command can work it out.
func TestHintNeedsNeitherSeedNorMinute(t *testing.T) {
	rec := record(t, &interview.Session{
		Seed: "calm-bison-0731", Problem: "pipeline-meltdown",
		CreatedAt: time.Now().Add(-17 * time.Minute),
	})
	code, stdout, stderr := run(t, "hint", "asked what the events say")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	hints := hintsIn(t, rec.Workdir)
	if len(hints) != 1 {
		t.Fatalf("hints = %v, want one", hints)
	}
	if hints[0].Minute != 17 {
		t.Errorf("minute = %d, want 17 measured from the session start", hints[0].Minute)
	}
	if hints[0].Text != "asked what the events say" {
		t.Errorf("text = %q", hints[0].Text)
	}
	if !strings.Contains(stdout, "minute 17") {
		t.Errorf("stdout = %q, want it to confirm the minute", stdout)
	}
	// Acting on an assumed session has to say which one.
	if !strings.Contains(stderr, "calm-bison-0731") {
		t.Errorf("stderr = %q, want the session named", stderr)
	}
}

func TestHintMinuteOverrideAndSeed(t *testing.T) {
	rec := record(t, &interview.Session{Seed: "keen-crane-0731", Problem: "pipeline-meltdown"})
	record(t, &interview.Session{Seed: "calm-bison-0731", Problem: "pipeline-meltdown"})

	if code, _, stderr := run(t, "hint", "a late entry", "--minute", "4", "--seed", "keen-crane-0731"); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	hints := hintsIn(t, rec.Workdir)
	if len(hints) != 1 || hints[0].Minute != 4 {
		t.Errorf("hints = %v, want one at minute 4", hints)
	}
}

// The record is created before terraform apply, so counting the ttl from
// CreatedAt called a host stranded minutes before its own self-destruct.
func TestTTLCountsFromProvisionedAt(t *testing.T) {
	now := time.Now()
	s := &interview.Session{Mode: interview.AWS, Host: "203.0.113.9", TTLMinutes: 60,
		CreatedAt: now.Add(-70 * time.Minute), ProvisionedAt: now.Add(-30 * time.Minute)}
	if state, rank := sessionStatus(s); rank != rankLive || !strings.HasPrefix(state, "up, ") {
		t.Errorf("state %q rank %d, want a live host with ttl left", state, rank)
	}
	// A record from before the field existed still counts from creation.
	s.ProvisionedAt = time.Time{}
	if state, rank := sessionStatus(s); state != "past its ttl" || rank != rankStranded {
		t.Errorf("state %q rank %d, want past its ttl measured from CreatedAt", state, rank)
	}
}

// grade hint rejects a negative minute with exit 2; hint used to fall back
// silently to the measured minute for the same input.
func TestHintRejectsANegativeMinute(t *testing.T) {
	rec := record(t, &interview.Session{Seed: "calm-bison-0731", Problem: "pipeline-meltdown"})
	code, _, stderr := run(t, "hint", "too early", "--minute", "-5")
	if code != 2 || !strings.Contains(stderr, "--minute") {
		t.Errorf("exit %d, stderr %q, want a usage error naming the flag", code, stderr)
	}
	if hints := hintsIn(t, rec.Workdir); len(hints) != 0 {
		t.Errorf("hints = %v, want none logged", hints)
	}
}

func TestHintRefusesAnUnknownSession(t *testing.T) {
	record(t, &interview.Session{Seed: "calm-bison-0731", Problem: "pipeline-meltdown"})
	code, _, stderr := run(t, "hint", "text", "--seed", "no-such-session")
	if code == 0 {
		t.Error("exit 0 for a session that does not exist")
	}
	if !strings.Contains(stderr, "no session") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestHintWithNothingStarted(t *testing.T) {
	t.Setenv(interview.HomeEnv, t.TempDir())
	code, _, stderr := run(t, "hint", "text")
	if code == 0 {
		t.Error("exit 0 with no sessions at all")
	}
	if !strings.Contains(stderr, "interviews start") {
		t.Errorf("stderr = %q, want the command that would fix it", stderr)
	}
}

func TestHintUsageErrors(t *testing.T) {
	record(t, &interview.Session{Seed: "calm-bison-0731", Problem: "pipeline-meltdown"})
	if code, _, _ := run(t, "hint"); code != 2 {
		t.Error("hint with no text should be a usage error")
	}
	if code, _, _ := run(t, "hint", "   "); code != 2 {
		t.Error("hint with blank text should be a usage error")
	}
}

// The URLs are printed once, at start. Forgetting them is the most likely
// reason to go looking for a session record.
func TestSessionsShowPrintsTheURLs(t *testing.T) {
	record(t, &interview.Session{
		Seed: "calm-bison-0731", Problem: "relay", Level: "senior", Mode: interview.AWS,
		CandidateURL: "https://1.2.3.4.sslip.io/c/tok", ObserverURL: "https://1.2.3.4.sslip.io/o/obs",
		Evidence: "s3://bucket/calm-bison-0731/", TerraformDir: "/tmp/tf",
	})
	code, stdout, stderr := run(t, "sessions", "show")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	for _, want := range []string{
		"calm-bison-0731", "relay", "senior", "aws",
		"https://1.2.3.4.sslip.io/c/tok", "https://1.2.3.4.sslip.io/o/obs",
		"s3://bucket/calm-bison-0731/", "/tmp/tf",
		"interviews grade sheet relay --seed calm-bison-0731",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("show missing %q:\n%s", want, stdout)
		}
	}
}

func TestSessionsListsOpenSessionsAndHidesEndedOnesByDefault(t *testing.T) {
	record(t, &interview.Session{Seed: "calm-bison-0731", Problem: "relay"})
	record(t, &interview.Session{
		Seed: "done-otter-0730", Problem: "orbit-shop",
		CreatedAt: time.Now().Add(-26 * time.Hour), EndedAt: time.Now().Add(-25 * time.Hour),
	})

	code, stdout, stderr := run(t, "sessions")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if !strings.Contains(stdout, "calm-bison-0731") {
		t.Errorf("open session missing:\n%s", stdout)
	}
	if strings.Contains(stdout, "done-otter-0730") {
		t.Errorf("ended session listed without --all:\n%s", stdout)
	}
	_, stdout, _ = run(t, "sessions", "--all")
	if !strings.Contains(stdout, "done-otter-0730") || !strings.Contains(stdout, "ended") {
		t.Errorf("--all does not show the ended session:\n%s", stdout)
	}
}

// A listing is read to find the next thing to do, so it is ordered by who
// each session is blocked on rather than by when it started.
func TestSessionsOrdersByWhoIsBlocked(t *testing.T) {
	t.Setenv(interview.HomeEnv, t.TempDir())
	now := time.Now()
	record(t, &interview.Session{Seed: "waiting-tide-0801", Problem: "relay",
		Mode: interview.Offline, Stage: interview.Sent, DueAt: now.Add(48 * time.Hour)})
	record(t, &interview.Session{Seed: "burning-cash-0801", Problem: "relay",
		Mode: interview.AWS, Host: "203.0.113.9", TTLMinutes: 60, CreatedAt: now.Add(-3 * time.Hour)})
	record(t, &interview.Session{Seed: "unsent-draft-0801", Problem: "relay",
		Mode: interview.Offline, Stage: interview.Created})
	record(t, &interview.Session{Seed: "late-heron-0801", Problem: "relay",
		Mode: interview.Offline, Stage: interview.Sent, DueAt: now.Add(-2 * time.Hour)})
	record(t, &interview.Session{Seed: "graded-next-0801", Problem: "relay",
		Mode: interview.Offline, Stage: interview.Returned})

	code, stdout, stderr := run(t, "sessions")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	want := []string{
		"burning-cash-0801", // past its ttl, and billing until someone ends it
		"graded-next-0801",  // returned, waiting on a review
		"late-heron-0801",   // overdue
		"unsent-draft-0801", // never sent
		"waiting-tide-0801", // sent, and the candidate has time left
	}
	if got := seedOrder(stdout, want); !slices.Equal(got, want) {
		t.Errorf("order = %v, want %v\n%s", got, want, stdout)
	}

	code, stdout, stderr = run(t, "sessions", "--waiting")
	if code != 0 {
		t.Fatalf("--waiting: exit %d, stderr %q", code, stderr)
	}
	if got := seedOrder(stdout, want); !slices.Equal(got, want[:4]) {
		t.Errorf("--waiting = %v, want %v\n%s", got, want[:4], stdout)
	}
}

// seedOrder returns the seeds from want in the order the listing printed
// them, so a missing seed fails as an order mismatch rather than silently.
func seedOrder(stdout string, want []string) []string {
	var got []string
	for line := range strings.Lines(stdout) {
		seed, _, _ := strings.Cut(strings.TrimSpace(line), " ")
		if slices.Contains(want, seed) {
			got = append(got, seed)
		}
	}
	return got
}

func TestSessionsWaitingWithNothingToDo(t *testing.T) {
	t.Setenv(interview.HomeEnv, t.TempDir())
	record(t, &interview.Session{Seed: "calm-bison-0801", Problem: "relay",
		Mode: interview.Offline, Stage: interview.Sent, DueAt: time.Now().Add(48 * time.Hour)})
	code, stdout, stderr := run(t, "sessions", "--waiting")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if !strings.Contains(stdout, "nothing is waiting on you") {
		t.Errorf("stdout = %q, want it to say so plainly", stdout)
	}
}

func TestSessionsOnAnEmptyRegistry(t *testing.T) {
	t.Setenv(interview.HomeEnv, t.TempDir())
	code, stdout, stderr := run(t, "sessions")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if !strings.Contains(stdout, "interviews start") {
		t.Errorf("stdout = %q, want the command that begins one", stdout)
	}
}

// State comes from the workdir, not the record: a session whose
// environment was torn down by hand is not still running.
func TestSessionsDerivesStateFromTheWorkdir(t *testing.T) {
	wd := t.TempDir()
	record(t, &interview.Session{Seed: "calm-bison-0731", Problem: "relay", Workdir: wd})
	for _, tc := range []struct {
		name  string
		state map[string]any
		want  string
	}{
		{"injected", map[string]any{"injected": []string{"01-x"}}, "1 injected"},
		{"healthy", map[string]any{"injected": []string{}}, "healthy"},
		{"torn down", map[string]any{"injected": []string{"01-x"}, "torn_down_at": "2026-07-31T10:00:00Z"}, "torn down"},
	} {
		raw, err := json.Marshal(tc.state)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(wd, "state.json"), raw, 0o600); err != nil {
			t.Fatal(err)
		}
		_, stdout, _ := run(t, "sessions")
		if !strings.Contains(stdout, tc.want) {
			t.Errorf("%s: listing says nothing about it:\n%s", tc.name, stdout)
		}
	}

	if err := os.Remove(filepath.Join(wd, "state.json")); err != nil {
		t.Fatal(err)
	}
	if _, stdout, _ := run(t, "sessions"); !strings.Contains(stdout, "gone") {
		t.Errorf("a session with no state file still reads as live:\n%s", stdout)
	}
}

// Whether there is anything to grade is the question a list of old
// sessions is being asked.
func TestSessionsReportsWhatEvidenceExists(t *testing.T) {
	wd := t.TempDir()
	record(t, &interview.Session{Seed: "calm-bison-0731", Problem: "relay", Workdir: wd, Evidence: wd})
	if _, stdout, _ := run(t, "sessions"); !strings.Contains(stdout, "workdir") {
		t.Errorf("empty workdir:\n%s", stdout)
	}
	if err := os.WriteFile(filepath.Join(wd, "session.cast"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, stdout, _ := run(t, "sessions"); !strings.Contains(stdout, "recording") {
		t.Errorf("recording not reported:\n%s", stdout)
	}
	if err := os.WriteFile(filepath.Join(wd, "evidence.tar.gz"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, stdout, _ := run(t, "sessions"); !strings.Contains(stdout, "bundled") {
		t.Errorf("bundle not reported:\n%s", stdout)
	}
}

func TestStartArgErrors(t *testing.T) {
	t.Setenv(interview.HomeEnv, t.TempDir())
	if code, _, _ := run(t, "start", "--content", goodRoot); code != 2 {
		t.Error("start without a problem should be a usage error")
	}
	if code, _, stderr := run(t, "start", "pipeline-meltdown", "--content", goodRoot,
		"--level", "wizard"); code != 2 || !strings.Contains(stderr, "--level") {
		t.Errorf("bad level: exit %d, stderr %q", code, stderr)
	}
	if code, _, stderr := run(t, "start", "nope", "--content", goodRoot); code != 1 ||
		!strings.Contains(stderr, "no problem") {
		t.Errorf("unknown problem: exit %d, stderr %q", code, stderr)
	}
	if code, _, stderr := run(t, "start", "slow-aligner", "--content", goodRoot); code != 1 ||
		!strings.Contains(stderr, "only debugging problems") {
		t.Errorf("take-home: exit %d, stderr %q", code, stderr)
	}
	if code, _, stderr := run(t, "start", "pipeline-meltdown", "--content", goodRoot,
		"--seed", "Not A Seed"); code != 2 || !strings.Contains(stderr, "seed") {
		t.Errorf("bad seed: exit %d, stderr %q", code, stderr)
	}
	// Nothing above should have left a record behind.
	list, err := interview.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("failed starts recorded sessions: %v", list)
	}
}

func TestEndRefusesAnUnknownSession(t *testing.T) {
	t.Setenv(interview.HomeEnv, t.TempDir())
	if code, _, stderr := run(t, "end", "--seed", "no-such-session"); code == 0 ||
		!strings.Contains(stderr, "no session") {
		t.Errorf("end on an unknown seed: exit %d, stderr %q", code, stderr)
	}
	if code, _, stderr := run(t, "end"); code == 0 || !strings.Contains(stderr, "interviews start") {
		t.Errorf("end with nothing started: exit %d, stderr %q", code, stderr)
	}
}

// end tears down against the tree the session was built from. Comparing the
// --content value to the fallback used to stand in for "was the flag given",
// which stops being true the moment a machine configures a root: the default
// is the configured one, so it never equals the fallback, so the recorded
// root was always discarded and teardown resolved against the wrong tree.
func TestContentRootForPrefersTheRecordedTree(t *testing.T) {
	const flagRoot = "/flag/root"
	for _, tc := range []struct {
		name     string
		recorded string
		explicit bool
		want     string
	}{
		{"recorded wins over a defaulted flag", "/recorded", false, "/recorded"},
		{"an explicit flag wins", "/recorded", true, flagRoot},
		{"nothing recorded falls back to the flag", "", false, flagRoot},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := contentRootFor(tc.recorded, flagRoot, tc.explicit); got != tc.want {
				t.Errorf("contentRootFor(%q, %q, %v) = %q, want %q",
					tc.recorded, flagRoot, tc.explicit, got, tc.want)
			}
		})
	}
}

// passed has to read the command line, not the resulting value, or the case
// above comes straight back.
func TestPassedReadsTheCommandLineNotTheValue(t *testing.T) {
	fs := newBareFlagSet("test", io.Discard)
	fs.String("content", "/configured", "")
	if err := fs.Parse([]string{"--content", "/configured"}); err != nil {
		t.Fatal(err)
	}
	if !passed(fs, "content") {
		t.Error("a flag given explicitly, even with its default value, reads as not given")
	}
	other := newBareFlagSet("test", io.Discard)
	other.String("content", "/configured", "")
	if err := other.Parse(nil); err != nil {
		t.Fatal(err)
	}
	if passed(other, "content") {
		t.Error("a flag nobody typed reads as given")
	}
}

// A session started with --set resolves to a different workdir without
// those same flags, so grading has to read the recorded one rather than
// derive an empty directory and call it a session with no score.
func TestRecordedWorkdirBeatsDerivingOne(t *testing.T) {
	wd := stateFor(t, "01-image-typo")
	record(t, &interview.Session{Seed: "calm-bison-0731", Problem: "pipeline-meltdown", Workdir: wd})
	if err := grading.AppendHint(wd, grading.Hint{Minute: 3, Text: "the recorded ledger"}); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := run(t, "grade", "sheet", "pipeline-meltdown",
		"--content", goodRoot, "--seed", "calm-bison-0731", "--set", "fault_pack=pack-b")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if !strings.Contains(stdout, "the recorded ledger") {
		t.Errorf("sheet read a derived workdir instead of the recorded one:\n%s", stdout)
	}
}

// Handing out a take-home used to mean inventing a seed and keeping it
// somewhere yourself, which is the problem the registry exists to remove.
func TestBundleGeneratesAndRecordsASession(t *testing.T) {
	t.Setenv(interview.HomeEnv, t.TempDir())
	out := filepath.Join(t.TempDir(), "drop")
	code, stdout, stderr := run(t, "bundle", "slow-aligner", "--content", goodRoot,
		"-o", out, "--level", "mid", "--due", "120h")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if !strings.Contains(stdout, "session ") || !strings.Contains(stdout, "due:") {
		t.Errorf("stdout does not report the session it created: %q", stdout)
	}
	rec, err := interview.Current()
	if err != nil {
		t.Fatalf("bundling recorded no session: %v", err)
	}
	if rec.Mode != interview.Offline || rec.Stage != interview.Created {
		t.Errorf("record = mode %q stage %q", rec.Mode, rec.Stage)
	}
	if rec.Level != taxonomy.Mid {
		t.Errorf("level = %q, want mid", rec.Level)
	}
	if rec.BundlePath == "" || rec.DueAt.IsZero() {
		t.Errorf("record lost the bundle path or the deadline: %+v", rec)
	}
	// An explicit seed still pins, for regenerating an identical drop.
	if code, _, stderr := run(t, "bundle", "slow-aligner", "--content", goodRoot,
		"-o", filepath.Join(t.TempDir(), "again"), "--seed", "calm-bison-0731"); code != 0 {
		t.Fatalf("explicit seed: exit %d, stderr %q", code, stderr)
	}
	if rec, err := interview.Load("calm-bison-0731"); err != nil || rec.Stage != interview.Created {
		t.Errorf("explicit seed not recorded: %v", err)
	}
}

// The stage verbs are what a list of take-homes is read for: which ones are
// waiting on me. They get forgotten, so each is one word.
func TestOfflineStagesMoveASessionAlong(t *testing.T) {
	rec := record(t, &interview.Session{
		Seed: "eager-kestrel-0801", Problem: "slow-aligner", Mode: interview.Offline,
		Stage: interview.Created, DueAt: time.Now().Add(48 * time.Hour),
	})
	if code, _, stderr := run(t, "sent"); code != 0 {
		t.Fatalf("sent: exit %d, stderr %q", code, stderr)
	}
	if got := stateOf(t, rec.Seed); !strings.Contains(got, "due in") {
		t.Errorf("state = %q, want a deadline", got)
	}

	sub := t.TempDir()
	if code, stdout, stderr := run(t, "returned", sub); code != 0 ||
		!strings.Contains(stdout, "grade sheet") {
		t.Fatalf("returned: exit %d, stdout %q stderr %q", code, stdout, stderr)
	}
	after, err := interview.Load(rec.Seed)
	if err != nil {
		t.Fatal(err)
	}
	if after.SubmissionPath == "" {
		t.Error("returned did not record where the submission landed")
	}
	if got := offlineState(after); !strings.Contains(got, "to review") {
		t.Errorf("state = %q, want it to say the interviewer is next", got)
	}

	if code, _, stderr := run(t, "reviewed"); code != 0 {
		t.Fatalf("reviewed: exit %d, stderr %q", code, stderr)
	}
	done, err := interview.Load(rec.Seed)
	if err != nil {
		t.Fatal(err)
	}
	if done.ReviewedAt.IsZero() || done.EndedAt.IsZero() {
		t.Error("a reviewed interview is over and should leave the open list")
	}
	// And it still reads as reviewed rather than as a generic ended session.
	if got := sessionState(done); got != "reviewed" {
		t.Errorf("state = %q, want reviewed", got)
	}
}

func stateOf(t *testing.T, seed string) string {
	t.Helper()
	rec, err := interview.Load(seed)
	if err != nil {
		t.Fatal(err)
	}
	return sessionState(rec)
}

// returned takes the path because grading reads it, and a path that is not
// there is a typo worth catching now rather than at the debrief.
func TestReturnedRefusesAMissingPath(t *testing.T) {
	record(t, &interview.Session{
		Seed: "eager-kestrel-0801", Problem: "slow-aligner",
		Mode: interview.Offline, Stage: interview.Sent,
	})
	if code, _, _ := run(t, "returned", filepath.Join(t.TempDir(), "nope")); code == 0 {
		t.Error("accepted a submission path that does not exist")
	}
	if code, _, _ := run(t, "returned"); code != 2 {
		t.Error("returned with no path should be a usage error")
	}
}

// A debugging session has no stages: its state comes from the environment,
// and end is what finishes it.
func TestStageVerbsRefuseADebuggingSession(t *testing.T) {
	record(t, &interview.Session{
		Seed: "calm-bison-0731", Problem: "pipeline-meltdown", Mode: interview.Local,
	})
	code, _, stderr := run(t, "sent")
	if code == 0 {
		t.Error("marked a debugging session as sent")
	}
	if !strings.Contains(stderr, "interviews end") {
		t.Errorf("stderr does not point at the right command: %q", stderr)
	}
}

// A provisioned host keeps its workdir on the host, so hint had nothing to
// write to and dead-ended, on the command start --remote tells you to run
// and the one most typed with a candidate waiting. The advice it gave named
// a flag with no value the interviewer could know.
func TestHintWorksOnASessionWithNoWorkdir(t *testing.T) {
	t.Setenv(interview.HomeEnv, t.TempDir())
	rec := record(t, &interview.Session{
		Seed: "quiet-badger-0801", Problem: "relay", Mode: interview.AWS,
		Host: "203.0.113.7", CreatedAt: time.Now().Add(-12 * time.Minute),
	})
	rec.Workdir = ""
	if err := interview.Save(rec); err != nil {
		t.Fatal(err)
	}
	if err := interview.SetCurrent(rec.Seed); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := run(t, "hint", "nudged them toward the events")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if !strings.Contains(stdout, "minute 12") {
		t.Errorf("stdout = %q, want the measured minute", stdout)
	}

	// And grading has to find it without being told where it went, or the
	// hint is logged and never read, which is the same as not logging it.
	dir, err := interview.HintsDir(rec.Seed)
	if err != nil {
		t.Fatal(err)
	}
	hints, err := grading.LoadHintsFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(hints) != 1 || hints[0].Text != "nudged them toward the events" {
		t.Errorf("ledger = %v, want the one hint", hints)
	}
}

// A typo used to be collected as a positional and dropped, so "sessions shwo"
// printed the plain listing and exited 0, which reads as a legitimate answer.
func TestSessionsRejectsAnUnknownVerb(t *testing.T) {
	if code, _, stderr := run(t, "sessions", "shwo"); code != 2 ||
		!strings.Contains(stderr, `no verb "shwo"`) {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
	// The verb only dispatches in first position, so after a flag it was
	// dropped just the same.
	if code, _, stderr := run(t, "sessions", "--all", "show"); code != 2 ||
		!strings.Contains(stderr, `no verb "show"`) {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
}

// The follow loop only reads the log when it grows, and a finished provision
// never writes again, so the finish line has to be seen in the log already
// printed. Without that, this follow sat out the full boot timeout. The aws
// CLI is stubbed on PATH; the log it serves is real all the way down.
func TestSessionsLogFollowExitsOnAFinishedLog(t *testing.T) {
	t.Setenv(interview.HomeEnv, t.TempDir())
	bin := t.TempDir()
	script := "#!/bin/sh\nprintf 'booting\\nprovisioning finished\\n'\n"
	if err := os.WriteFile(filepath.Join(bin, "aws"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	record(t, &interview.Session{Seed: "done-host-0801", Problem: "relay",
		Mode: interview.AWS, Evidence: "s3://bucket/done-host-0801/"})

	type result struct {
		code   int
		stdout string
	}
	done := make(chan result, 1)
	go func() {
		code, stdout, _ := run(t, "sessions", "log", "done-host-0801", "--follow")
		done <- result{code, stdout}
	}()
	select {
	case got := <-done:
		if got.code != 0 || !strings.Contains(got.stdout, "provisioning finished") {
			t.Errorf("exit %d, stdout %q", got.code, got.stdout)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("sessions log --follow did not exit on an already-finished log")
	}
}
