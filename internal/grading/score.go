package grading

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/sean-reid/interviews/internal/fileio"
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
	// CheckFailed marks a fault whose check script could not run. Fixed is
	// false there because there is no reading at all, not because the fault
	// is still present, and a sheet must not claim otherwise.
	CheckFailed bool `json:"check_failed,omitempty"`
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
	return fileio.WriteAtomic(filepath.Join(workdir, ScoreFile), raw, 0o644)
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

// hint lock tuning. The critical section is one small read and one write,
// so a writer that waits this long is waiting on something stuck.
const (
	hintLockWait = 5 * time.Second
	hintLockPoll = 10 * time.Millisecond
)

// lockHints takes the hint ledger's lockfile. Appending is a
// read-modify-write and the writers are separate processes (the CLI, the
// session timers), so a mutex would not close it.
func lockHints(workdir string) (func(), error) {
	path := filepath.Join(workdir, HintsFile+".lock")
	deadline := time.Now().Add(hintLockWait)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			return func() {
				_ = f.Close()
				_ = os.Remove(path)
			}, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%s held for over %s; delete it if no interviews command is running",
				path, hintLockWait)
		}
		time.Sleep(hintLockPoll)
	}
}

// AppendHint adds one hint to the session's ledger. Hints are evidence, so
// two logged at once must not cost one of them.
func AppendHint(workdir string, h Hint) error {
	unlock, err := lockHints(workdir)
	if err != nil {
		return err
	}
	defer unlock()
	hints, err := LoadHints(workdir)
	if err != nil {
		return err
	}
	hints = append(hints, h)
	raw, err := json.MarshalIndent(hints, "", "  ")
	if err != nil {
		return err
	}
	return fileio.WriteAtomic(filepath.Join(workdir, HintsFile), raw, 0o644)
}

// LoadHints reads a session workdir's hints ledger; empty without error when
// none exists.
func LoadHints(workdir string) ([]Hint, error) {
	return LoadHintsFrom(filepath.Join(workdir, HintsFile))
}

// LoadHintsFrom reads a ledger named directly, or the ledger inside a
// directory. An interviewer logs hints where they are sitting, which is not
// where the rest of the session's evidence lands, so grading has to be able
// to name the ledger on its own.
func LoadHintsFrom(path string) ([]Hint, error) {
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		path = filepath.Join(path, HintsFile)
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var hints []Hint
	if err := json.Unmarshal(raw, &hints); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return hints, nil
}

// MergeHints combines ledgers into session order, dropping hints that appear
// in more than one so naming the same ledger twice cannot double it up.
func MergeHints(sets ...[]Hint) []Hint {
	var out []Hint
	seen := map[Hint]bool{}
	for _, set := range sets {
		for _, h := range set {
			if !seen[h] {
				seen[h] = true
				out = append(out, h)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Minute < out[j].Minute })
	return out
}
