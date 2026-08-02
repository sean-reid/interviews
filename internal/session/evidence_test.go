package session

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// The bundle syncs to the bucket every two minutes while the interview is
// still running, so a token inside it is a live credential. The old test
// named the two fields redacted() cleared, which is the implementation
// restated: it could not notice a third field arriving, and one had. This
// asserts the property instead, over whatever fields Info happens to have.
func TestRedactionLeavesNoCredentialInTheBundle(t *testing.T) {
	secret := func(field string) string { return "SECRET-" + field }
	var info Info
	v := reflect.ValueOf(&info).Elem()
	var planted []string
	for i := range v.NumField() {
		name := v.Type().Field(i).Name
		if v.Field(i).Kind() != reflect.String || !credentialField(name) {
			continue
		}
		v.Field(i).SetString(secret(name))
		planted = append(planted, name)
	}
	if len(planted) < 5 {
		t.Fatalf("planted %v; Info should carry at least the two tokens, two urls and the app pair", planted)
	}

	raw, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	// Positive control: the values are really in the unredacted file, so a
	// clean result below means redaction worked rather than that the search
	// cannot find anything.
	for _, name := range planted {
		if !bytes.Contains(raw, []byte(secret(name))) {
			t.Fatalf("%s never reached the file; this test cannot prove redaction", name)
		}
	}

	out, err := redactedInfo(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range planted {
		if bytes.Contains(out, []byte(secret(name))) {
			t.Errorf("%s survives into the evidence bundle", name)
		}
	}
}

// credentialField names the Info fields that are secrets or that embed one.
func credentialField(name string) bool {
	return strings.Contains(name, "Token") || strings.Contains(name, "URL")
}

// The candidate's own directory is the work being assessed. Teardown
// reported it as evidence worth keeping while the artifact that leaves the
// machine, and the only thing that survives for grading weeks later, did not
// carry it: the bundle shipped a flat list of engine files.
func TestBundleCarriesTheCandidatesWork(t *testing.T) {
	workdir := t.TempDir()
	work := filepath.Join(workdir, CandidateDir, "notes")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "theory.md"), []byte("the selector is wrong"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Over the cap, so it is named rather than shipped or silently dropped.
	big := filepath.Join(workdir, CandidateDir, "dump.log")
	if err := os.WriteFile(big, make([]byte, candidateFileLimit+1), 0o644); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(t.TempDir(), EvidenceFile)
	if err := bundle(workdir, dest); err != nil {
		t.Fatal(err)
	}
	got := tarNames(t, dest)
	if !slices.Contains(got, "candidate/notes/theory.md") {
		t.Errorf("the candidate's work is not in the bundle: %v", got)
	}
	if slices.Contains(got, "candidate/dump.log") {
		t.Error("a file over the cap was shipped anyway")
	}
	skipped := tarMember(t, dest, "skipped.txt")
	if skipped == "" {
		t.Fatal("a file was left out and the bundle does not say so")
	}
	if !strings.Contains(skipped, "candidate/dump.log") {
		t.Errorf("skipped.txt = %q, want it to name what was left out", skipped)
	}
}

// A killed recorder restarts into numbered cast segments. The bundle has to
// carry every segment plus the log saying why they exist, or ending the
// recorder would still cost the rest of the recording.
func TestBundleCarriesCastSegmentsAndRecorderLog(t *testing.T) {
	workdir := t.TempDir()
	members := map[string]string{
		CastFile:        "segment zero",
		CastFile + ".1": "segment one",
		CastFile + ".2": "segment two",
		RecorderLogFile: "2026-08-02T12:00:00Z the recorder exited with the session still up; restarting into session.cast.1\n",
	}
	for name, body := range members {
		if err := os.WriteFile(filepath.Join(workdir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dest := filepath.Join(t.TempDir(), EvidenceFile)
	if err := bundle(workdir, dest); err != nil {
		t.Fatal(err)
	}
	for name, body := range members {
		if got := tarMember(t, dest, name); got != body {
			t.Errorf("%s in the bundle = %q, want %q", name, got, body)
		}
	}
}

// The cast grows for the length of the interview and the bundle is rebuilt
// every two minutes, so bundling has to stream it: reading it whole once per
// sync pass is how a chatty terminal could OOM the host mid-interview.
func TestBundleStreamsTheCast(t *testing.T) {
	workdir := t.TempDir()
	cast := make([]byte, 32<<20)
	for i := range cast {
		cast[i] = byte(i * 7)
	}
	if err := os.WriteFile(filepath.Join(workdir, CastFile), cast, 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), EvidenceFile)

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	if err := bundle(workdir, dest); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)

	if got := tarMember(t, dest, CastFile); got != string(cast) {
		t.Errorf("bundle carries %d of the cast's %d bytes, or altered them", len(got), len(cast))
	}
	if alloc := after.TotalAlloc - before.TotalAlloc; alloc > uint64(len(cast))/2 {
		t.Errorf("bundling a %d byte cast allocated %d bytes; the cast must be streamed, not read whole", len(cast), alloc)
	}
}

// tarMember returns one member's contents from a tar.gz, or "" if absent.
func tarMember(t *testing.T, path, want string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Error(err)
		}
	}()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return ""
		}
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Name == want {
			raw, err := io.ReadAll(tr)
			if err != nil {
				t.Fatal(err)
			}
			return string(raw)
		}
	}
}
