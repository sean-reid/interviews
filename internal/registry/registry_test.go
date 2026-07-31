package registry

import (
	"fmt"
	"strings"
	"testing"
	"testing/fstest"
)

func manifest(id, typ, extra string) string {
	timeSpec := "soft_budget_hours: 6"
	params := ""
	if typ == "debugging" {
		timeSpec = "session_minutes: 60"
		params = `params:
  fault_pack:
    type: choice
    of: [pack-a]
`
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
%svisibility:
  candidate: [candidate/**]
`, id, typ, extra, timeSpec, params)
}

// problemFiles writes a minimal valid problem; debugging problems also get
// the environment spec and one fault the scenario rules demand.
func problemFiles(fsys fstest.MapFS, dir, id, typ, extra string) {
	fsys[dir+"/problem.yaml"] = &fstest.MapFile{Data: []byte(manifest(id, typ, extra))}
	fsys[dir+"/candidate/brief.md"] = &fstest.MapFile{Data: []byte("brief")}
	fsys[dir+"/interviewer/notes.md"] = &fstest.MapFile{Data: []byte("key")}
	if typ == "sysdesign" {
		fsys[dir+"/candidate/brief.md"] = &fstest.MapFile{Data: []byte("See candidate/constraints.md")}
		fsys[dir+"/candidate/constraints.md"] = &fstest.MapFile{Data: []byte("# Constraints")}
		fsys[dir+"/interviewer/probes.md"] = &fstest.MapFile{Data: []byte("# Probes")}
		fsys[dir+"/interviewer/reference.md"] = &fstest.MapFile{Data: []byte("# Reference")}
		fsys[dir+"/review.yaml"] = &fstest.MapFile{Data: []byte(`tensions:
  - id: only-tension
    summary: S
    hides_in: H
    good_move: G
curveballs:
  - id: first-ball
    prompt: P
    probes: R
    good_move: G
    red_flag: F
  - id: second-ball
    prompt: P
    probes: R
    good_move: G
    red_flag: F
`)}
		return
	}
	if typ != "debugging" {
		return
	}
	script := &fstest.MapFile{Data: []byte("#!/bin/sh\n"), Mode: 0o755}
	env := `provider: kind
kind:
  manifests: env/manifests
  namespace: app
verify: env/verify.sh
`
	if strings.Contains(extra, "compose-linux") {
		env = `provider: compose
compose:
  file: env/docker-compose.yml
verify: env/verify.sh
`
		fsys[dir+"/env/docker-compose.yml"] = &fstest.MapFile{Data: []byte("services: {}")}
	} else {
		fsys[dir+"/env/manifests/00-ns.yaml"] = &fstest.MapFile{Data: []byte("kind: Namespace")}
	}
	fsys[dir+"/env.yaml"] = &fstest.MapFile{Data: []byte(env)}
	fsys[dir+"/env/verify.sh"] = script
	fsys[dir+"/faults/01-only/fault.yaml"] = &fstest.MapFile{Data: []byte("id: 01-only\ntitle: T\ntier: easy\npacks: [pack-a]\n")}
	fsys[dir+"/faults/01-only/inject.sh"] = script
	fsys[dir+"/faults/01-only/check.sh"] = script
	fsys[dir+"/faults/01-only/fix.sh"] = script
	fsys[dir+"/faults/01-only/notes.md"] = &fstest.MapFile{Data: []byte("notes")}
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
	// Extend the generated params block with a scale parameter.
	addScale := func(fsys fstest.MapFS, dir string) {
		raw := string(fsys[dir+"/problem.yaml"].Data)
		raw = strings.Replace(raw, "    of: [pack-a]\n",
			"    of: [pack-a]\n  scale:\n    type: int\n    min: 1\n    max: 5\n", 1)
		fsys[dir+"/problem.yaml"] = &fstest.MapFile{Data: []byte(raw)}
	}

	fsys := fstest.MapFS{}
	problemFiles(fsys, "debugging/typo-brief", "typo-brief", "debugging", "flavor: kubernetes")
	addScale(fsys, "debugging/typo-brief")
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
	problemFiles(fsys, "debugging/good-brief", "good-brief", "debugging", "flavor: kubernetes")
	addScale(fsys, "debugging/good-brief")
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
