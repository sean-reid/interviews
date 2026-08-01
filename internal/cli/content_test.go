package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// contentClone makes a throwaway clone of this repository and returns the
// clone and its content root. Real git against a real repository, because
// what is under test is what git reports; and cloning rather than building a
// fixture means no commit is ever authored here.
func contentClone(t *testing.T) (clone, root string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	here, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// The package directory is not the repository root, and clone wants the
	// root.
	top, err := git(here, "rev-parse", "--show-toplevel")
	if err != nil || top == "" {
		t.Skip("not running inside a git checkout")
	}
	clone = filepath.Join(t.TempDir(), "clone")
	// Shared objects, so this costs nothing even on a large history.
	if out, err := exec.Command("git", "clone", "--quiet", "--shared", top, clone).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	// CI checks out detached with no local branch, and cloning that yields a
	// clone with no branch and no upstream. Build both explicitly so the
	// fixture is the same everywhere instead of inheriting whatever state the
	// surrounding checkout happens to be in.
	for _, args := range [][]string{
		{"fetch", "--quiet", "origin", "HEAD:refs/remotes/origin/fixture"},
		{"checkout", "--quiet", "-B", "fixture", "refs/remotes/origin/fixture"},
		{"branch", "--quiet", "--set-upstream-to=origin/fixture", "fixture"},
	} {
		cmd := exec.Command("git", append([]string{"-C", clone}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	root = filepath.Join(clone, "content")
	if _, err := os.Stat(root); err != nil {
		t.Skipf("no content tree to test against: %v", err)
	}
	return clone, root
}

func TestContentStatusOnACurrentCheckout(t *testing.T) {
	clone, root := contentClone(t)
	s := contentStatus(root, false)
	if s.Repo == "" {
		t.Fatal("did not recognize a git checkout")
	}
	if filepath.Base(s.Repo) != "clone" {
		t.Errorf("repo = %q, want the clone", s.Repo)
	}
	if s.Behind != 0 {
		t.Errorf("fresh clone reads as %d behind", s.Behind)
	}
	if s.Dirty {
		t.Error("fresh clone reads as dirty")
	}
	if s.Upstream != "origin/fixture" {
		t.Errorf("upstream = %q, want origin/fixture", s.Upstream)
	}
	if s.Branch != "fixture" {
		t.Errorf("branch = %q, want fixture", s.Branch)
	}
	_ = clone
}

// Running an interview against last month's problems is the failure this
// exists to catch, and the check has to work without touching the network:
// start runs it with a candidate waiting.
func TestContentStatusSeesCommitsItDoesNotHave(t *testing.T) {
	clone, root := contentClone(t)
	if out, err := exec.Command("git", "-C", clone, "reset", "--quiet", "--hard", "HEAD~1").CombinedOutput(); err != nil {
		t.Skipf("history too short to rewind: %v\n%s", err, out)
	}

	// How many commits behind depends on the shape of the history the test
	// happens to run against: CI builds a merge commit, so rewinding one step
	// puts the whole merged side ahead of us. What matters is that being
	// behind is detected without a fetch, and that the number reported is the
	// number warned about.
	s := contentStatus(root, false)
	if s.Behind < 1 {
		t.Fatalf("behind = %d after rewinding, want at least 1", s.Behind)
	}

	// git reports the resolved path, which on macOS is not the one TempDir
	// handed out.
	resolved, err := filepath.EvalSymlinks(clone)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	warnStale(root, &out)
	for _, want := range []string{
		fmt.Sprintf("%d commit(s) behind origin/fixture", s.Behind),
		"git -C " + resolved + " pull",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("warning missing %q: %q", want, out.String())
		}
	}
}

func TestContentStatusSeesUncommittedChanges(t *testing.T) {
	_, root := contentClone(t)
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) == 0 {
		t.Skip("no content to edit")
	}
	scratch := filepath.Join(root, "scratch-for-test.txt")
	if err := os.WriteFile(scratch, []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s := contentStatus(root, false); !s.Dirty {
		t.Error("an untracked file under content does not read as dirty")
	}
	var out strings.Builder
	warnStale(root, &out)
	if !strings.Contains(out.String(), "uncommitted changes") {
		t.Errorf("no warning: %q", out.String())
	}
}

// The dirty check is scoped to the content root: what the rest of the
// repository is doing is none of our business, and warning about it would
// train the operator to ignore the warning.
func TestContentStatusIgnoresChangesOutsideContent(t *testing.T) {
	clone, root := contentClone(t)
	if err := os.WriteFile(filepath.Join(clone, "scratch-for-test.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s := contentStatus(root, false); s.Dirty {
		t.Error("a change outside content reads as dirty content")
	}
}

// A content tree that is a plain directory is a perfectly good content tree,
// and must produce neither warnings nor errors.
func TestContentStatusOutsideARepositoryIsSilent(t *testing.T) {
	dir := t.TempDir()
	if s := contentStatus(dir, true); s.Repo != "" {
		t.Errorf("found a repo where there is none: %+v", s)
	}
	var out strings.Builder
	warnStale(dir, &out)
	if out.String() != "" {
		t.Errorf("warned about a plain directory: %q", out.String())
	}
}
