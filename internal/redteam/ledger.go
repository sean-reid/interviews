package redteam

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// LedgerPath is where calibration history lives, relative to the repo root.
// It is committed: the record of how easily each problem falls is the point,
// and it only means something as a series.
const LedgerPath = "calibration/ledger.jsonl"

// Verdict is a calibration reading for one problem.
type Verdict string

const (
	// Holds means an unassisted frontier agent did not get far enough to
	// make the problem trivial.
	Holds Verdict = "holds"
	// TooEasy means the problem no longer discriminates: rework it.
	TooEasy Verdict = "too-easy"
	// Inconclusive means the run failed for reasons unrelated to difficulty.
	Inconclusive Verdict = "inconclusive"
)

// Entry is one calibration attempt, appended to the ledger.
type Entry struct {
	Problem string `json:"problem"`
	Type    string `json:"type"`
	Seed    string `json:"seed"`
	// Pack is the fault pack the run faced; calibration runs one pack at a
	// time and every pack has its own difficulty.
	Pack   string    `json:"pack,omitempty"`
	Driver string    `json:"driver"`
	Model  string    `json:"model,omitempty"`
	At     time.Time `json:"at"`
	Budget string    `json:"budget"`
	Turns  int       `json:"turns,omitempty"`
	// Fixed and Total describe debugging outcomes; Notes carries whatever
	// the type-specific scorer measured.
	Fixed    int     `json:"fixed,omitempty"`
	Total    int     `json:"total,omitempty"`
	Verified bool    `json:"verified,omitempty"`
	Notes    string  `json:"notes,omitempty"`
	Verdict  Verdict `json:"verdict"`
}

// Share is the fraction of the pack the agent fixed.
func (e Entry) Share() float64 {
	if e.Total == 0 {
		return 0
	}
	return float64(e.Fixed) / float64(e.Total)
}

// tooEasyShare is the fraction of a fault pack an unassisted agent may fix
// before the problem stops discriminating between candidates.
const tooEasyShare = 0.6

// Judge assigns a verdict from a debugging attempt's numbers. An agent that
// fixes most of a pack unassisted, or that makes the app healthy end to end,
// has made the exercise a formality.
func Judge(e Entry) Verdict {
	if e.Total == 0 {
		return Inconclusive
	}
	if e.Verified || e.Share() >= tooEasyShare {
		return TooEasy
	}
	return Holds
}

// Append adds one entry to the ledger, creating it if needed.
func Append(root string, e Entry) error {
	path := filepath.Join(root, LedgerPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(f, string(raw)); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// Load reads the whole ledger. A missing ledger is not an error: no
// calibration has run yet.
func Load(root string) ([]Entry, error) {
	f, err := os.Open(filepath.Join(root, LedgerPath))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return parseLedger(f)
}

func parseLedger(r io.Reader) ([]Entry, error) {
	var out []Entry
	scan := bufio.NewScanner(r)
	scan.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for line := 1; scan.Scan(); line++ {
		text := scan.Bytes()
		if len(text) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(text, &e); err != nil {
			return nil, fmt.Errorf("%s line %d: %w", LedgerPath, line, err)
		}
		out = append(out, e)
	}
	return out, scan.Err()
}

// Latest returns the most recent entry per problem, keyed by problem id.
func Latest(entries []Entry) map[string]Entry {
	out := map[string]Entry{}
	for _, e := range entries {
		prev, ok := out[e.Problem]
		if !ok || e.At.After(prev.At) {
			out[e.Problem] = e
		}
	}
	return out
}

// Stale returns the problems whose most recent verdict says rework them,
// ordered by problem id so the ledger diffs cleanly between runs.
func Stale(entries []Entry) []Entry {
	latest := Latest(entries)
	var out []Entry
	for _, id := range slices.Sorted(maps.Keys(latest)) {
		if e := latest[id]; e.Verdict == TooEasy {
			out = append(out, e)
		}
	}
	return out
}
