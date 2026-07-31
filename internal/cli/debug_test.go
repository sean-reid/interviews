package cli

import (
	"strings"
	"testing"
)

// These cover argument handling; engine behavior is tested in internal/debug
// against a fake runner, and real environments are proven in CI.

func TestEnvArgErrors(t *testing.T) {
	if code, _, stderr := run(t, "env", "up", "pipeline-meltdown", "--content", goodRoot); code != 1 ||
		!strings.Contains(stderr, "--seed is required") {
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
}
