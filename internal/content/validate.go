package content

import (
	"fmt"
	"maps"
	"regexp"
	"slices"

	"github.com/sean-reid/interviews/internal/leak"
	"github.com/sean-reid/interviews/internal/taxonomy"
)

var (
	paramRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

func manifestIssue(format string, args ...any) Issue {
	return Issuef(ManifestName, format, args...)
}

func validateManifest(m *Manifest, wantType taxonomy.Type, dirName string) []Issue {
	var issues []Issue
	add := func(format string, args ...any) {
		issues = append(issues, manifestIssue(format, args...))
	}

	if m.Schema != SupportedSchema {
		add("schema: %d is not supported (this binary understands %d)", m.Schema, SupportedSchema)
	}
	if !taxonomy.ValidID(m.ID) {
		add("id: %q must be kebab-case", m.ID)
	} else if m.ID != dirName {
		add("id: %q must equal the directory name %q", m.ID, dirName)
	}
	if !taxonomy.ValidType(m.Type) {
		add("type: %q is not an interview type", m.Type)
	} else if m.Type != wantType {
		add("type: %q but the problem lives under %s/", m.Type, wantType)
	}
	if m.Title == "" {
		add("title: required")
	}
	if m.Summary == "" {
		add("summary: required")
	}

	issues = append(issues, validateSet("disciplines", m.Disciplines, taxonomy.ValidDiscipline)...)
	issues = append(issues, validateSet("levels", m.Levels, taxonomy.ValidLevel)...)

	switch {
	case m.Type == taxonomy.Debugging && m.Flavor == "":
		add("flavor: required for debugging problems")
	case m.Type == taxonomy.Debugging && !taxonomy.ValidFlavor(m.Flavor):
		add("flavor: %q is not a debugging flavor", m.Flavor)
	case m.Type != taxonomy.Debugging && m.Flavor != "":
		add("flavor: only debugging problems have one")
	}
	switch {
	case m.Type == taxonomy.TakeHome && m.Class == "":
		add("class: required for take-home problems")
	case m.Type == taxonomy.TakeHome && !taxonomy.ValidClass(m.Class):
		add("class: %q is not a take-home class", m.Class)
	case m.Type != taxonomy.TakeHome && m.Class != "":
		add("class: only take-home problems have one")
	}

	if m.Type == taxonomy.Debugging {
		if m.Time.SessionMinutes <= 0 {
			add("time: debugging needs session_minutes > 0")
		}
		if m.Time.SoftBudgetHours != 0 {
			add("time: debugging uses session_minutes, not soft_budget_hours")
		}
	} else if taxonomy.ValidType(m.Type) {
		if m.Time.SoftBudgetHours <= 0 {
			add("time: %s needs soft_budget_hours > 0", m.Type)
		}
		if m.Time.SessionMinutes != 0 {
			add("time: %s uses soft_budget_hours, not session_minutes", m.Type)
		}
	}

	for _, name := range slices.Sorted(maps.Keys(m.Params)) {
		issues = append(issues, validateParam(name, m.Params[name])...)
	}
	return issues
}

func validateSet[T ~string](field string, values []T, valid func(T) bool) []Issue {
	var issues []Issue
	if len(values) == 0 {
		return []Issue{manifestIssue("%s: at least one required", field)}
	}
	seen := map[T]bool{}
	for _, v := range values {
		if !valid(v) {
			issues = append(issues, manifestIssue("%s: %q is not a known value", field, v))
		}
		if seen[v] {
			issues = append(issues, manifestIssue("%s: %q listed twice", field, v))
		}
		seen[v] = true
	}
	return issues
}

func validateParam(name string, p ParamSpec) []Issue {
	var issues []Issue
	add := func(format string, args ...any) {
		issues = append(issues, manifestIssue("params.%s: "+format, append([]any{name}, args...)...))
	}
	if !paramRe.MatchString(name) {
		add("name must match %s", paramRe)
	}
	switch p.Type {
	case Choice:
		if p.Min != nil || p.Max != nil {
			add("choice takes of, not min/max")
		}
		if len(p.Of) == 0 {
			add("choice needs a non-empty of list")
		}
		seen := map[string]bool{}
		for _, v := range p.Of {
			if seen[v] {
				add("of value %q listed twice", v)
			}
			seen[v] = true
		}
		if p.Default != nil {
			s, ok := p.Default.(string)
			if !ok {
				add("default must be a string")
			} else if len(p.Of) > 0 && !seen[s] {
				add("default %q is not in of", s)
			}
		}
	case Int:
		if p.Of != nil {
			add("int takes min/max, not of")
		}
		if p.Min == nil || p.Max == nil {
			add("int needs both min and max")
		} else if *p.Min > *p.Max {
			add("min %d > max %d", *p.Min, *p.Max)
		}
		if p.Default != nil {
			d, ok := p.Default.(int)
			if !ok {
				add("default must be an integer")
			} else if p.Min != nil && p.Max != nil && (d < *p.Min || d > *p.Max) {
				add("default %d outside [%d, %d]", d, *p.Min, *p.Max)
			}
		}
	case String:
		if p.Of != nil || p.Min != nil || p.Max != nil {
			add("string takes only a default")
		}
		if p.Default == nil {
			add("string needs a default (it only changes by override)")
		} else if _, ok := p.Default.(string); !ok {
			add("default must be a string")
		}
	default:
		add("type %q is not choice, int, or string", p.Type)
	}
	return issues
}

// validateFiles checks the rules that need the problem's file tree. It reuses
// the single classification scan, so nothing here re-walks or re-matches.
func validateFiles(p *Problem) []Issue {
	var issues []Issue

	// The scan carries the names the walk actually saw. fs.Stat answers
	// case-insensitively on APFS, so a capitalised Brief.md used to pass it
	// on a Mac, match no candidate glob, and bundle a take-home whose front
	// page tells the candidate to start with a brief that is not in it.
	if !slices.Contains(p.Scan.Candidate, BriefPath) && !slices.Contains(p.Scan.Interviewer, BriefPath) {
		issues = append(issues, Issue{Path: BriefPath, Msg: "candidate brief is required for every problem"})
	} else if p.Classifier.Classify(BriefPath) != leak.CandidateVisible {
		issues = append(issues, Issue{Path: BriefPath, Msg: "candidate brief must be candidate-visible"})
	}

	for _, path := range p.Scan.Irregular {
		issues = append(issues, Issue{Path: path,
			Msg: "not a regular file; a link can name a candidate path and point at an answer key"})
	}
	for _, g := range p.Scan.UnmatchedGlobs {
		issues = append(issues, Issue{Path: ManifestName, Warning: true,
			Msg: fmt.Sprintf("visibility: candidate glob %q matches no files", g)})
	}
	return issues
}
