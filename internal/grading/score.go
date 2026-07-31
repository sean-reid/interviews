package grading

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Score is the objective record for a debugging session: which faults the
// checks report fixed, and whether the app verifies end to end. It informs
// the grade; it is not the grade.
type Score struct {
	Problem     string        `json:"problem"`
	InterviewID string        `json:"interview_id"`
	Pack        string        `json:"pack"`
	Faults      []FaultResult `json:"faults"`
	Fixed       int           `json:"fixed"`
	Total       int           `json:"total"`
	Verified    bool          `json:"verified"`
	At          time.Time     `json:"at"`
}

// FaultResult is one fault's check outcome.
type FaultResult struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Tier  string `json:"tier"`
	Fixed bool   `json:"fixed"`
}

// ScoreFile is the score's name inside a session workdir.
const ScoreFile = "score.json"

// HintsFile is the hints ledger's name inside a session workdir.
const HintsFile = "hints.json"

// Hint is one hint given during a session. Hints are evidence: they are
// logged when given, not reconstructed from memory afterwards.
type Hint struct {
	Minute int       `json:"minute"`
	Text   string    `json:"text"`
	At     time.Time `json:"at"`
}

// WriteScore saves the score into a session workdir.
func WriteScore(workdir string, s *Score) error {
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(workdir, ScoreFile), raw, 0o644)
}

// LoadScore reads a previously written score; nil without error when none
// has been written yet.
func LoadScore(workdir string) (*Score, error) {
	raw, err := os.ReadFile(filepath.Join(workdir, ScoreFile))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s Score
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", ScoreFile, err)
	}
	return &s, nil
}

// AppendHint adds one hint to the session's ledger.
func AppendHint(workdir string, h Hint) error {
	hints, err := LoadHints(workdir)
	if err != nil {
		return err
	}
	hints = append(hints, h)
	raw, err := json.MarshalIndent(hints, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(workdir, HintsFile), raw, 0o644)
}

// LoadHints reads the hints ledger; empty without error when none exists.
func LoadHints(workdir string) ([]Hint, error) {
	raw, err := os.ReadFile(filepath.Join(workdir, HintsFile))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var hints []Hint
	if err := json.Unmarshal(raw, &hints); err != nil {
		return nil, fmt.Errorf("%s: %w", HintsFile, err)
	}
	return hints, nil
}
