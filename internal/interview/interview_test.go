package interview

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func home(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(HomeEnv, dir)
	return dir
}

func save(t *testing.T, seed string, created time.Time, ended time.Time) *Session {
	t.Helper()
	s := &Session{
		Seed: seed, Problem: "orbit-shop", Mode: Local,
		CreatedAt: created, EndedAt: ended,
		Workdir: filepath.Join("/tmp", seed),
	}
	if err := Save(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSaveLoadRoundTripsAndSetsCurrent(t *testing.T) {
	home(t)
	now := time.Now().Truncate(time.Second)
	want := &Session{
		Seed: "calm-bison-0731", Problem: "relay", Mode: AWS, Level: "senior",
		CreatedAt: now, TTLMinutes: 120,
		Workdir: "/tmp/wd", TerraformDir: "/tmp/tf",
		Evidence: "s3://bucket/calm-bison-0731/", CandidateURL: "https://h/c/tok",
		ObserverURL: "https://h/o/tok", Host: "1.2.3.4",
	}
	if err := Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := Load("calm-bison-0731")
	if err != nil {
		t.Fatal(err)
	}
	// Compare the encodings, not the structs: a round-tripped time.Time
	// carries a fixed zone where the original carried the local one, so ==
	// reports two identical-looking values as different.
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("round trip lost fields:\n got %s\nwant %s", gotJSON, wantJSON)
	}
	cur, err := Current()
	if err != nil {
		t.Fatal(err)
	}
	if cur.Seed != want.Seed {
		t.Errorf("current = %q, want %q", cur.Seed, want.Seed)
	}
}

// The record holds both URL tokens, which are the only thing between the
// internet and a shell on the host.
func TestRecordIsOwnerOnly(t *testing.T) {
	dir := home(t)
	save(t, "calm-bison-0731", time.Now(), time.Time{})
	if err := SetCurrent("calm-bison-0731"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		filepath.Join(dir, "sessions", "calm-bison-0731.json"),
		filepath.Join(dir, currentFile),
	} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Errorf("%s mode = %o, want 600", filepath.Base(p), mode)
		}
	}
}

func TestListIsNewestFirst(t *testing.T) {
	home(t)
	base := time.Now()
	save(t, "old-otter-0729", base.Add(-48*time.Hour), time.Time{})
	save(t, "new-heron-0731", base, time.Time{})
	save(t, "mid-ibex-0730", base.Add(-24*time.Hour), time.Time{})

	all, err := List()
	if err != nil {
		t.Fatal(err)
	}
	var seeds []string
	for _, s := range all {
		seeds = append(seeds, s.Seed)
	}
	want := []string{"new-heron-0731", "mid-ibex-0730", "old-otter-0729"}
	if strings.Join(seeds, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", seeds, want)
	}
}

func TestListSkipsAnUnreadableRecord(t *testing.T) {
	dir := home(t)
	save(t, "calm-bison-0731", time.Now(), time.Time{})
	if err := os.WriteFile(filepath.Join(dir, "sessions", "broken.json"), []byte("{oh no"), 0o600); err != nil {
		t.Fatal(err)
	}
	all, err := List()
	if err != nil {
		t.Fatalf("one bad record hid the rest: %v", err)
	}
	if len(all) != 1 || all[0].Seed != "calm-bison-0731" {
		t.Errorf("list = %v, want just the good record", all)
	}
}

// Losing the marker must not lose the session. One open session is
// unambiguous; two is not, and guessing there would route a hint into the
// wrong interview.
func TestCurrentFallsBackToTheOnlyOpenSession(t *testing.T) {
	dir := home(t)
	save(t, "calm-bison-0731", time.Now(), time.Time{})
	save(t, "done-otter-0730", time.Now().Add(-time.Hour), time.Now())
	if _, err := os.Stat(filepath.Join(dir, currentFile)); !os.IsNotExist(err) {
		t.Fatalf("saving a record wrote the marker; this test needs it absent (%v)", err)
	}

	cur, err := Current()
	if err != nil {
		t.Fatal(err)
	}
	if cur.Seed != "calm-bison-0731" {
		t.Errorf("current = %q, want the open one", cur.Seed)
	}

	save(t, "keen-crane-0731", time.Now(), time.Time{})
	_, err = Current()
	if err == nil {
		t.Fatal("two open sessions and no marker resolved to one of them")
	}
	for _, want := range []string{"calm-bison-0731", "keen-crane-0731", "--seed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestCurrentWithNothingStarted(t *testing.T) {
	home(t)
	if _, err := Current(); !errors.Is(err, ErrNoCurrent) {
		t.Errorf("current on an empty registry = %v, want ErrNoCurrent", err)
	}
}

// A stale marker pointing at a removed session must not break the
// commands that fall back to it.
func TestCurrentIgnoresAMarkerPointingNowhere(t *testing.T) {
	home(t)
	save(t, "calm-bison-0731", time.Now(), time.Time{})
	save(t, "keen-crane-0731", time.Now(), time.Time{})
	if err := Remove("keen-crane-0731"); err != nil {
		t.Fatal(err)
	}
	cur, err := Current()
	if err != nil {
		t.Fatalf("removing the current session broke current: %v", err)
	}
	if cur.Seed != "calm-bison-0731" {
		t.Errorf("current = %q, want the remaining session", cur.Seed)
	}
}

func TestRemoveLeavesTheOtherRecords(t *testing.T) {
	home(t)
	save(t, "calm-bison-0731", time.Now(), time.Time{})
	save(t, "keen-crane-0731", time.Now(), time.Time{})
	if err := Remove("calm-bison-0731"); err != nil {
		t.Fatal(err)
	}
	if _, err := Load("calm-bison-0731"); err == nil {
		t.Error("record survived removal")
	}
	if _, err := Load("keen-crane-0731"); err != nil {
		t.Errorf("removal took the other record: %v", err)
	}
	if err := Remove("calm-bison-0731"); err != nil {
		t.Errorf("removing twice = %v, want it to be fine", err)
	}
}

func TestNewSeedShapeAndUniqueness(t *testing.T) {
	home(t)
	now := time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)
	seen := map[string]bool{}
	for range 40 {
		seed, err := NewSeed(now)
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidSeed(seed); err != nil {
			t.Fatalf("generated an invalid seed %q: %v", seed, err)
		}
		if !strings.HasSuffix(seed, "-0731") {
			t.Errorf("seed %q does not carry the date, which is what makes a list readable later", seed)
		}
		if seen[seed] {
			t.Fatalf("generated %q twice before it was saved anywhere", seed)
		}
		seen[seed] = true
		save(t, seed, now, time.Time{})
	}
}

// A generated seed never collides, but a typed one can, and reusing a seed
// would append to another interview's evidence.
func TestNewSeedSkipsAnExistingRecord(t *testing.T) {
	home(t)
	now := time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)
	first, err := NewSeed(now)
	if err != nil {
		t.Fatal(err)
	}
	save(t, first, now, time.Time{})
	for range 20 {
		next, err := NewSeed(now)
		if err != nil {
			t.Fatal(err)
		}
		if next == first {
			t.Fatalf("NewSeed returned %q, which is already recorded", next)
		}
	}
}

// Seeds name files, kubernetes objects, and cluster names. One with a
// slash would write the record somewhere else entirely.
func TestValidSeedRejectsUnsafeNames(t *testing.T) {
	for _, bad := range []string{"", "../escape", "has space", "UPPER", "semi;colon", strings.Repeat("a", 65)} {
		if err := ValidSeed(bad); err == nil {
			t.Errorf("ValidSeed(%q) = nil, want an error", bad)
		}
	}
	for _, ok := range []string{"calm-bison-0731", "a", "seed-123"} {
		if err := ValidSeed(ok); err != nil {
			t.Errorf("ValidSeed(%q) = %v", ok, err)
		}
	}
}

func TestSaveRefusesAnUnsafeSeed(t *testing.T) {
	dir := home(t)
	if err := Save(&Session{Seed: "../../escape", Problem: "relay"}); err == nil {
		t.Fatal("saved a record under a traversing seed")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape.json")); err == nil {
		t.Error("wrote outside the registry")
	}
}

// A seed becomes a key in the same bucket that holds the shared tarball and
// every interview's terraform state, and the host's role is scoped to its
// own seed prefix. So a seed spelling one of those prefixes would hand a
// candidate's host write access to what every future host executes.
func TestValidSeedRejectsTheBucketsOwnPrefixes(t *testing.T) {
	// Positive control: an ordinary seed of the same shape is accepted, so a
	// rejection below is about the name and not about the check being broken.
	if err := ValidSeed("state-machine-0801"); err != nil {
		t.Fatalf("an ordinary seed was rejected (%v); this test proves nothing", err)
	}
	for _, seed := range reservedSeeds {
		if err := ValidSeed(seed); err == nil {
			t.Errorf("seed %q accepted, and it names a prefix the bucket already uses", seed)
		}
	}
}

// Ending a session used to make it current, because saving a record set the
// marker and end saves the record it just ended. The next unqualified hint
// then wrote into a workdir teardown had already deleted.
func TestEndedSessionsDoNotStayCurrent(t *testing.T) {
	home(t)
	save(t, "calm-bison-0801", time.Now(), time.Time{})
	if err := SetCurrent("calm-bison-0801"); err != nil {
		t.Fatal(err)
	}
	save(t, "keen-crane-0801", time.Now(), time.Time{})
	if err := SetCurrent("keen-crane-0801"); err != nil {
		t.Fatal(err)
	}
	// Positive control: it really is current before it ends.
	if cur, err := Current(); err != nil || cur.Seed != "keen-crane-0801" {
		t.Fatalf("current = %v (%v), want keen-crane-0801; this test proves nothing", cur, err)
	}

	save(t, "keen-crane-0801", time.Now(), time.Now())
	if err := ClearCurrent("keen-crane-0801"); err != nil {
		t.Fatal(err)
	}
	cur, err := Current()
	if err != nil {
		t.Fatal(err)
	}
	if cur.Seed != "calm-bison-0801" {
		t.Errorf("current = %q after ending the other one, want the session still open", cur.Seed)
	}
}

// Preparing the next candidate's take-home in a second terminal must not
// repoint the hints of the interview in progress.
func TestPreparingADropDoesNotStealALiveSession(t *testing.T) {
	home(t)
	save(t, "calm-bison-0801", time.Now(), time.Time{})
	if err := SetCurrent("calm-bison-0801"); err != nil {
		t.Fatal(err)
	}
	save(t, "brisk-ember-0801", time.Now(), time.Time{})
	if err := SetCurrentIfIdle("brisk-ember-0801"); err != nil {
		t.Fatal(err)
	}
	if cur, _ := Current(); cur == nil || cur.Seed != "calm-bison-0801" {
		t.Errorf("current = %v, want the live interview to keep it", cur)
	}

	// With nothing live, the drop is the obvious thing to mean.
	save(t, "calm-bison-0801", time.Now(), time.Now())
	if err := ClearCurrent("calm-bison-0801"); err != nil {
		t.Fatal(err)
	}
	if err := SetCurrentIfIdle("brisk-ember-0801"); err != nil {
		t.Fatal(err)
	}
	if cur, _ := Current(); cur == nil || cur.Seed != "brisk-ember-0801" {
		t.Errorf("current = %v, want the drop once nothing is live", cur)
	}
}

// The contract the two tests above rest on: writing a record says nothing
// about which session you are in. Coupling them is what made ending one
// select it and preparing a drop steal a live interview.
func TestSaveDoesNotTouchTheCurrentMarker(t *testing.T) {
	dir := home(t)
	if err := SetCurrent("calm-bison-0801"); err != nil {
		t.Fatal(err)
	}
	save(t, "brisk-ember-0801", time.Now(), time.Time{})
	raw, err := os.ReadFile(filepath.Join(dir, currentFile))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(raw)); got != "calm-bison-0801" {
		t.Errorf("marker = %q after saving another record, want it untouched", got)
	}
}
