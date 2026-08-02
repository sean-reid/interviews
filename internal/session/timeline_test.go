package session

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTimelineTickAppendsJSONL(t *testing.T) {
	m, r, _ := testManager(t, nil)
	wd := m.Engine.Workdir
	writeState(t, wd, "01-image-typo", "02-net-policy")
	r.scriptErr["02-net-policy/check.sh"] = errors.New("still broken")

	ctx := context.Background()
	if err := m.TimelineTick(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.TimelineTick(ctx); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(wd, TimelineFile))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 6 { // two ticks of two faults plus verify
		t.Fatalf("timeline lines = %d: %q", len(lines), raw)
	}
	var first faultSample
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if first.Fault != "01-image-typo" || !first.Fixed {
		t.Errorf("first sample = %+v", first)
	}
	if _, err := time.Parse(time.RFC3339, first.T); err != nil {
		t.Errorf("t not RFC3339: %q", first.T)
	}
	var second faultSample
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatal(err)
	}
	if second.Fault != "02-net-policy" || second.Fixed {
		t.Errorf("second sample = %+v", second)
	}
	var last verifySample
	if err := json.Unmarshal([]byte(lines[2]), &last); err != nil {
		t.Fatal(err)
	}
	if !last.Verify || last.T != first.T {
		t.Errorf("verify sample = %+v", last)
	}
}

func TestTimelineRunTicksUntilDeadline(t *testing.T) {
	m, r, _ := testManager(t, nil)
	writeState(t, m.Engine.Workdir, "01-image-typo")
	// Frozen now makes the deadline arithmetic deterministic: with the
	// clock pinned, now+interval never passes now+duration, so cap ticks
	// by advancing a fake clock manually.
	base := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	elapsed := time.Duration(0)
	m.now = func() time.Time {
		now := base.Add(elapsed)
		elapsed += 5 * time.Millisecond
		return now
	}
	if err := m.TimelineRun(context.Background(), time.Millisecond, 12*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if got := len(r.callsMatching("01-image-typo/check.sh")); got < 2 {
		t.Errorf("ticks = %d, want at least 2", got)
	}
}

// The timeline answers when the app came back, which is not the same
// question as whether every check passes.
func TestTimelineSamplesTheApp(t *testing.T) {
	m, _, _ := testManager(t, appFixture)
	writeState(t, m.Engine.Workdir, "01-image-typo")
	if _, err := m.Start(context.Background(), StartOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := m.TimelineTick(context.Background()); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(m.Engine.Workdir, TimelineFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"app"`) {
		t.Errorf("timeline has no app sample:\n%s", raw)
	}
}
