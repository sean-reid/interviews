package leak

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"testing/fstest"
)

func mustClassifier(t *testing.T, globs ...string) *Classifier {
	t.Helper()
	c, err := NewClassifier(globs)
	if err != nil {
		t.Fatalf("NewClassifier(%v): %v", globs, err)
	}
	return c
}

func TestGlobMatching(t *testing.T) {
	tests := []struct {
		pattern, name string
		want          bool
	}{
		{"candidate/**", "candidate/brief.md", true},
		{"candidate/**", "candidate/deep/nested/file.go", true},
		{"candidate/**", "candidates/brief.md", false},
		{"candidate/**", "brief.md", false},
		{"**/*.md", "a/b/c.md", true},
		{"**/*.md", "c.md", true},
		{"**/*.md", "a/b/c.go", false},
		{"harness/*.json", "harness/vectors.json", true},
		{"harness/*.json", "harness/sub/vectors.json", false},
		{"*.md", "brief.md", true},
		{"*.md", "sub/brief.md", false},
		{"a/**/z.txt", "a/z.txt", true},
		{"a/**/z.txt", "a/b/c/z.txt", true},
		{"a/**/z.txt", "a/b/c/y.txt", false},
	}
	for _, tt := range tests {
		g, err := compileGlob(tt.pattern)
		if err != nil {
			t.Fatalf("compileGlob(%q): %v", tt.pattern, err)
		}
		if got := g.match(tt.name); got != tt.want {
			t.Errorf("match(%q, %q) = %v, want %v", tt.pattern, tt.name, got, tt.want)
		}
	}
}

func TestCompileGlobRejects(t *testing.T) {
	for _, pattern := range []string{
		"", "/abs/path", "a//b", "a/./b", "a/../b", "pre**", "**post/x", "bad[/x",
	} {
		if _, err := compileGlob(pattern); err == nil {
			t.Errorf("compileGlob(%q) succeeded, want error", pattern)
		}
	}
}

func TestClassifyFailClosed(t *testing.T) {
	c := mustClassifier(t, "candidate/**")
	if got := c.Classify("candidate/brief.md"); got != CandidateVisible {
		t.Errorf("candidate/brief.md = %v, want CandidateVisible", got)
	}
	for _, p := range []string{
		"problem.yaml",          // unclassified: default interviewer-only
		"faults/01/inject.sh",   // unclassified
		"interviewer/rubric.md", // interviewer dir
		"notes.md",              // unclassified top-level
	} {
		if got := c.Classify(p); got != InterviewerOnly {
			t.Errorf("Classify(%q) = %v, want InterviewerOnly", p, got)
		}
	}
}

func TestInterviewerAlwaysWins(t *testing.T) {
	// Even a glob that sweeps everything cannot expose interviewer/.
	c := mustClassifier(t, "**")
	if got := c.Classify("interviewer/answer-key.md"); got != InterviewerOnly {
		t.Errorf("interviewer/answer-key.md = %v, want InterviewerOnly", got)
	}
	if got := c.Classify("interviewer"); got != InterviewerOnly {
		t.Errorf("interviewer (as a file) = %v, want InterviewerOnly", got)
	}
	if got := c.Classify("anything/else.md"); got != CandidateVisible {
		t.Errorf("anything/else.md = %v, want CandidateVisible", got)
	}
}

func TestNewClassifierRejectsProtectedGlobs(t *testing.T) {
	for _, pattern := range []string{"interviewer/**", "interviewer", "faults/**", "faults", "faults/*/fix.sh"} {
		if _, err := NewClassifier([]string{pattern}); err == nil {
			t.Errorf("glob %q targeting a protected dir accepted, want error", pattern)
		}
	}
}

// Fault scripts are the answer key for a debugging scenario; like
// interviewer/, no glob may expose them.
func TestFaultsDirAlwaysProtected(t *testing.T) {
	c := mustClassifier(t, "**")
	for _, p := range []string{"faults/01-image-typo/fix.sh", "faults/01-image-typo/notes.md", "faults"} {
		if got := c.Classify(p); got != InterviewerOnly {
			t.Errorf("Classify(%q) = %v, want InterviewerOnly", p, got)
		}
	}
}

func TestScanPartitionsAndReportsUnmatchedGlobs(t *testing.T) {
	fsys := fstest.MapFS{
		"candidate/brief.md":    {Data: []byte("go")},
		"candidate/src/main.go": {Data: []byte("package main")},
		"interviewer/rubric.md": {Data: []byte("secret")},
		"problem.yaml":          {Data: []byte("schema: 1")},
	}
	c := mustClassifier(t, "candidate/**", "starter/**")
	s, err := c.Scan(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"candidate/brief.md", "candidate/src/main.go"}; !reflect.DeepEqual(s.Candidate, want) {
		t.Errorf("Candidate = %v, want %v", s.Candidate, want)
	}
	if want := []string{"interviewer/rubric.md", "problem.yaml"}; !reflect.DeepEqual(s.Interviewer, want) {
		t.Errorf("Interviewer = %v, want %v", s.Interviewer, want)
	}
	if want := []string{"starter/**"}; !reflect.DeepEqual(s.UnmatchedGlobs, want) {
		t.Errorf("UnmatchedGlobs = %v, want %v", s.UnmatchedGlobs, want)
	}
	if len(s.Irregular) != 0 {
		t.Errorf("Irregular = %v, want none", s.Irregular)
	}
}

// A symlink can sit at a candidate-visible path while pointing at an answer
// key, so classification by path alone must not vouch for it.
func TestScanTreatsSymlinksAsInterviewerOnly(t *testing.T) {
	dir := t.TempDir()
	for _, sub := range []string{"candidate", "interviewer"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "candidate/brief.md"), []byte("brief"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "interviewer/rubric.md"), []byte("answer key"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../interviewer/rubric.md", filepath.Join(dir, "candidate/notes.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	c := mustClassifier(t, "candidate/**")
	s, err := c.Scan(os.DirFS(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(s.Irregular, "candidate/notes.md") {
		t.Errorf("symlink not reported as irregular: %v", s.Irregular)
	}
	if slices.Contains(s.Candidate, "candidate/notes.md") {
		t.Error("symlink pointing into interviewer/ was classified candidate-visible")
	}
	if !slices.Contains(s.Interviewer, "candidate/notes.md") {
		t.Errorf("symlink missing from interviewer-only set: %v", s.Interviewer)
	}
	leaks, err := Leaks(os.DirFS(dir), c)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(leaks, "candidate/notes.md") {
		t.Errorf("Leaks did not flag the symlink: %v", leaks)
	}
}

func TestLeaks(t *testing.T) {
	fsys := fstest.MapFS{
		"candidate/brief.md":    {Data: []byte("go")},
		"candidate/src/main.go": {Data: []byte("package main")},
		"interviewer/rubric.md": {Data: []byte("secret")},
		"problem.yaml":          {Data: []byte("schema: 1")},
	}
	c := mustClassifier(t, "candidate/**")
	got, err := Leaks(fsys, c)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"interviewer/rubric.md", "problem.yaml"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Leaks = %v, want %v", got, want)
	}

	clean := fstest.MapFS{
		"candidate/brief.md": {Data: []byte("go")},
	}
	got, err = Leaks(clean, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("Leaks on clean tree = %v, want none", got)
	}
}

// A protected directory nested below the root is still protected. Organising
// notes one level down is plausible; publishing them that way is not.
func TestProtectedDirsAtAnyDepth(t *testing.T) {
	c := mustClassifier(t, "candidate/**", "**/*.md")
	for _, p := range []string{
		"candidate/notes/interviewer/reference.md",
		"candidate/notes/faults/01-x/inject.sh",
		"deep/nest/interviewer/probes.md",
		"candidate/faults/notes.md",
	} {
		if got := c.Classify(p); got != InterviewerOnly {
			t.Errorf("Classify(%q) = %v, want InterviewerOnly", p, got)
		}
	}
}

// The classifier and the filesystem have to agree about identity. On a
// case-insensitive filesystem Interviewer/ and interviewer/ are one directory.
func TestProtectedDirsFoldCase(t *testing.T) {
	c := mustClassifier(t, "**/*.md")
	for _, p := range []string{
		"Interviewer/probes.md", "INTERVIEWER/keys.md", "candidate/Faults/x.md",
	} {
		if got := c.Classify(p); got != InterviewerOnly {
			t.Errorf("Classify(%q) = %v, want InterviewerOnly", p, got)
		}
	}
	if _, err := NewClassifier([]string{"Interviewer/**"}); err == nil {
		t.Error("glob naming Interviewer/ accepted, want error")
	}
	if _, err := NewClassifier([]string{"**/interviewer/**"}); err == nil {
		t.Error("glob naming interviewer/ at depth accepted, want error")
	}
}

// The files that describe the exercise are not answer-free.
func TestRootSpecFilesAreProtected(t *testing.T) {
	c := mustClassifier(t, "**")
	for _, p := range []string{"env.yaml", "review.yaml", "problem.yaml"} {
		if got := c.Classify(p); got != InterviewerOnly {
			t.Errorf("Classify(%q) = %v, want InterviewerOnly", p, got)
		}
	}
	// The same name inside the candidate tree is ordinary content.
	if got := c.Classify("candidate/problem.yaml"); got != CandidateVisible {
		t.Errorf("candidate/problem.yaml = %v, want CandidateVisible", got)
	}
}

// A hardlink is a second name for bytes that live somewhere else, and the
// path gives no sign of it.
func TestScanRejectsHardlinks(t *testing.T) {
	dir := t.TempDir()
	for _, sub := range []string{"candidate", "interviewer"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	key := filepath.Join(dir, "interviewer/reference.md")
	if err := os.WriteFile(key, []byte("answer key"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "candidate/brief.md"), []byte("brief"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(key, filepath.Join(dir, "candidate/util.md")); err != nil {
		t.Skipf("hardlinks unavailable: %v", err)
	}

	c := mustClassifier(t, "candidate/**")
	s, err := c.Scan(os.DirFS(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(s.Irregular, "candidate/util.md") {
		t.Errorf("hardlink not reported irregular: %v", s.Irregular)
	}
	if slices.Contains(s.Candidate, "candidate/util.md") {
		t.Error("hardlink to an answer key was classified candidate-visible")
	}
}
