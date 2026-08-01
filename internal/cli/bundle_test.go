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
	if !strings.Contains(stdout, "bundle:  ") {
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

// A design problem is delivered as a document, so the drop is the rendered
// candidate tree and a front page, with no git repository around it.
func TestBundleCommandSysDesign(t *testing.T) {
	out := filepath.Join(t.TempDir(), "design")
	code, stdout, stderr := run(t, "bundle", "--content", goodRoot, "global-feed",
		"--seed", "calm-bison-0731", "-o", out)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if !strings.Contains(stdout, "bundle:  ") {
		t.Errorf("stdout = %q", stdout)
	}
	about, err := os.ReadFile(filepath.Join(out, "ABOUT.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(about), "candidate/constraints.md") {
		t.Errorf("ABOUT.md does not point at the constraint sheet:\n%s", about)
	}
	for _, want := range []string{"candidate/brief.md", "candidate/constraints.md"} {
		if _, err := os.Stat(filepath.Join(out, want)); err != nil {
			t.Errorf("%s missing: %v", want, err)
		}
	}
	for _, gone := range []string{".git", "interviewer", "review.yaml"} {
		if _, err := os.Stat(filepath.Join(out, gone)); err == nil {
			t.Errorf("%s reached the bundle", gone)
		}
	}
}

// A problem with validation errors must not be handed out: a rejected
// visibility glob leaves nothing candidate-visible, and the drop would be a
// front page over an empty tree.
func TestBundleCommandRefusesInvalidProblem(t *testing.T) {
	root := filepath.Join(t.TempDir(), "content")
	if err := os.CopyFS(root, os.DirFS(goodRoot)); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(root, "sysdesign", "global-feed", "problem.yaml")
	raw, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	broken := strings.Replace(string(raw), "candidate: [candidate/**]",
		"candidate: [candidate/**, interviewer/**]", 1)
	if broken == string(raw) {
		t.Fatal("fixture visibility line changed; update this test")
	}
	if err := os.WriteFile(manifest, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "design")
	code, _, stderr := run(t, "bundle", "--content", root, "global-feed",
		"--seed", "s", "-o", out)
	if code != 1 || !strings.Contains(stderr, "does not validate") ||
		!strings.Contains(stderr, "can never be candidate-visible") {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("bundle written for a problem that does not validate")
	}
}

func TestBundleCommandErrors(t *testing.T) {
	dir := t.TempDir()
	if code, _, stderr := run(t, "bundle", "--content", goodRoot, "pipeline-meltdown",
		"--seed", "s", "-o", filepath.Join(dir, "a")); code != 1 ||
		!strings.Contains(stderr, "only take-home and system design") {
		t.Errorf("debugging problem: exit %d, stderr %q", code, stderr)
	}
	if code, _, stderr := run(t, "bundle", "--content", goodRoot, "nope",
		"--seed", "s", "-o", filepath.Join(dir, "b")); code != 1 ||
		!strings.Contains(stderr, "no problem") {
		t.Errorf("unknown problem: exit %d, stderr %q", code, stderr)
	}
	// No --seed is not an error: one gets generated and recorded, which is the
	// whole point of the registry.
	if code, stdout, stderr := run(t, "bundle", "--content", goodRoot, "slow-aligner",
		"-o", filepath.Join(dir, "c")); code != 0 || !strings.Contains(stdout, "session ") {
		t.Errorf("no --seed: exit %d, stdout %q stderr %q", code, stdout, stderr)
	}
	if code, _, stderr := run(t, "bundle", "--content", goodRoot, "slow-aligner",
		"--seed", "Not A Seed", "-o", filepath.Join(dir, "d")); code != 2 ||
		!strings.Contains(stderr, "seed") {
		t.Errorf("bad seed: exit %d, stderr %q", code, stderr)
	}
	if code, _, _ := run(t, "bundle", "--content", goodRoot, "slow-aligner",
		"--seed", "s"); code != 2 {
		t.Errorf("missing -o: exit %d, want 2", code)
	}
}
