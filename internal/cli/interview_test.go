package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sean-reid/interviews/internal/grading"
	"github.com/sean-reid/interviews/internal/interview"
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
		{"broken", map[string]any{"injected": []string{"01-x"}}, "broken"},
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
