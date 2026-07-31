package takehome

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/sean-reid/interviews/internal/content"
	"github.com/sean-reid/interviews/internal/taxonomy"
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
visibility:
  candidate: [candidate/**]
`

// baseFiles is a full valid take-home problem; tests copy and mutate it.
func baseFiles() map[string]*fstest.MapFile {
	return map[string]*fstest.MapFile{
		"problem.yaml": {Data: []byte(testManifest)},
		"candidate/brief.md": {Data: []byte(
			"Optimize the dataset. Write STOPPING-POINT.md when you stop.\n")},
		"interviewer/probes.md": {Data: []byte("What did you profile first?")},
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

func TestValidate(t *testing.T) {
	if issues := Validate(loadProblem(t, baseFiles())); len(issues) != 0 {
		t.Errorf("valid problem flagged: %v", issues)
	}

	files := baseFiles()
	delete(files, "interviewer/probes.md")
	issues := Validate(loadProblem(t, files))
	if len(issues) != 1 || issues[0].Path != ProbesPath {
		t.Errorf("missing probes not flagged: %v", issues)
	}

	files = baseFiles()
	files["candidate/brief.md"] = &fstest.MapFile{Data: []byte("Just do the task.")}
	issues = Validate(loadProblem(t, files))
	if len(issues) != 1 || !strings.Contains(issues[0].Msg, "stopping-point") {
		t.Errorf("brief without stopping point not flagged: %v", issues)
	}

	// Either spelling of the writeup counts, in any case.
	files["candidate/brief.md"] = &fstest.MapFile{Data: []byte("Note your Stopping Point when done.")}
	if issues := Validate(loadProblem(t, files)); len(issues) != 0 {
		t.Errorf("stopping point spelling rejected: %v", issues)
	}
}
