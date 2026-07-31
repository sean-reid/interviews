package sysdesign

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/sean-reid/interviews/internal/content"
	"github.com/sean-reid/interviews/internal/taxonomy"
)

const validReview = `tensions:
  - id: writable-old-primary
    summary: Two requirements cannot both hold.
    hides_in: Four bullets down the requirements list.
    good_move: Names it and picks a side.
curveballs:
  - id: budget-halved
    prompt: The budget is cut in half today.
    probes: Whether they know what their design costs.
    good_move: Cuts scope, not safety.
    red_flag: Drops verification to save money.
  - id: region-lost
    prompt: One region is gone for six hours.
    probes: Per-phase failure semantics.
    good_move: Answers per phase.
    red_flag: Invents a failover the design never had.
`

func problemFS(mutate func(fstest.MapFS)) fstest.MapFS {
	fsys := fstest.MapFS{
		"problem.yaml": &fstest.MapFile{Data: []byte(`schema: 1
id: ledger-migration
type: sysdesign
title: T
summary: S
disciplines: [systems]
levels: [senior]
time:
  soft_budget_hours: 5
visibility:
  candidate: [candidate/**]
`)},
		"candidate/brief.md": &fstest.MapFile{
			Data: []byte("Read candidate/constraints.md before you start."),
		},
		"candidate/constraints.md": &fstest.MapFile{Data: []byte("# Constraints")},
		"review.yaml":              &fstest.MapFile{Data: []byte(validReview)},
		"interviewer/probes.md":    &fstest.MapFile{Data: []byte("# Probes")},
		"interviewer/reference.md": &fstest.MapFile{Data: []byte("# Reference")},
	}
	if mutate != nil {
		mutate(fsys)
	}
	return fsys
}

func load(t *testing.T, mutate func(fstest.MapFS)) (*Review, []content.Issue) {
	t.Helper()
	p, issues := content.Load(problemFS(mutate), taxonomy.SysDesign, "ledger-migration")
	if content.Errors(issues) {
		t.Fatalf("fixture invalid: %v", issues)
	}
	return Load(p)
}

func wantIssue(t *testing.T, mutate func(fstest.MapFS), substr string) {
	t.Helper()
	_, issues := load(t, mutate)
	for _, i := range issues {
		if strings.Contains(i.Msg, substr) {
			return
		}
	}
	t.Errorf("no issue containing %q in %v", substr, issues)
}

func TestValidProblemLoads(t *testing.T) {
	r, issues := load(t, nil)
	if len(issues) != 0 {
		t.Fatalf("issues on valid problem: %v", issues)
	}
	if len(r.Tensions) != 1 || r.Tensions[0].ID != "writable-old-primary" {
		t.Errorf("tensions = %+v", r.Tensions)
	}
	if len(r.Curveballs) != 2 || r.Curveballs[1].RedFlag == "" {
		t.Errorf("curveballs = %+v", r.Curveballs)
	}
}

func TestMissingReviewManifest(t *testing.T) {
	r, issues := load(t, func(m fstest.MapFS) { delete(m, "review.yaml") })
	if r != nil || len(issues) == 0 {
		t.Error("missing review.yaml must return nil review with an issue")
	}
}

func TestReviewRules(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(fstest.MapFS)
		want   string
	}{
		{"unknown field", func(m fstest.MapFS) {
			m["review.yaml"] = &fstest.MapFile{Data: []byte(validReview + "bogus: true\n")}
		}, "cannot decode"},
		{"no tensions", func(m fstest.MapFS) {
			cut := strings.Index(validReview, "curveballs:")
			m["review.yaml"] = &fstest.MapFile{Data: []byte(validReview[cut:])}
		}, "at least one planted contradiction"},
		{"one curveball", func(m fstest.MapFS) {
			cut := strings.Index(validReview, "  - id: region-lost")
			m["review.yaml"] = &fstest.MapFile{Data: []byte(validReview[:cut])}
		}, "at least two are required"},
		{"bad tension id", func(m fstest.MapFS) {
			m["review.yaml"] = &fstest.MapFile{
				Data: []byte(strings.Replace(validReview, "id: writable-old-primary", "id: Writable_Old", 1)),
			}
		}, "kebab-case"},
		{"duplicate curveball id", func(m fstest.MapFS) {
			m["review.yaml"] = &fstest.MapFile{
				Data: []byte(strings.Replace(validReview, "id: region-lost", "id: budget-halved", 1)),
			}
		}, "listed twice"},
		{"incomplete tension", func(m fstest.MapFS) {
			m["review.yaml"] = &fstest.MapFile{
				Data: []byte(strings.Replace(validReview, "    good_move: Names it and picks a side.\n", "", 1)),
			}
		}, "good_move"},
		{"incomplete curveball", func(m fstest.MapFS) {
			m["review.yaml"] = &fstest.MapFile{
				Data: []byte(strings.Replace(validReview, "    red_flag: Drops verification to save money.\n", "", 1)),
			}
		}, "red_flag"},
		{"missing constraints", func(m fstest.MapFS) {
			delete(m, "candidate/constraints.md")
		}, "constraint sheet"},
		{"missing probes", func(m fstest.MapFS) {
			delete(m, "interviewer/probes.md")
		}, "live review is where a design exercise is scored"},
		{"missing reference", func(m fstest.MapFS) {
			delete(m, "interviewer/reference.md")
		}, "live review is where a design exercise is scored"},
		{"brief ignores constraints", func(m fstest.MapFS) {
			m["candidate/brief.md"] = &fstest.MapFile{Data: []byte("Design something nice.")}
		}, "must point the candidate at"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantIssue(t, tt.mutate, tt.want)
		})
	}
}

// The constraint sheet is useless if the visibility globs do not expose it.
func TestConstraintsMustBeCandidateVisible(t *testing.T) {
	wantIssue(t, func(m fstest.MapFS) {
		m["problem.yaml"] = &fstest.MapFile{
			Data: []byte(strings.Replace(string(m["problem.yaml"].Data),
				"candidate: [candidate/**]", "candidate: [candidate/brief.md]", 1)),
		}
	}, "must be candidate-visible")
}
