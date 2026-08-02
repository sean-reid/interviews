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
	"github.com/sean-reid/interviews/internal/taxonomy"
)

// stateFor writes engine state as env up would have, so grade commands run
// against real check scripts from the testdata scenario without docker.
func stateFor(t *testing.T, injected ...string) string {
	t.Helper()
	wd := t.TempDir()
	st := map[string]any{
		"problem": "pipeline-meltdown", "interview_id": "test-seed",
		"pack": "pack-b", "injected": injected, "provider": "kind",
		"created_at": "2026-07-31T00:00:00Z",
	}
	raw, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wd, "state.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return wd
}

func TestGradeScoreWritesScoreJSON(t *testing.T) {
	wd := stateFor(t, "01-image-typo", "02-net-policy")
	code, stdout, stderr := run(t, "grade", "score", "pipeline-meltdown",
		"--content", goodRoot, "--seed", "test-seed", "--set", "fault_pack=pack-b", "--workdir", wd)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	// The testdata check scripts exit 0, so everything reports fixed.
	if !strings.Contains(stdout, "score: 2/2 faults fixed, end-to-end verify passed") {
		t.Errorf("stdout = %q", stdout)
	}
	score, err := grading.LoadScore(wd)
	if err != nil || score == nil {
		t.Fatalf("LoadScore = %v, %v", score, err)
	}
	if score.Fixed != 2 || score.Total != 2 || !score.Verified || score.Pack != "pack-b" {
		t.Errorf("score = %+v", score)
	}
}

func TestGradeSheetEmbedsScoreAndHints(t *testing.T) {
	wd := stateFor(t, "01-image-typo")
	if code, _, stderr := run(t, "grade", "score", "pipeline-meltdown",
		"--content", goodRoot, "--seed", "test-seed", "--set", "fault_pack=pack-b", "--workdir", wd); code != 0 {
		t.Fatalf("grade score failed: %s", stderr)
	}
	if code, _, stderr := run(t, "grade", "hint", "pipeline-meltdown", "look at the events",
		"--content", goodRoot, "--seed", "test-seed", "--minute", "17", "--workdir", wd); code != 0 {
		t.Fatalf("grade hint failed: %s", stderr)
	}

	code, stdout, stderr := run(t, "grade", "sheet", "pipeline-meltdown",
		"--content", goodRoot, "--seed", "test-seed", "--workdir", wd)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	for _, want := range []string{
		"# Grading sheet: Pipeline meltdown",
		"| 01-image-typo: Image tag typo | easy | yes | | |",
		"| 17 | look at the events |",
		"Tool and AI wrangling",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("sheet missing %q", want)
		}
	}
}

// The interviewer has no shell on the session host, so hints are logged into
// a workdir on their own machine while the score arrives with the evidence.
// The sheet has to bring the two together.
func TestGradeSheetMergesHintsLoggedElsewhere(t *testing.T) {
	evidence := stateFor(t, "01-image-typo")
	if code, _, stderr := run(t, "grade", "score", "pipeline-meltdown",
		"--content", goodRoot, "--seed", "test-seed", "--set", "fault_pack=pack-b",
		"--workdir", evidence); code != 0 {
		t.Fatalf("grade score failed: %s", stderr)
	}

	local := t.TempDir()
	for _, hint := range [][2]string{{"9", "asked what the logs said"}, {"31", "pointed at the network policy"}} {
		if code, _, stderr := run(t, "grade", "hint", "pipeline-meltdown", hint[1],
			"--content", goodRoot, "--seed", "test-seed", "--minute", hint[0],
			"--workdir", local); code != 0 {
			t.Fatalf("grade hint failed: %s", stderr)
		}
	}
	if _, err := os.Stat(filepath.Join(evidence, grading.HintsFile)); err == nil {
		t.Fatal("hints landed in the evidence workdir; this test no longer covers the split")
	}

	code, stdout, stderr := run(t, "grade", "sheet", "pipeline-meltdown",
		"--content", goodRoot, "--seed", "test-seed", "--workdir", evidence, "--hints", local)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	for _, want := range []string{
		"| 01-image-typo: Image tag typo | easy | yes | | |",
		"| 9 | asked what the logs said |",
		"| 31 | pointed at the network policy |",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("sheet missing %q", want)
		}
	}
	if strings.Index(stdout, "minute 31") > 0 && strings.Index(stdout, "| 31 |") < strings.Index(stdout, "| 9 |") {
		t.Error("hints out of session order")
	}

	// Naming the ledger the evidence already carries must not double it.
	if err := os.Rename(filepath.Join(local, grading.HintsFile),
		filepath.Join(evidence, grading.HintsFile)); err != nil {
		t.Fatal(err)
	}
	_, stdout, stderr = run(t, "grade", "sheet", "pipeline-meltdown",
		"--content", goodRoot, "--seed", "test-seed", "--workdir", evidence, "--hints", evidence)
	if stderr != "" {
		t.Fatalf("stderr %q", stderr)
	}
	if got := strings.Count(stdout, "asked what the logs said"); got != 1 {
		t.Errorf("hint appears %d times, want 1", got)
	}
}

func TestGradeSheetToFile(t *testing.T) {
	out := filepath.Join(t.TempDir(), "sheet.md")
	code, stdout, stderr := run(t, "grade", "sheet", "pipeline-meltdown",
		"--content", goodRoot, "--seed", "s", "-o", out)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout should be empty with -o, got %q", stdout)
	}
	raw, err := os.ReadFile(out)
	if err != nil || !strings.Contains(string(raw), "## Rubric") {
		t.Errorf("file sheet = %v, %q", err, raw)
	}
}

func TestGradeArgErrors(t *testing.T) {
	if code, _, _ := run(t, "grade"); code != 2 {
		t.Error("bare grade should be a usage error")
	}
	if code, _, _ := run(t, "grade", "audit", "x"); code != 2 {
		t.Error("unknown grade verb should be a usage error")
	}
	if code, _, stderr := run(t, "grade", "sheet", "pipeline-meltdown", "--content", goodRoot); code != 1 ||
		!strings.Contains(stderr, "no session yet") {
		t.Errorf("sheet without seed: exit %d, stderr %q", code, stderr)
	}
	if code, _, _ := run(t, "grade", "hint", "pipeline-meltdown", "text",
		"--content", goodRoot, "--seed", "s", "--minute", "x"); code != 2 {
		t.Error("non-numeric minute should be a usage error")
	}
	if code, _, stderr := run(t, "grade", "score", "pipeline-meltdown",
		"--content", goodRoot, "--seed", "s", "--workdir", t.TempDir()); code != 1 ||
		!strings.Contains(stderr, "env up first") {
		t.Errorf("score without state: exit %d, stderr %q", code, stderr)
	}
}

// stateWithOverrides writes state recording the overrides the environment
// was built with, the way env up does.
func stateWithOverrides(t *testing.T, overrides map[string]string, injected ...string) string {
	t.Helper()
	wd := t.TempDir()
	st := map[string]any{
		"problem": "pipeline-meltdown", "interview_id": "test-seed",
		"overrides": overrides, "pack": overrides["fault_pack"],
		"injected": injected, "provider": "kind",
		"created_at": "2026-07-31T00:00:00Z",
	}
	raw, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wd, "state.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return wd
}

// The fault table comes from score.json, written by the environment that
// was actually built. The Variant row has to agree with it.
func TestGradeSheetUsesTheRecordedOverrides(t *testing.T) {
	wd := stateWithOverrides(t, map[string]string{"fault_pack": "pack-a", "scale": "4"}, "01-image-typo")

	code, stdout, stderr := run(t, "grade", "sheet", "pipeline-meltdown",
		"--content", goodRoot, "--seed", "test-seed", "--workdir", wd)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	// Checked pair by pair rather than as a whole row: the row also carries
	// seed-derived parameters, and pinning their values here would make this
	// test about variant resolution instead of about the recorded overrides.
	for _, want := range []string{"fault_pack=pack-a", "scale=4", "team_name=umbrella"} {
		if !strings.Contains(variantRow(t, stdout), want) {
			t.Errorf("variant row ignores the recorded override %s:\n%s", want, variantRow(t, stdout))
		}
	}

	// An explicit --set is the interviewer overruling the record.
	code, stdout, stderr = run(t, "grade", "sheet", "pipeline-meltdown",
		"--content", goodRoot, "--seed", "test-seed", "--workdir", wd, "--set", "fault_pack=pack-b")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if !strings.Contains(variantRow(t, stdout), "fault_pack=pack-b") {
		t.Errorf("--set did not win over the recorded overrides:\n%s", variantRow(t, stdout))
	}
}

// variantRow pulls the sheet's Variant line out, so an assertion about the
// parameters does not have to quote the whole sheet.
func variantRow(t *testing.T, sheet string) string {
	t.Helper()
	for line := range strings.SplitSeq(sheet, "\n") {
		if strings.HasPrefix(line, "| Variant |") {
			return line
		}
	}
	t.Fatal("sheet has no Variant row")
	return ""
}

// Content moves on; a workdir recording a parameter the problem no longer
// declares must still render a sheet.
func TestGradeSheetSurvivesStaleRecordedOverrides(t *testing.T) {
	wd := stateWithOverrides(t, map[string]string{"fault_pack": "pack-a", "gone": "x"}, "01-image-typo")
	code, stdout, stderr := run(t, "grade", "sheet", "pipeline-meltdown",
		"--content", goodRoot, "--seed", "test-seed", "--workdir", wd)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if !strings.Contains(stderr, "ignoring the overrides recorded in state.json") {
		t.Errorf("no warning about the unusable record: %q", stderr)
	}
	if !strings.Contains(stdout, "| Variant |") {
		t.Errorf("no sheet rendered:\n%s", stdout)
	}
}

// A typo in --seed used to derive a workdir, create it, and report success,
// leaving the hint in a ledger nothing will ever read. It is the command
// typed most often, and typed mid-conversation.
func TestGradeHintRefusesASeedWithNoSession(t *testing.T) {
	code, stdout, stderr := run(t, "grade", "hint", "pipeline-meltdown", "asked about the events",
		"--content", goodRoot, "--seed", "no-such-sessionn", "--minute", "9")
	if code == 0 {
		t.Errorf("exit 0 for an unknown seed, stdout %q", stdout)
	}
	if !strings.Contains(stderr, "no session for --seed no-such-sessionn") {
		t.Errorf("stderr does not name the seed: %q", stderr)
	}
}

// The remote flow logs hints from the interviewer's machine, where no
// environment exists, so an explicit directory still gets created.
func TestGradeHintCreatesAnExplicitWorkdirAndNamesTheLedger(t *testing.T) {
	wd := filepath.Join(t.TempDir(), "calm-bison-0731")
	code, stdout, stderr := run(t, "grade", "hint", "pipeline-meltdown", "asked about the events",
		"--content", goodRoot, "--seed", "test-seed", "--minute", "9", "--workdir", wd)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	ledger := filepath.Join(wd, "hints.json")
	if !strings.Contains(stdout, ledger) {
		t.Errorf("output does not name the ledger it wrote: %q", stdout)
	}
	if _, err := os.Stat(ledger); err != nil {
		t.Errorf("no ledger written: %v", err)
	}
}

// The level decides how the rubric is read. start records it, so the sheet
// should not ask for it again.
func TestGradeSheetTakesTheLevelFromTheSession(t *testing.T) {
	wd := stateFor(t, "01-image-typo")
	record(t, &interview.Session{
		Seed: "calm-bison-0731", Problem: "pipeline-meltdown",
		Level: taxonomy.Senior, Workdir: wd,
	})
	code, stdout, stderr := run(t, "grade", "sheet", "pipeline-meltdown", "--content", goodRoot)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if !strings.Contains(stdout, "| Level targeted | senior |") {
		t.Errorf("sheet did not pick up the recorded level:\n%s", stdout)
	}
	// An explicit flag still wins, for grading one interview against a
	// different band.
	_, stdout, _ = run(t, "grade", "sheet", "pipeline-meltdown", "--content", goodRoot, "--level", "staff")
	if !strings.Contains(stdout, "| Level targeted | staff |") {
		t.Errorf("--level ignored:\n%s", stdout)
	}
	if code, _, stderr := run(t, "grade", "sheet", "pipeline-meltdown", "--content", goodRoot,
		"--level", "wizard"); code != 2 || !strings.Contains(stderr, "--level") {
		t.Errorf("bad level: exit %d, stderr %q", code, stderr)
	}
}

// The other half of hinting a remote session: interviews hint puts the
// ledger in a per-seed location because there is no workdir here, and the
// sheet has to pick it up without being handed --hints. A hint nobody reads
// is the same as one never logged.
func TestGradeSheetFindsHintsLoggedAgainstASeed(t *testing.T) {
	t.Setenv(interview.HomeEnv, t.TempDir())
	evidence := stateFor(t, "01-image-typo")
	if code, _, stderr := run(t, "grade", "score", "pipeline-meltdown",
		"--content", goodRoot, "--seed", "test-seed", "--set", "fault_pack=pack-b",
		"--workdir", evidence); code != 0 {
		t.Fatalf("grade score failed: %s", stderr)
	}
	remote := record(t, &interview.Session{
		Seed: "test-seed", Problem: "pipeline-meltdown", Mode: interview.AWS,
		Host: "203.0.113.7", CreatedAt: time.Now().Add(-20 * time.Minute),
	})
	// A provisioned host keeps its workdir on the host, which is the whole
	// reason the ledger has to live somewhere else.
	remote.Workdir = ""
	if err := interview.Save(remote); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := run(t, "hint", "asked what the events said",
		"--seed", "test-seed", "--minute", "14"); code != 0 {
		t.Fatalf("hint failed: %s", stderr)
	}

	// No --hints: finding it is the point.
	code, stdout, stderr := run(t, "grade", "sheet", "pipeline-meltdown",
		"--content", goodRoot, "--seed", "test-seed", "--workdir", evidence)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if !strings.Contains(stdout, "| 14 | asked what the events said |") {
		t.Errorf("the sheet does not carry the hint:\n%s", stdout)
	}
}

// The seed and the problem were resolved separately and never compared, so
// naming the wrong problem rendered a full sheet, headed with that problem's
// title and carrying this session's interview id, and exited 0.
func TestGradeSheetRefusesAProblemTheSessionIsNotFor(t *testing.T) {
	t.Setenv(interview.HomeEnv, t.TempDir())
	evidence := stateFor(t, "01-image-typo")
	record(t, &interview.Session{
		Seed: "test-seed", Problem: "pipeline-meltdown", Mode: interview.Local,
		Workdir: evidence,
	})

	// Positive control: the right problem still grades.
	if code, _, stderr := run(t, "grade", "sheet", "pipeline-meltdown",
		"--content", goodRoot, "--seed", "test-seed", "--workdir", evidence); code != 0 {
		t.Fatalf("the correct problem was refused (%s); this test proves nothing", stderr)
	}

	code, stdout, stderr := run(t, "grade", "sheet", "global-feed",
		"--content", goodRoot, "--seed", "test-seed", "--workdir", evidence)
	if code == 0 {
		t.Errorf("graded a problem the candidate never saw, exit 0:\n%s", stdout)
	}
	for _, want := range []string{"test-seed", "pipeline-meltdown", "global-feed"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, want it to name %q", stderr, want)
		}
	}
}
