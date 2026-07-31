package registry

import (
	"fmt"
	"strings"
	"testing"
	"testing/fstest"
)

func manifest(id, typ, extra string) string {
	timeSpec := "soft_budget_hours: 6"
	if typ == "debugging" {
		timeSpec = "session_minutes: 60"
	}
	return fmt.Sprintf(`schema: 1
id: %s
type: %s
title: T
summary: S
disciplines: [systems]
levels: [mid]
%s
time:
  %s
visibility:
  candidate: [candidate/**]
`, id, typ, extra, timeSpec)
}

func problemFiles(fsys fstest.MapFS, dir, id, typ, extra string) {
	fsys[dir+"/problem.yaml"] = &fstest.MapFile{Data: []byte(manifest(id, typ, extra))}
	fsys[dir+"/candidate/brief.md"] = &fstest.MapFile{Data: []byte("brief")}
	fsys[dir+"/interviewer/notes.md"] = &fstest.MapFile{Data: []byte("key")}
}

func validRoot() fstest.MapFS {
	fsys := fstest.MapFS{}
	problemFiles(fsys, "debugging/pipeline-meltdown", "pipeline-meltdown", "debugging", "flavor: kubernetes")
	problemFiles(fsys, "takehome/slow-aligner", "slow-aligner", "takehome", "class: optimization-ladder")
	problemFiles(fsys, "sysdesign/global-feed", "global-feed", "sysdesign", "")
	return fsys
}

func TestLoadValidRoot(t *testing.T) {
	r, err := Load(validRoot())
	if err != nil {
		t.Fatal(err)
	}
	if Errors(r.Findings()) {
		t.Fatalf("findings on valid root: %v", r.Findings())
	}
	if got := len(r.Problems()); got != 3 {
		t.Fatalf("problems = %d, want 3", got)
	}
	dirs := []string{}
	for _, e := range r.Problems() {
		dirs = append(dirs, e.Dir)
	}
	want := "debugging/pipeline-meltdown, sysdesign/global-feed, takehome/slow-aligner"
	if got := strings.Join(dirs, ", "); got != want {
		t.Errorf("sorted dirs = %s, want %s", got, want)
	}
	e, ok := r.Get("slow-aligner")
	if !ok || e.Problem.Manifest.Title != "T" {
		t.Errorf("Get(slow-aligner) = %+v, %v", e, ok)
	}
	if _, ok := r.Get("nope"); ok {
		t.Error("Get(nope) found something")
	}
}

func TestEmptyAndPartialRoots(t *testing.T) {
	r, err := Load(fstest.MapFS{".keep": &fstest.MapFile{}})
	if err != nil {
		t.Fatal(err)
	}
	// The stray root file is flagged; no problems is fine.
	if len(r.Problems()) != 0 {
		t.Errorf("problems in empty root: %v", r.Problems())
	}
	if !Errors(r.Findings()) {
		t.Error("stray root file not flagged")
	}

	fsys := fstest.MapFS{}
	problemFiles(fsys, "debugging/only-one", "only-one", "debugging", "flavor: kubernetes")
	r, err = Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if Errors(r.Findings()) || len(r.Problems()) != 1 {
		t.Errorf("single-type root: findings %v, problems %d", r.Findings(), len(r.Problems()))
	}
}

func TestDuplicateIDAcrossTypes(t *testing.T) {
	fsys := validRoot()
	problemFiles(fsys, "takehome/pipeline-meltdown", "pipeline-meltdown", "takehome", "class: legacy-rescue")
	r, err := Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range r.Findings() {
		if strings.Contains(f.Msg, "already used by debugging/pipeline-meltdown") {
			found = true
		}
	}
	if !found {
		t.Errorf("duplicate id not flagged: %v", r.Findings())
	}
	// The first loaded entry keeps the id.
	e, _ := r.Get("pipeline-meltdown")
	if e.Dir != "debugging/pipeline-meltdown" {
		t.Errorf("Get returned %s", e.Dir)
	}
}

func TestStrayFileInTypeDir(t *testing.T) {
	fsys := validRoot()
	fsys["debugging/README.md"] = &fstest.MapFile{Data: []byte("stray")}
	r, err := Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if !Errors(r.Findings()) {
		t.Error("stray file in type dir not flagged")
	}
}

// The manifest declares parameters and candidate text consumes them; the two
// halves must be checked together, or a typo only surfaces when a bundle is
// rendered for a real candidate.
func TestTemplateReferencesAreCheckedAgainstDeclaredParams(t *testing.T) {
	withParams := `params:
  scale:
    type: int
    min: 1
    max: 5
flavor: kubernetes`

	fsys := fstest.MapFS{}
	problemFiles(fsys, "debugging/typo-brief", "typo-brief", "debugging", withParams)
	fsys["debugging/typo-brief/candidate/brief.md"] = &fstest.MapFile{
		Data: []byte("Runs at scale {{.scal}}."),
	}
	r, err := Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range r.Findings() {
		if f.Path == "candidate/brief.md" && strings.Contains(f.Msg, "scal") {
			found = true
		}
	}
	if !found {
		t.Errorf("undeclared template reference not reported: %v", r.Findings())
	}

	fsys = fstest.MapFS{}
	problemFiles(fsys, "debugging/good-brief", "good-brief", "debugging", withParams)
	fsys["debugging/good-brief/candidate/brief.md"] = &fstest.MapFile{
		Data: []byte("Runs at scale {{.scale}}."),
	}
	r, err = Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if Errors(r.Findings()) {
		t.Errorf("correct template reference flagged: %v", r.Findings())
	}
}

// Interviewer-only text is never rendered, so it may hold anything.
func TestInterviewerTextIsNotTemplateChecked(t *testing.T) {
	fsys := fstest.MapFS{}
	problemFiles(fsys, "debugging/keys", "keys", "debugging", "flavor: kubernetes")
	fsys["debugging/keys/interviewer/notes.md"] = &fstest.MapFile{
		Data: []byte("Answer key mentions {{.whatever_it_likes}} and {{unparseable"),
	}
	r, err := Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if Errors(r.Findings()) {
		t.Errorf("interviewer-only text was template-checked: %v", r.Findings())
	}
}

func TestBrokenProblemFindingsCarryDir(t *testing.T) {
	fsys := validRoot()
	fsys["debugging/broken-one/problem.yaml"] = &fstest.MapFile{Data: []byte("schema: 1\nnot_a_field: true\n")}
	r, err := Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range r.Findings() {
		if f.Dir == "debugging/broken-one" && !f.Warning {
			found = true
		}
	}
	if !found {
		t.Errorf("broken problem findings missing dir context: %v", r.Findings())
	}
}
