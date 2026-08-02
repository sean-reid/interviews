// Package grading holds the resourcefulness-first rubric and renders
// grading sheets. The rubric is data: dimensions with anchored 1-4 scales,
// per-level calibration bands, and per-type evidence prompts. Objective
// completion is recorded in the sheet but is secondary signal by design.
package grading

import (
	_ "embed"
	"fmt"
	"slices"

	"github.com/sean-reid/interviews/internal/content"
	"github.com/sean-reid/interviews/internal/taxonomy"
)

//go:embed default-rubric.yaml
var defaultRubric []byte

// SupportedVersion is the rubric schema version this binary understands.
const SupportedVersion = 1

// Rubric is the parsed rubric definition.
type Rubric struct {
	Version    int                        `yaml:"version"`
	Dimensions []Dimension                `yaml:"dimensions"`
	Levels     map[taxonomy.Level]string  `yaml:"levels"`
	Types      map[taxonomy.Type][]string `yaml:"types"`
}

// Dimension is one scored axis with anchored levels 1-4.
type Dimension struct {
	Key      string         `yaml:"key"`
	Title    string         `yaml:"title"`
	Question string         `yaml:"question"`
	Anchors  map[int]string `yaml:"anchors"`
}

// Default returns the rubric embedded in the binary.
func Default() (*Rubric, error) {
	return Parse(defaultRubric)
}

// Parse decodes and validates a rubric.
func Parse(raw []byte) (*Rubric, error) {
	var r Rubric
	if err := content.DecodeStrict(raw, &r); err != nil {
		return nil, fmt.Errorf("cannot decode rubric: %w", err)
	}
	if err := r.validate(); err != nil {
		return nil, err
	}
	return &r, nil
}

func (r *Rubric) validate() error {
	if r.Version != SupportedVersion {
		return fmt.Errorf("rubric version %d is not supported (this binary understands %d)", r.Version, SupportedVersion)
	}
	if len(r.Dimensions) == 0 {
		return fmt.Errorf("rubric has no dimensions")
	}
	seen := map[string]bool{}
	for _, d := range r.Dimensions {
		if d.Key == "" || d.Title == "" {
			return fmt.Errorf("dimension %q: key and title are required", d.Key)
		}
		if seen[d.Key] {
			return fmt.Errorf("dimension %q defined twice", d.Key)
		}
		seen[d.Key] = true
		for score := 1; score <= 4; score++ {
			if d.Anchors[score] == "" {
				return fmt.Errorf("dimension %q: anchor %d is missing", d.Key, score)
			}
		}
	}
	for _, level := range taxonomy.Levels {
		if r.Levels[level] == "" {
			return fmt.Errorf("levels: no calibration band for %q", level)
		}
	}
	for typ := range r.Types {
		if !taxonomy.ValidType(typ) {
			return fmt.Errorf("types: %q is not an interview type", typ)
		}
	}
	for _, typ := range taxonomy.Types {
		if len(r.Types[typ]) == 0 {
			return fmt.Errorf("types: no evidence prompts for %q", typ)
		}
	}
	if !slices.ContainsFunc(r.Dimensions, func(d Dimension) bool { return d.Key == "wrangling" }) {
		return fmt.Errorf("rubric must keep a tool-and-AI wrangling dimension; it is the platform's core signal")
	}
	return nil
}
