package cli

import (
	"strings"
	"testing"
)

// These cover argument handling; engine behavior is tested in internal/debug
// against a fake runner, and real environments are proven in CI.

func TestEnvArgErrors(t *testing.T) {
	if code, _, stderr := run(t, "env", "up", "pipeline-meltdown", "--content", goodRoot); code != 1 ||
		!strings.Contains(stderr, "no session yet") {
		t.Errorf("env up without seed: exit %d, stderr %q", code, stderr)
	}
	if code, _, _ := run(t, "env", "up", "--content", goodRoot, "--seed", "s"); code != 2 {
		t.Error("env up without problem id should be a usage error")
	}
	if code, _, _ := run(t, "env", "reboot", "x", "--content", goodRoot, "--seed", "s"); code != 2 {
		t.Error("unknown env verb should be a usage error")
	}
	if code, _, stderr := run(t, "env", "up", "nope", "--content", goodRoot, "--seed", "s"); code != 1 ||
		!strings.Contains(stderr, "no problem") {
		t.Errorf("env up unknown problem: exit %d, stderr %q", code, stderr)
	}
	if code, _, stderr := run(t, "env", "up", "slow-aligner", "--content", goodRoot, "--seed", "s"); code != 1 ||
		!strings.Contains(stderr, "only debugging problems") {
		t.Errorf("env up on a takehome: exit %d, stderr %q", code, stderr)
	}
}

func TestBreakAndFaultArgErrors(t *testing.T) {
	if code, _, _ := run(t, "break", "--content", goodRoot, "--seed", "s"); code != 2 {
		t.Error("break without problem id should be a usage error")
	}
	if code, _, _ := run(t, "fault", "status", "--content", goodRoot, "--seed", "s"); code != 2 {
		t.Error("fault status without problem id should be a usage error")
	}
	if code, _, _ := run(t, "fault", "poke", "pipeline-meltdown", "--content", goodRoot, "--seed", "s"); code != 2 {
		t.Error("unknown fault verb should be a usage error")
	}
}

// The fixture's 03-cannot-check exits 2 for real, so this covers the whole
// path: the script, its exit status, the engine, the table.
func TestFaultStatusNamesACheckThatCannotRun(t *testing.T) {
	wd := stateFor(t, "01-image-typo", "03-cannot-check")
	code, stdout, stderr := run(t, "fault", "status", "pipeline-meltdown",
		"--content", goodRoot, "--seed", "test-seed", "--set", "fault_pack=pack-b", "--workdir", wd)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	for _, want := range []string{"01-image-typo", "FIXED", "03-cannot-check", "CHECK-CANNOT-RUN"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("status table missing %q:\n%s", want, stdout)
		}
	}
	if !strings.Contains(stderr, "exited 2") {
		t.Errorf("stderr says nothing about the broken check: %q", stderr)
	}
}

func TestProveArgErrors(t *testing.T) {
	if code, _, _ := run(t, "prove", "--content", goodRoot); code != 2 {
		t.Error("prove without problem id should be a usage error")
	}
	if code, _, stderr := run(t, "prove", "pipeline-meltdown", "--content", goodRoot, "--pack", "pack-z"); code != 1 ||
		!strings.Contains(stderr, `no pack "pack-z"`) {
		t.Errorf("prove unknown pack: exit %d, stderr %q", code, stderr)
	}
	if code, _, stderr := run(t, "prove", "slow-aligner", "--content", goodRoot); code != 1 ||
		!strings.Contains(stderr, "no debugging problem") {
		t.Errorf("prove on a takehome: exit %d, stderr %q", code, stderr)
	}
	if code, _, stderr := run(t, "prove", "pipeline-meltdown", "--content", goodRoot,
		"--set", "fault_pack=pack-b"); code != 2 || !strings.Contains(stderr, "--pack") {
		t.Errorf("prove --set fault_pack: exit %d, stderr %q", code, stderr)
	}
	// A pinned value has to reach variant resolution, not merely parse: an
	// out-of-range one is rejected there and nowhere else.
	if code, _, stderr := run(t, "prove", "pipeline-meltdown", "--content", goodRoot,
		"--pack", "pack-a", "--set", "scale=99"); code != 1 ||
		!strings.Contains(stderr, "outside") {
		t.Errorf("prove --set out of range: exit %d, stderr %q", code, stderr)
	}
}

// Both commands print the injected faults and both get run mid-interview,
// when a screen is often shared. Unmarked, that output reads as ordinary
// progress.
func TestAnswerKeyOutputIsMarked(t *testing.T) {
	wd := stateFor(t, "01-image-typo", "03-cannot-check")
	_, stdout, stderr := run(t, "fault", "status", "pipeline-meltdown",
		"--content", goodRoot, "--seed", "test-seed", "--set", "fault_pack=pack-b", "--workdir", wd)
	if !strings.Contains(stdout, "interviewer only") {
		t.Errorf("fault status did not mark its output: %q %q", stdout, stderr)
	}
}
