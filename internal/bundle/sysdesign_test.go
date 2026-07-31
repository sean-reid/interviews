package bundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/sean-reid/interviews/internal/content"
	"github.com/sean-reid/interviews/internal/taxonomy"
	"github.com/sean-reid/interviews/internal/variant"
)

const designManifest = `schema: 1
id: global-ledger
type: sysdesign
title: Global ledger
summary: S
disciplines: [systems]
levels: [staff]
time:
  soft_budget_hours: 5
params:
  company:
    type: string
    default: Northwind
  region_count:
    type: int
    min: 2
    max: 5
visibility:
  candidate: [candidate/**]
`

// designFiles is a full valid design problem: both halves of the exercise,
// with parameters in each candidate-facing file.
func designFiles() map[string]*fstest.MapFile {
	return map[string]*fstest.MapFile{
		"problem.yaml": {Data: []byte(designManifest)},
		"candidate/brief.md": {Data: []byte(
			"Design the {{.company}} ledger. Read candidate/constraints.md first.\n" +
				"Write STOPPING-POINT.md when you stop.\n")},
		"candidate/constraints.md": {Data: []byte(
			"You operate in {{.region_count}} regions.\n")},
		"candidate/topology.bin": {Data: []byte{0x89, 0x50, 0x4e, 0x47, 0x0d}},
		"review.yaml":            {Data: []byte("tensions: []\ncurveballs: []\n")},
		"interviewer/probes.md":  {Data: []byte("Where does the auditor read from?")},
		"interviewer/reference.md": {Data: []byte(
			"one worked design that passes")},
	}
}

func loadDesign(t *testing.T, files map[string]*fstest.MapFile) *content.Problem {
	t.Helper()
	p, issues := content.Load(fstest.MapFS(files), taxonomy.SysDesign, "global-ledger")
	if p == nil || content.Errors(issues) {
		t.Fatalf("test problem does not load cleanly: %v", issues)
	}
	return p
}

func resolveDesign(t *testing.T, p *content.Problem) *variant.Resolved {
	t.Helper()
	v, err := variant.Resolve(p.Manifest.ID, p.Manifest.Params, "seed-1",
		map[string]string{"region_count": "4"})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestDesignBundleDir(t *testing.T) {
	p := loadDesign(t, designFiles())
	out := filepath.Join(t.TempDir(), "out")
	if err := Write(p, resolveDesign(t, p), out); err != nil {
		t.Fatal(err)
	}

	brief, err := os.ReadFile(filepath.Join(out, "candidate", "brief.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(brief), "the Northwind ledger") || strings.Contains(string(brief), "{{") {
		t.Errorf("brief not rendered: %q", brief)
	}

	constraints, err := os.ReadFile(filepath.Join(out, "candidate", "constraints.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(constraints), "in 4 regions") {
		t.Errorf("constraint sheet not rendered: %q", constraints)
	}

	figure, err := os.ReadFile(filepath.Join(out, "candidate", "topology.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(figure, []byte{0x89, 0x50, 0x4e, 0x47, 0x0d}) {
		t.Errorf("binary file altered: %v", figure)
	}

	about, err := os.ReadFile(filepath.Join(out, AboutName))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Global ledger", "candidate/brief.md",
		"candidate/constraints.md", "design document", "about 5 hours",
		"STOPPING-POINT.md", "requirements to change"} {
		if !strings.Contains(string(about), want) {
			t.Errorf("ABOUT.md missing %q:\n%s", want, about)
		}
	}
	// A design candidate hands back a document, so nothing in the front page
	// may talk about repositories or a harness to run.
	for _, banned := range []string{"repositor", "harness", "variant", "seed", "pack", "grad", "interviews"} {
		if strings.Contains(strings.ToLower(string(about)), banned) {
			t.Errorf("ABOUT.md mentions %q:\n%s", banned, about)
		}
	}

	for _, gone := range []string{"interviewer", "review.yaml", "problem.yaml"} {
		if _, err := os.Stat(filepath.Join(out, gone)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s reached the bundle", gone)
		}
	}

	// The deliverable is a document, not a repository: an empty git history
	// would only invite the candidate to commit something nobody reads.
	if _, err := os.Stat(filepath.Join(out, ".git")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("design bundle has a git repository: %v", err)
	}
}

func TestDesignBundleTarball(t *testing.T) {
	p := loadDesign(t, designFiles())
	out := filepath.Join(t.TempDir(), "design.tar.gz")
	if err := Write(p, resolveDesign(t, p), out); err != nil {
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
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(hdr.Name, ".git/") {
			t.Errorf("tarball contains %s", hdr.Name)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		contents[hdr.Name] = string(data)
		if strings.Contains(hdr.Name, "interviewer") || strings.HasSuffix(hdr.Name, "review.yaml") {
			t.Errorf("tarball contains %s", hdr.Name)
		}
	}
	for _, want := range []string{
		"global-ledger/" + AboutName,
		"global-ledger/candidate/brief.md",
		"global-ledger/candidate/constraints.md",
	} {
		if _, ok := contents[want]; !ok {
			t.Errorf("tarball missing %s (has %v)", want, contents)
		}
	}
	if got := contents["global-ledger/candidate/constraints.md"]; !strings.Contains(got, "4 regions") {
		t.Errorf("constraint sheet in tarball = %q", got)
	}
}

// The gate is the same code for both types, but a design bundle takes the
// path that never runs git init, so prove it still refuses a leak there.
func TestDesignGateCatchesSweptInterviewerFile(t *testing.T) {
	p := loadDesign(t, designFiles())
	p.Scan.Candidate = append(p.Scan.Candidate, "interviewer/reference.md")
	gateFor(t, p, resolveDesign(t, p), "interviewer/reference.md")
}

func TestDesignGateCatchesTemplateHole(t *testing.T) {
	files := designFiles()
	files["candidate/appendix.dat"] = &fstest.MapFile{Data: []byte("scale {{.typo}}")}
	p := loadDesign(t, files)
	gateFor(t, p, resolveDesign(t, p), "candidate/appendix.dat")
}
