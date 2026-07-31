package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
}

func TestBundleCommand(t *testing.T) {
	requireGit(t)
	out := filepath.Join(t.TempDir(), "bundle")
	code, stdout, stderr := run(t, "bundle", "--content", goodRoot, "slow-aligner",
		"--seed", "calm-bison-0731", "--set", "dataset=large", "-o", out)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if !strings.Contains(stdout, "wrote bundle") {
		t.Errorf("stdout = %q", stdout)
	}
	if _, err := os.Stat(filepath.Join(out, "ABOUT.md")); err != nil {
		t.Errorf("ABOUT.md missing: %v", err)
	}
	brief, err := os.ReadFile(filepath.Join(out, "candidate", "brief.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(brief), "large dataset") || strings.Contains(string(brief), "{{") {
		t.Errorf("brief not rendered: %q", brief)
	}
	info, err := os.Stat(filepath.Join(out, "harness", "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Errorf("exec bit lost: %v", info.Mode())
	}
	if _, err := os.Stat(filepath.Join(out, "interviewer")); err == nil {
		t.Error("interviewer/ reached the bundle")
	}
}

func TestBundleCommandTarball(t *testing.T) {
	requireGit(t)
	out := filepath.Join(t.TempDir(), "drop.tgz")
	code, _, stderr := run(t, "bundle", "--content", goodRoot, "slow-aligner",
		"--seed", "calm-bison-0731", "-o", out)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	info, err := os.Stat(out)
	if err != nil || info.Size() == 0 {
		t.Errorf("tarball not written: %v", err)
	}
}

func TestBundleCommandErrors(t *testing.T) {
	dir := t.TempDir()
	if code, _, stderr := run(t, "bundle", "--content", goodRoot, "pipeline-meltdown",
		"--seed", "s", "-o", filepath.Join(dir, "a")); code != 1 ||
		!strings.Contains(stderr, "only take-home") {
		t.Errorf("debugging problem: exit %d, stderr %q", code, stderr)
	}
	if code, _, stderr := run(t, "bundle", "--content", goodRoot, "nope",
		"--seed", "s", "-o", filepath.Join(dir, "b")); code != 1 ||
		!strings.Contains(stderr, "no problem") {
		t.Errorf("unknown problem: exit %d, stderr %q", code, stderr)
	}
	if code, _, _ := run(t, "bundle", "--content", goodRoot, "slow-aligner",
		"-o", filepath.Join(dir, "c")); code != 2 {
		t.Errorf("missing --seed: exit %d, want 2", code)
	}
	if code, _, _ := run(t, "bundle", "--content", goodRoot, "slow-aligner",
		"--seed", "s"); code != 2 {
		t.Errorf("missing -o: exit %d, want 2", code)
	}
}
