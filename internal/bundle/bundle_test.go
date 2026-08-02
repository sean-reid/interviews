package bundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/sean-reid/interviews/internal/content"
	"github.com/sean-reid/interviews/internal/taxonomy"
	"github.com/sean-reid/interviews/internal/variant"
)

const testManifest = `schema: 1
id: slow-aligner
type: takehome
title: Slow aligner
summary: S
disciplines: [systems]
levels: [senior]
class: optimization-ladder
time:
  soft_budget_hours: 6
params:
  dataset:
    type: choice
    of: [small, medium, large]
visibility:
  candidate: [candidate/**, harness/**]
`

// baseFiles is a full valid take-home problem; tests copy and mutate it.
func baseFiles() map[string]*fstest.MapFile {
	return map[string]*fstest.MapFile{
		"problem.yaml": {Data: []byte(testManifest)},
		"candidate/brief.md": {Data: []byte(
			"Optimize the {{.dataset}} dataset. Write STOPPING-POINT.md when you stop.\n")},
		"candidate/data.bin":      {Data: []byte{0x00, 0x01, 0xff, 0x7f, 0x02}},
		"harness/run.sh":          {Data: []byte("#!/bin/sh\necho {{.dataset}}\n"), Mode: 0o755},
		"interviewer/probes.md":   {Data: []byte("What did you profile first?")},
		"interviewer/solution.md": {Data: []byte("the answer key")},
	}
}

func loadProblem(t *testing.T, files map[string]*fstest.MapFile) *content.Problem {
	t.Helper()
	p, issues := content.Load(fstest.MapFS(files), taxonomy.TakeHome, "slow-aligner")
	if p == nil || content.Errors(issues) {
		t.Fatalf("test problem does not load cleanly: %v", issues)
	}
	return p
}

func resolve(t *testing.T, p *content.Problem) *variant.Resolved {
	t.Helper()
	v, err := variant.Resolve(p.Manifest.ID, p.Manifest.Params, "seed-1",
		map[string]string{"dataset": "medium"})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestBundleDir(t *testing.T) {
	requireGit(t)
	p := loadProblem(t, baseFiles())
	out := filepath.Join(t.TempDir(), "out")
	if err := Write(p, resolve(t, p), out); err != nil {
		t.Fatal(err)
	}

	brief, err := os.ReadFile(filepath.Join(out, "candidate", "brief.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(brief), "the medium dataset") || strings.Contains(string(brief), "{{") {
		t.Errorf("brief not rendered: %q", brief)
	}

	info, err := os.Stat(filepath.Join(out, "harness", "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Errorf("exec bit lost on run.sh: mode %v", info.Mode())
	}
	script, _ := os.ReadFile(filepath.Join(out, "harness", "run.sh"))
	if !strings.Contains(string(script), "echo medium") {
		t.Errorf("script not rendered: %q", script)
	}

	bin, err := os.ReadFile(filepath.Join(out, "candidate", "data.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bin, []byte{0x00, 0x01, 0xff, 0x7f, 0x02}) {
		t.Errorf("binary file altered: %v", bin)
	}

	about, err := os.ReadFile(filepath.Join(out, AboutName))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Slow aligner", "about 6 hours", "STOPPING-POINT.md",
		"candidate/brief.md", "harness/ directory", "zip"} {
		if !strings.Contains(string(about), want) {
			t.Errorf("ABOUT.md missing %q:\n%s", want, about)
		}
	}
	for _, banned := range []string{"variant", "seed", "pack", "grad", "interviews"} {
		if strings.Contains(strings.ToLower(string(about)), banned) {
			t.Errorf("ABOUT.md mentions platform internals %q:\n%s", banned, about)
		}
	}

	for _, gone := range []string{"interviewer", "problem.yaml"} {
		if _, err := os.Stat(filepath.Join(out, gone)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s reached the bundle", gone)
		}
	}

	if got := gitOut(t, out, "rev-list", "--count", "HEAD"); got != "1" {
		t.Errorf("commit count = %s, want 1", got)
	}
	log := gitOut(t, out, "log", "--format=%an|%ae|%cn|%ce|%s")
	if log != "candidate|candidate@localhost|candidate|candidate@localhost|initial drop" {
		t.Errorf("commit identity leaked: %q", log)
	}
	if got := gitOut(t, out, "status", "--porcelain"); got != "" {
		t.Errorf("bundle repo not clean: %q", got)
	}
}

func TestBundleWithoutHarnessOmitsNote(t *testing.T) {
	requireGit(t)
	files := baseFiles()
	delete(files, "harness/run.sh")
	p := loadProblem(t, files) // the unmatched harness glob is only a warning
	out := filepath.Join(t.TempDir(), "out")
	if err := Write(p, resolve(t, p), out); err != nil {
		t.Fatal(err)
	}
	about, err := os.ReadFile(filepath.Join(out, AboutName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(about), "harness") {
		t.Errorf("harness note present without a harness:\n%s", about)
	}
}

func TestBundleTarballRoundTrips(t *testing.T) {
	requireGit(t)
	p := loadProblem(t, baseFiles())
	out := filepath.Join(t.TempDir(), "drop.tar.gz")
	if err := Write(p, resolve(t, p), out); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	contents := map[string]string{}
	modes := map[string]fs.FileMode{}
	sawGit := false
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(hdr.Name, "slow-aligner/.git/") {
			sawGit = true
			continue
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		contents[hdr.Name] = string(data)
		modes[hdr.Name] = hdr.FileInfo().Mode()
	}
	if !sawGit {
		t.Error("tarball has no .git repository")
	}
	if got := contents["slow-aligner/candidate/brief.md"]; !strings.Contains(got, "medium") {
		t.Errorf("brief in tarball = %q", got)
	}
	if !strings.Contains(contents["slow-aligner/"+AboutName], "Slow aligner") {
		t.Error("ABOUT.md missing from tarball")
	}
	if modes["slow-aligner/harness/run.sh"]&0o111 == 0 {
		t.Errorf("exec bit lost in tarball: %v", modes["slow-aligner/harness/run.sh"])
	}
	for name := range contents {
		if strings.Contains(name, "interviewer") {
			t.Errorf("tarball contains %s", name)
		}
	}
}

// Debugging is delivered as a live session; there is nothing to hand over as
// files, so it must not reach the bundle writer at all.
func TestBundleRejectsDebugging(t *testing.T) {
	p := loadProblem(t, baseFiles())
	other := *p
	other.Manifest.Type = taxonomy.Debugging
	err := Write(&other, resolve(t, p), filepath.Join(t.TempDir(), "out"))
	if err == nil || !strings.Contains(err.Error(), "only take-home and system design") {
		t.Errorf("err = %v, want a type refusal", err)
	}
}

// bundleExpectingGate runs Write, requires it to fail mentioning wantErr,
// and requires the partial output to have been removed.
func bundleExpectingGate(t *testing.T, p *content.Problem, wantErr string) {
	t.Helper()
	requireGit(t)
	gateFor(t, p, resolve(t, p), wantErr)
}

func gateFor(t *testing.T, p *content.Problem, v *variant.Resolved, wantErr string) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "out")
	err := Write(p, v, out)
	if err == nil || !strings.Contains(err.Error(), wantErr) {
		t.Fatalf("err = %v, want mention of %q", err, wantErr)
	}
	if _, statErr := os.Stat(out); !errors.Is(statErr, fs.ErrNotExist) {
		t.Errorf("partial bundle left behind at %s", out)
	}
}

// A scan that upstream code (or a future refactor) lets sweep a protected
// file must still die at the gate: the gate re-classifies the output and
// trusts nothing that came before it.
func TestGateCatchesSweptProtectedFile(t *testing.T) {
	p := loadProblem(t, baseFiles())
	p.Scan.Candidate = append(p.Scan.Candidate, "interviewer/solution.md")
	bundleExpectingGate(t, p, "interviewer/solution.md")
}

// A rendered file can still contain literal braces when the template
// escapes them; only the output grep catches that.
func TestGateCatchesEscapedBracesInRenderedFile(t *testing.T) {
	files := baseFiles()
	files["candidate/table.csv"] = &fstest.MapFile{Data: []byte(`rows,{{"{{"}}count`)}
	bundleExpectingGate(t, loadProblem(t, files), "candidate/table.csv")
}

// A template hole in a file the renderer copies byte-for-byte survives
// rendering entirely; the grep is the only thing standing.
func TestGateCatchesTemplateHoleInCopiedFile(t *testing.T) {
	files := baseFiles()
	files["candidate/blob.dat"] = &fstest.MapFile{Data: []byte("payload {{.typo}} payload")}
	bundleExpectingGate(t, loadProblem(t, files), "candidate/blob.dat")
}

func TestGateCatchesInterviewerReference(t *testing.T) {
	files := baseFiles()
	files["candidate/notes.dat"] = &fstest.MapFile{Data: []byte("see interviewer/solution.md")}
	bundleExpectingGate(t, loadProblem(t, files), "candidate/notes.dat")
}

// An undeclared parameter in a rendered file fails the render itself, well
// before the gate, and still removes the output.
func TestRenderHoleFailsBundle(t *testing.T) {
	files := baseFiles()
	files["candidate/extra.md"] = &fstest.MapFile{Data: []byte("scale {{.typo}}")}
	bundleExpectingGate(t, loadProblem(t, files), "candidate/extra.md")
}

func TestBundleRefusesNonEmptyDir(t *testing.T) {
	p := loadProblem(t, baseFiles())
	out := t.TempDir()
	keep := filepath.Join(out, "keep.txt")
	if err := os.WriteFile(keep, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Write(p, resolve(t, p), out)
	if err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("err = %v, want a not-empty refusal", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("existing file disturbed: %v", err)
	}
}

func TestBundleAcceptsEmptyExistingDir(t *testing.T) {
	requireGit(t)
	p := loadProblem(t, baseFiles())
	out := t.TempDir()
	if err := Write(p, resolve(t, p), out); err != nil {
		t.Fatal(err)
	}
}

func TestBundleRefusesExistingTarball(t *testing.T) {
	p := loadProblem(t, baseFiles())
	out := filepath.Join(t.TempDir(), "drop.tar.gz")
	if err := os.WriteFile(out, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Write(p, resolve(t, p), out)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err = %v, want an already-exists refusal", err)
	}
}

// The candidate drop is a git repository, and git init copies the operator's
// template directory into .git. The gate does not look inside .git, so
// anything an interviewer keeps in their template rode out with the drop.
func TestBundleCarriesNothingFromTheOperatorsGitTemplate(t *testing.T) {
	requireGit(t)
	tmpl := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpl, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	const marker = "OPERATOR-TEMPLATE-MARKER"
	if err := os.WriteFile(filepath.Join(tmpl, "hooks", "pre-commit"),
		[]byte("#!/bin/sh\n# "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_TEMPLATE_DIR", tmpl)

	// Positive control: git really does honour this template, so a clean
	// bundle below means the fix worked and not that the plumbing is inert.
	control := filepath.Join(t.TempDir(), "control")
	if err := os.MkdirAll(control, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", control, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("control init: %v: %s", err, out)
	}
	if _, err := os.Stat(filepath.Join(control, ".git", "hooks", "pre-commit")); err != nil {
		t.Skipf("this git ignores GIT_TEMPLATE_DIR, so the assertion below proves nothing: %v", err)
	}

	p := loadProblem(t, baseFiles())
	out := filepath.Join(t.TempDir(), "out")
	if err := Write(p, resolve(t, p), out); err != nil {
		t.Fatal(err)
	}
	var found []string
	if err := filepath.WalkDir(out, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, rerr := os.ReadFile(path)
		if rerr == nil && strings.Contains(string(raw), marker) {
			found = append(found, path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(found) > 0 {
		t.Errorf("the operator's git template reached the candidate drop: %v", found)
	}
}
