package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/sean-reid/interviews/internal/interview"
)

// The discovery reads tags the terraform module writes, and nothing else
// connects the two: rename a tag on either side and the listing quietly stops
// finding hosts. So this reads the real module rather than a copy of it.
func TestTheModuleWritesTheTagsDiscoveryReads(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "infra", "aws", "interview", "versions.tf"))
	if err != nil {
		t.Fatal(err)
	}
	tagged := func(key string) bool {
		return regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(key) + `\s*=`).Match(raw)
	}
	for _, key := range []string{tagManagedBy, tagInterview, tagProblem, tagTTL} {
		if !tagged(key) {
			t.Errorf("the module does not set the %s tag, so discovery cannot find a host by it", key)
		}
	}
	// A tag the module does not set, proving the search above can fail.
	if tagged("NotATagAnythingSets") {
		t.Fatal("the matcher finds tags that are not there, so the assertions above prove nothing")
	}
	if !regexp.MustCompile(`"` + regexp.QuoteMeta(tagOwner) + `"`).Match(raw) {
		t.Errorf("the module does not tag resources with %q, which is the filter every lookup uses", tagOwner)
	}
}

func TestParseHostsReadsTheTags(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "describe-instances.json"))
	if err != nil {
		t.Fatal(err)
	}
	hosts, err := parseHosts(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 1 {
		t.Fatalf("got %d hosts, want 1: %+v", len(hosts), hosts)
	}
	h := hosts[0]
	if h.Seed == "" || h.Problem == "" || h.TTLMinutes == 0 || h.LaunchedAt.IsZero() {
		t.Errorf("a field did not survive the tags: %+v", h)
	}
	if h.State != "running" {
		t.Errorf("state = %q, want running", h.State)
	}
}

// An instance the module made but this machine never recorded is the case the
// whole flag exists for: nothing local can see it, and it bills either way.
func TestReconcileSurfacesAHostThisMachineDoesNotKnow(t *testing.T) {
	rows := reconcile(nil, nil, []remoteHost{{
		Seed: "other-laptop-0801", Problem: "relay", State: "running",
		LaunchedAt: time.Now().Add(-10 * time.Minute), TTLMinutes: 120,
	}})
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if !rows[0].elsewhere {
		t.Error("the row is not marked as belonging to no local record")
	}
	if rows[0].rank != rankLive {
		t.Errorf("rank = %d, want a live session", rows[0].rank)
	}
}

// Past its ttl is the same finding whoever started it, so it sorts with the
// rest of the stranded work rather than with the running interviews.
func TestReconcileRanksAnExpiredStrayFirst(t *testing.T) {
	rows := reconcile(nil, nil, []remoteHost{{
		Seed: "forgotten-host-0801", Problem: "orbit-shop", State: "running",
		LaunchedAt: time.Now().Add(-3 * time.Hour), TTLMinutes: 60,
	}})
	if rows[0].state != "past its ttl" || rows[0].rank != rankStranded {
		t.Errorf("state %q rank %d, want a stranded host past its ttl", rows[0].state, rows[0].rank)
	}
}

func TestReconcileReadsBackFromTheAccount(t *testing.T) {
	live := &interview.Session{Seed: "live-host-0801", Mode: interview.AWS, Host: "203.0.113.1"}
	gone := &interview.Session{Seed: "gone-host-0801", Mode: interview.AWS, Host: "203.0.113.2"}
	applying := &interview.Session{Seed: "applying-now-0801", Mode: interview.AWS}
	ended := &interview.Session{Seed: "ended-host-0801", Mode: interview.AWS,
		Host: "203.0.113.3", EndedAt: time.Now().Add(-time.Hour)}

	rows := []sessionRow{
		{s: live, state: "up", rank: rankLive},
		{s: gone, state: "up", rank: rankLive},
		{s: applying, state: "provisioning", rank: rankLive},
		{s: ended, state: "ended", rank: rankClosed},
	}
	known := map[string]*interview.Session{}
	for _, r := range rows {
		known[r.s.Seed] = r.s
	}
	rows = reconcile(rows, known, []remoteHost{
		{Seed: "live-host-0801", State: "running", LaunchedAt: time.Now().Add(-5 * time.Minute), TTLMinutes: 120},
		{Seed: "ended-host-0801", State: "running", LaunchedAt: time.Now().Add(-2 * time.Hour), TTLMinutes: 120},
	})

	want := map[string]struct {
		state string
		rank  int
	}{
		// The account agrees, and its clock is the one that counts.
		"live-host-0801": {"up, 1h of ttl left", rankLive},
		// The record says a host is up and there is no instance.
		"gone-host-0801": {"host gone", rankStranded},
		// No address yet, so the apply may still be running.
		"applying-now-0801": {"provisioning", rankLive},
		// Ended here, still running there: the destroy did not finish.
		"ended-host-0801": {"still up after end", rankStranded},
	}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows, want %d", len(rows), len(want))
	}
	for _, r := range rows {
		w, ok := want[r.s.Seed]
		if !ok {
			t.Errorf("unexpected row %s", r.s.Seed)
			continue
		}
		if r.state != w.state || r.rank != w.rank {
			t.Errorf("%s: state %q rank %d, want %q rank %d", r.s.Seed, r.state, r.rank, w.state, w.rank)
		}
		if r.elsewhere {
			t.Errorf("%s: marked as having no local record, but it has one", r.s.Seed)
		}
	}
}

func TestSessionsRemoteWithoutAnAWSSetup(t *testing.T) {
	t.Setenv(interview.HomeEnv, t.TempDir())
	code, _, stderr := run(t, "sessions", "--remote")
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(stderr, "setup aws") {
		t.Errorf("stderr = %q, want the command that fixes it", stderr)
	}
}
