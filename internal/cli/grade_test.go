package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sean-reid/interviews/internal/grading"
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
		!strings.Contains(stderr, "--seed is required") {
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
