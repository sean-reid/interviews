// Package sysdesign validates system design problems. A design exercise is
// offline work followed by a live review, so the package enforces that both
// halves ship together: the candidate gets a prompt and a constraint sheet,
// and the interviewer gets probes, scripted curveballs, and reference notes
// to calibrate against.
package sysdesign

import (
	"fmt"
	"io/fs"
	"strings"

	"github.com/sean-reid/interviews/internal/content"
	"github.com/sean-reid/interviews/internal/leak"
	"github.com/sean-reid/interviews/internal/taxonomy"
)

// Required paths in every system design problem.
const (
	// ConstraintsPath is candidate-visible: scale, budget, team, compliance,
	// and the deliverable spec.
	ConstraintsPath = "candidate/constraints.md"
	// ReviewManifest declares the live-review material.
	ReviewManifest = leak.ReviewFile
	// ProbesPath is the interviewer's question bank for the review.
	ProbesPath = content.ProbesPath
	// ReferencePath is one or more worked designs for calibration only.
	ReferencePath = "interviewer/reference.md"
)

// Review is the parsed review.yaml: what the live session does with the
// document the candidate submitted.
type Review struct {
	// Tensions are the contradictions planted in the constraints. Finding
	// them is part of the exercise, so each names where it hides and what a
	// good resolution looks like.
	Tensions []Tension `yaml:"tensions"`
	// Curveballs are requirement changes the interviewer introduces mid
	// review. A design that only survives its original assumptions fails
	// here, and a memorized or generated answer cannot adapt.
	Curveballs []Curveball `yaml:"curveballs"`
}

// Tension is one planted contradiction.
type Tension struct {
	ID       string `yaml:"id"`
	Summary  string `yaml:"summary"`
	HidesIn  string `yaml:"hides_in"`
	GoodMove string `yaml:"good_move"`
}

// Curveball is one scripted mid-review change.
type Curveball struct {
	ID     string `yaml:"id"`
	Prompt string `yaml:"prompt"`
	// Probes is what the change is testing.
	Probes string `yaml:"probes"`
	// GoodMove and RedFlag anchor the interviewer's read of the response.
	GoodMove string `yaml:"good_move"`
	RedFlag  string `yaml:"red_flag"`
}

// Load reads review.yaml and checks the problem carries both halves of the
// exercise. Like content.Load it returns every issue it finds.
func Load(p *content.Problem) (*Review, []content.Issue) {
	raw, err := fs.ReadFile(p.FS, ReviewManifest)
	if err != nil {
		return nil, []content.Issue{{Path: ReviewManifest,
			Msg: "system design problems need a review manifest with tensions and curveballs"}}
	}
	var r Review
	if err := content.DecodeStrict(raw, &r); err != nil {
		return nil, []content.Issue{{Path: ReviewManifest, Msg: fmt.Sprintf("cannot decode: %v", err)}}
	}

	var issues []content.Issue
	add := func(path, format string, args ...any) {
		issues = append(issues, content.Issuef(path, format, args...))
	}

	if len(r.Tensions) == 0 {
		add(ReviewManifest, "tensions: at least one planted contradiction is required")
	}
	seen := map[string]bool{}
	for i, t := range r.Tensions {
		where := fmt.Sprintf("tensions[%d]", i)
		if !taxonomy.ValidID(t.ID) {
			add(ReviewManifest, "%s.id: %q must be kebab-case", where, t.ID)
		}
		if seen[t.ID] {
			add(ReviewManifest, "%s.id: %q listed twice", where, t.ID)
		}
		seen[t.ID] = true
		if t.Summary == "" || t.HidesIn == "" || t.GoodMove == "" {
			add(ReviewManifest, "%s: summary, hides_in, and good_move are all required", where)
		}
	}

	if len(r.Curveballs) < 2 {
		add(ReviewManifest, "curveballs: at least two are required so a review can escalate")
	}
	seen = map[string]bool{}
	for i, c := range r.Curveballs {
		where := fmt.Sprintf("curveballs[%d]", i)
		if !taxonomy.ValidID(c.ID) {
			add(ReviewManifest, "%s.id: %q must be kebab-case", where, c.ID)
		}
		if seen[c.ID] {
			add(ReviewManifest, "%s.id: %q listed twice", where, c.ID)
		}
		seen[c.ID] = true
		if c.Prompt == "" || c.Probes == "" || c.GoodMove == "" || c.RedFlag == "" {
			add(ReviewManifest, "%s: prompt, probes, good_move, and red_flag are all required", where)
		}
	}

	issues = append(issues, requireCandidateFile(p, ConstraintsPath)...)
	for _, path := range []string{ProbesPath, ReferencePath} {
		if _, err := fs.Stat(p.FS, path); err != nil {
			add(path, "required: the live review is where a design exercise is scored")
		}
	}
	issues = append(issues, checkBriefMentionsDeliverable(p)...)
	return &r, issues
}

func requireCandidateFile(p *content.Problem, path string) []content.Issue {
	if _, err := fs.Stat(p.FS, path); err != nil {
		return []content.Issue{{Path: path, Msg: "required: the candidate needs the constraint sheet"}}
	}
	if p.Scan == nil {
		return nil
	}
	for _, f := range p.Scan.Candidate {
		if f == path {
			return nil
		}
	}
	return []content.Issue{{Path: path, Msg: "must be candidate-visible"}}
}

// checkBriefMentionsDeliverable keeps the brief honest about what to submit
// and when to stop: offline work with no stated shape produces artifacts
// nobody can review.
func checkBriefMentionsDeliverable(p *content.Problem) []content.Issue {
	raw, err := fs.ReadFile(p.FS, content.BriefPath)
	if err != nil {
		return nil // content validation already reports the missing brief
	}
	lower := strings.ToLower(string(raw))
	if !strings.Contains(lower, "constraints.md") {
		return []content.Issue{{Path: content.BriefPath,
			Msg: "must point the candidate at candidate/constraints.md"}}
	}
	return nil
}
