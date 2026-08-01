// Package content defines the problem manifest and loads one problem
// directory into a validated Problem. Decoding is strict and the schema is
// versioned: an unknown field or future schema fails loudly instead of being
// silently ignored, so later stages extend the manifest without ambiguity.
package content

import (
	"bytes"
	"fmt"
	"io/fs"
	"slices"

	"gopkg.in/yaml.v3"

	"github.com/sean-reid/interviews/internal/leak"
	"github.com/sean-reid/interviews/internal/taxonomy"
)

// SupportedSchema is the manifest schema version this binary understands.
const SupportedSchema = 1

// ManifestName is the manifest file every problem directory must contain.
const ManifestName = "problem.yaml"

// BriefPath is the candidate brief every problem must ship: the exact text a
// candidate is given at the start, whatever the interview type.
const BriefPath = "candidate/brief.md"

// Manifest is the parsed problem.yaml.
type Manifest struct {
	Schema      int                   `yaml:"schema"`
	ID          string                `yaml:"id"`
	Type        taxonomy.Type         `yaml:"type"`
	Title       string                `yaml:"title"`
	Summary     string                `yaml:"summary"`
	Disciplines []taxonomy.Discipline `yaml:"disciplines"`
	Levels      []taxonomy.Level      `yaml:"levels"`
	Flavor      taxonomy.Flavor       `yaml:"flavor,omitempty"`
	Class       taxonomy.Class        `yaml:"class,omitempty"`
	Time        TimeSpec              `yaml:"time"`
	Params      map[string]ParamSpec  `yaml:"params,omitempty"`
	Visibility  Visibility            `yaml:"visibility"`
}

// TimeSpec is the time envelope: a session length for live types, a soft
// budget for offline ones. Exactly one is set, enforced by validation.
type TimeSpec struct {
	SessionMinutes  int     `yaml:"session_minutes,omitempty"`
	SoftBudgetHours float64 `yaml:"soft_budget_hours,omitempty"`
}

// ParamType is the kind of a variant parameter.
type ParamType string

const (
	// Choice picks one value from a list.
	Choice ParamType = "choice"
	// Int picks an integer from an inclusive range.
	Int ParamType = "int"
	// String is a fixed value, changeable only by explicit override.
	String ParamType = "string"
)

// ParamSpec declares one variant parameter. A parameter with a default is
// pinned to it; without one it resolves from the interview seed.
type ParamSpec struct {
	Type    ParamType `yaml:"type"`
	Of      []string  `yaml:"of,omitempty"`
	Min     *int      `yaml:"min,omitempty"`
	Max     *int      `yaml:"max,omitempty"`
	Default any       `yaml:"default,omitempty"`
	// Secret keeps the resolved value out of artifacts a human passes
	// around. The value still reaches the environment; a grading sheet gets
	// pasted into a hiring thread, and a credential does not belong there.
	Secret bool `yaml:"secret,omitempty"`
}

// Visibility declares what a candidate may see. Everything else in the
// problem directory is interviewer-only by default (see the leak package).
type Visibility struct {
	Candidate []string `yaml:"candidate"`
}

// Problem is one loaded, validated problem directory.
type Problem struct {
	Manifest   Manifest
	FS         fs.FS // rooted at the problem directory
	Classifier *leak.Classifier
	// Scan classifies every file in the problem, walked once at load time.
	Scan *leak.Scan
}

// Issue is one validation finding, tied to a file inside the problem.
type Issue struct {
	Path    string
	Msg     string
	Warning bool
}

func (i Issue) String() string {
	sev := "error"
	if i.Warning {
		sev = "warning"
	}
	return fmt.Sprintf("%s: %s: %s", sev, i.Path, i.Msg)
}

// Errors reports whether issues contains at least one non-warning.
func Errors(issues []Issue) bool {
	return slices.ContainsFunc(issues, func(i Issue) bool { return !i.Warning })
}

// Load reads and validates the problem rooted at fsys, which the caller
// found under the wantType directory named dirName. The returned issues
// contain every finding, not just the first; the Problem is nil only when
// the manifest cannot be decoded at all.
func Load(fsys fs.FS, wantType taxonomy.Type, dirName string) (*Problem, []Issue) {
	raw, err := fs.ReadFile(fsys, ManifestName)
	if err != nil {
		return nil, []Issue{{Path: ManifestName, Msg: "missing manifest"}}
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, []Issue{{Path: ManifestName, Msg: fmt.Sprintf("cannot decode: %v", err)}}
	}

	issues := validateManifest(&m, wantType, dirName)

	classifier, err := leak.NewClassifier(m.Visibility.Candidate)
	if err != nil {
		issues = append(issues, Issue{Path: ManifestName, Msg: fmt.Sprintf("visibility: %v", err)})
		classifier, _ = leak.NewClassifier(nil) // fail-closed fallback so callers can proceed
	}

	scan, err := classifier.Scan(fsys)
	if err != nil {
		issues = append(issues, Issue{Path: ".", Msg: fmt.Sprintf("walking problem files: %v", err)})
		scan = &leak.Scan{}
	}

	p := &Problem{Manifest: m, FS: fsys, Classifier: classifier, Scan: scan}
	issues = append(issues, validateFiles(p)...)
	return p, issues
}
