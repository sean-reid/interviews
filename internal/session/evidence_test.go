package session

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sean-reid/interviews/internal/debug"
	"github.com/sean-reid/interviews/internal/grading"
	"github.com/sean-reid/interviews/internal/provenance"
	"github.com/sean-reid/interviews/internal/version"
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

// tarNames lists the member names of a tar.gz.
func tarNames(t *testing.T, path string) []string {
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
	var names []string
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, hdr.Name)
	}
	return names
}

// exitStatus is a script failure carrying a process exit code, the shape
// ExecRunner returns for a script that exited non-zero.
type exitStatus int

func (e exitStatus) Error() string { return fmt.Sprintf("exit status %d", int(e)) }
func (e exitStatus) ExitCode() int { return int(e) }

// A check that could not run is not a fault left unfixed. The score is what
// a grading sheet reads, so it has to carry the difference.
func TestRefreshScoreRecordsACheckThatCannotRun(t *testing.T) {
	m, r, _ := testManager(t, nil)
	wd := m.Engine.Workdir
	writeState(t, wd, "01-image-typo", "02-net-policy")
	r.scriptErr["01-image-typo/check.sh"] = exitStatus(debug.CheckCannotRunExit)

	score, err := RefreshScore(context.Background(), m.Engine)
	if err != nil {
		t.Fatal(err)
	}
	if score.Fixed != 1 || score.Total != 2 {
		t.Errorf("score = %d/%d fixed, want 1/2", score.Fixed, score.Total)
	}
	if got := score.Faults[0]; got.Fixed || !got.CheckFailed {
		t.Errorf("fault with the unrunnable check = %+v", got)
	}
	if got := score.Faults[1]; !got.Fixed || got.CheckFailed {
		t.Errorf("fault with the working check = %+v", got)
	}
	written, err := grading.LoadScore(wd)
	if err != nil || written == nil || !written.Faults[0].CheckFailed {
		t.Errorf("score.json = %+v, %v", written, err)
	}
}

func TestEvidenceBundlesWhatExists(t *testing.T) {
	m, r, _ := testManager(t, nil)
	wd := m.Engine.Workdir
	writeState(t, wd, "01-image-typo", "02-net-policy")
	for _, name := range []string{TimelineFile, CastFile, RawLogFile} {
		if err := os.WriteFile(filepath.Join(wd, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r.scriptErr["02-net-policy/check.sh"] = errors.New("still broken")

	if err := m.Evidence(context.Background(), EvidenceOptions{}); err != nil {
		t.Fatal(err)
	}
	names := tarNames(t, filepath.Join(wd, EvidenceFile))
	want := []string{debug.StateFile, grading.ScoreFile, TimelineFile, CastFile, RawLogFile}
	if fmt.Sprint(names) != fmt.Sprint(want) {
		t.Errorf("bundle members = %v, want %v", names, want)
	}

	// The refresh ran the real check paths and recorded the broken fault.
	score, err := grading.LoadScore(wd)
	if err != nil || score == nil {
		t.Fatalf("LoadScore = %v, %v", score, err)
	}
	if score.Fixed != 1 || score.Total != 2 || !score.Verified {
		t.Errorf("score = %+v", score)
	}
}

func TestEvidenceToleratesCheckErrors(t *testing.T) {
	m, _, _ := testManager(t, nil)
	wd := m.Engine.Workdir
	// State naming an unknown fault makes the score refresh itself error,
	// not just report a fault broken.
	writeState(t, wd, "99-ghost")

	if err := m.Evidence(context.Background(), EvidenceOptions{Final: true}); err != nil {
		t.Fatalf("final evidence must never abort on a check error: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(wd, ScoreErrorFile))
	if err != nil || !strings.Contains(string(raw), "99-ghost") {
		t.Errorf("score-error.txt = %q, %v", raw, err)
	}
	names := tarNames(t, filepath.Join(wd, EvidenceFile))
	if fmt.Sprint(names) != fmt.Sprint([]string{debug.StateFile, ScoreErrorFile}) {
		t.Errorf("bundle members = %v", names)
	}

	// A periodic (non-final) pass still bundles but surfaces the error.
	if err := m.Evidence(context.Background(), EvidenceOptions{}); err == nil {
		t.Error("non-final evidence should surface the refresh error")
	}
}

func TestEvidenceUploadsToS3(t *testing.T) {
	m, r, _ := testManager(t, nil)
	wd := m.Engine.Workdir
	writeState(t, wd, "01-image-typo")

	if err := m.Evidence(context.Background(), EvidenceOptions{S3: "s3://bucket/prefix/"}); err != nil {
		t.Fatal(err)
	}
	want := "aws s3 cp " + filepath.Join(wd, EvidenceFile) + " s3://bucket/prefix/" + EvidenceFile
	if len(r.callsMatching(want)) != 1 {
		t.Errorf("no upload call %q in %v", want, r.calls)
	}
}

// The checks print as they run, so an evidence pass that says nothing
// leaves the operator reading a kubectl timeout as the command failing,
// with no idea whether a bundle was written or where.
func TestEvidenceSaysWhatItDidAndWhereTheBundleIs(t *testing.T) {
	m, _, out := testManager(t, nil)
	writeState(t, m.Engine.Workdir, "01-image-typo")

	if err := m.Evidence(context.Background(), EvidenceOptions{}); err != nil {
		t.Fatal(err)
	}
	tarPath := filepath.Join(m.Engine.Workdir, EvidenceFile)
	for _, want := range []string{"score: 1/1 faults fixed", "evidence: " + tarPath} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("evidence output missing %q, got %q", want, out.String())
		}
	}
}

// A refresh that could not run must not pass for a clean pass: the score in
// the bundle is then the previous one, and the sheet will date it.
func TestEvidenceNamesAFailedRefresh(t *testing.T) {
	m, _, out := testManager(t, nil)
	writeState(t, m.Engine.Workdir, "nonexistent-fault")

	err := m.Evidence(context.Background(), EvidenceOptions{Final: true})
	if err != nil {
		t.Fatalf("final pass = %v, want it to record the failure and carry on", err)
	}
	if !strings.Contains(out.String(), "could not be refreshed") {
		t.Errorf("failed refresh not reported: %q", out.String())
	}
	if !strings.Contains(out.String(), "evidence: ") {
		t.Errorf("bundle location not reported: %q", out.String())
	}
}

// Teardown removes the environment but keeps the workdir, so an evidence
// pass afterwards must not try to check faults that are gone.
func TestEvidenceSkipsTheRefreshAfterTeardown(t *testing.T) {
	m, r, _ := testManager(t, nil)
	writeState(t, m.Engine.Workdir, "01-image-typo")
	st, err := debug.LoadState(m.Engine.Workdir)
	if err != nil {
		t.Fatal(err)
	}
	st.TornDownAt = time.Now()
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.Engine.Workdir, debug.StateFile), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := m.Evidence(context.Background(), EvidenceOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := r.callsMatching("check.sh"); len(got) != 0 {
		t.Errorf("checked a torn-down environment: %v", got)
	}
	if _, err := os.Stat(filepath.Join(m.Engine.Workdir, EvidenceFile)); err != nil {
		t.Errorf("no bundle written after teardown: %v", err)
	}
}

// The bundle is the only thing that leaves the machine, and comparing two
// candidates on one seeded problem only holds if both ran on the same thing.
// The state file it carries has to say what that was.
func TestBundleCarriesWhatTheEnvironmentWasProducedOn(t *testing.T) {
	m, r, _ := testManager(t, nil)
	m.Engine.Origin = provenance.New(provenance.Host, "v-0123456789abcdef")
	m.Engine.Scenario.Env.Kind.NodeImage = "kindest/node:v1.31.4@sha256:0badc0de"
	r.outputs["kind version"] = "kind v0.29.0 go1.24.2 linux/amd64\n"
	r.outputs["-o json"] = `{"clientVersion":{"gitVersion":"v1.33.2"}}`

	ctx := context.Background()
	if err := m.Engine.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Evidence(ctx, EvidenceOptions{}); err != nil {
		t.Fatal(err)
	}

	var st debug.State
	raw := tarFile(t, filepath.Join(m.Engine.Workdir, EvidenceFile), debug.StateFile)
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatal(err)
	}
	if st.Provenance == nil {
		t.Fatalf("the bundled state says nothing about the machine: %s", raw)
	}
	// Against the command that built the cluster, so the bundle is checked
	// against what really ran rather than against the fake's own script.
	created := r.callsMatching("kind create cluster")
	if len(created) != 1 || !strings.Contains(created[0], "--image "+st.Provenance.NodeImage) {
		t.Errorf("bundled node image %q is not what built the cluster: %v", st.Provenance.NodeImage, created)
	}
	if st.Provenance.Kind != "v0.29.0" || st.Provenance.Kubectl != "v1.33.2" {
		t.Errorf("bundled tool versions = %+v", st.Provenance)
	}
	if st.Provenance.Where != provenance.Host || st.Provenance.Content != "v-0123456789abcdef" {
		t.Errorf("bundle does not say where it was produced: %+v", st.Provenance)
	}
	if st.Provenance.Platform != version.Version {
		t.Errorf("bundled platform version = %q, want %q", st.Provenance.Platform, version.Version)
	}
}

// tarFile returns one member's contents.
func tarFile(t *testing.T, path, want string) []byte {
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
		if errors.Is(err, io.EOF) {
			t.Fatalf("%s has no %s", path, want)
		}
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Name != want {
			continue
		}
		raw, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		if int64(len(raw)) != hdr.Size {
			t.Fatalf("%s header says %d bytes, body has %d", want, hdr.Size, len(raw))
		}
		return raw
	}
}

// The bundle is the artifact that leaves the machine and sits in a bucket.
// Nothing grading needs is a credential: the URL tokens are the whole of
// the session authentication, and the candidate kubeconfig is cluster
// access.
func TestEvidenceBundleCarriesNoCredentials(t *testing.T) {
	m, _, _ := testManager(t, nil)
	wd := m.Engine.Workdir
	writeState(t, wd, "01-image-typo")
	if err := os.WriteFile(filepath.Join(wd, KubeconfigFile), []byte("token: sa-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(context.Background(), StartOptions{
		BaseURL:        "https://host/",
		CandidateToken: strings.Repeat("c", 32),
		ObserverToken:  strings.Repeat("o", 32),
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.Evidence(context.Background(), EvidenceOptions{}); err != nil {
		t.Fatal(err)
	}
	tarPath := filepath.Join(wd, EvidenceFile)

	if slices.Contains(tarNames(t, tarPath), KubeconfigFile) {
		t.Error("bundle carries the candidate kubeconfig")
	}
	info := tarFile(t, tarPath, InfoFile)
	for _, secret := range []string{strings.Repeat("c", 32), strings.Repeat("o", 32)} {
		if strings.Contains(string(info), secret) {
			t.Errorf("bundled %s carries a URL token", InfoFile)
		}
	}
	// Still readable, and still says which session it was.
	var parsed Info
	if err := json.Unmarshal(info, &parsed); err != nil {
		t.Fatalf("bundled %s is not valid json: %v", InfoFile, err)
	}
	if parsed.Problem == "" || parsed.StartedAt.IsZero() {
		t.Errorf("redaction took the parts grading reads: %+v", parsed)
	}

	// The workdir copy keeps them: the host needs the tokens while the
	// session runs.
	local, err := LoadInfo(wd)
	if err != nil {
		t.Fatal(err)
	}
	if local.CandidateToken == "" {
		t.Error("redacted the workdir copy, not just the bundle")
	}
}
