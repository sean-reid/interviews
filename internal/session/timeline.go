package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// faultSample is one fault's check result at one instant.
type faultSample struct {
	T     string `json:"t"`
	Fault string `json:"fault"`
	Fixed bool   `json:"fixed"`
}

// verifySample is the end-to-end verify result at one instant.
type verifySample struct {
	T      string `json:"t"`
	Verify bool   `json:"verify"`
}

// TimelineTick samples every injected fault's check plus the end-to-end
// verify once and appends the results to workdir/timeline.jsonl. The
// timeline is the provider-agnostic record of when each fault flipped;
// grading reads it instead of an apiserver audit log.
func (m *Manager) TimelineTick(ctx context.Context) (err error) {
	statuses, err := m.Engine.Status(ctx)
	if err != nil {
		return err
	}
	now := m.now().UTC().Format(time.RFC3339)
	f, err := os.OpenFile(filepath.Join(m.Engine.Workdir, TimelineFile),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()
	enc := json.NewEncoder(f)
	fixed := 0
	for _, s := range statuses {
		if s.Fixed() {
			fixed++
		}
		if err := enc.Encode(faultSample{T: now, Fault: s.ID, Fixed: s.Fixed()}); err != nil {
			return err
		}
	}
	verified := m.Engine.Verify(ctx) == nil
	if err := enc.Encode(verifySample{T: now, Verify: verified}); err != nil {
		return err
	}
	// The checks and verify print as they run, so a sample that says nothing
	// leaves that output looking like a failure.
	fmt.Fprintf(m.Out, "sampled %d/%d fixed, verify %v, into %s\n", fixed, len(statuses), verified, TimelineFile)
	return nil
}

// TimelineRun ticks immediately and then every interval until the
// duration elapses. Tick errors are reported and the sampler keeps
// going: one failed sample must not end the record.
func (m *Manager) TimelineRun(ctx context.Context, interval, duration time.Duration) error {
	fmt.Fprintf(m.Out, "sampling every %s for %s into %s; this holds the terminal, so leave it in its own window\n",
		interval, duration, filepath.Join(m.Engine.Workdir, TimelineFile))
	deadline := m.now().Add(duration)
	for {
		if err := m.TimelineTick(ctx); err != nil {
			fmt.Fprintf(m.Out, "timeline tick: %v\n", err)
		}
		if m.now().Add(interval).After(deadline) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}
