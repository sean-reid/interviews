package session

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/sean-reid/interviews/internal/content"
	"github.com/sean-reid/interviews/internal/debug"
	"github.com/sean-reid/interviews/internal/grading"
	"github.com/sean-reid/interviews/internal/taxonomy"
	"github.com/sean-reid/interviews/internal/variant"
)

// fakeRunner records every call. Session logic never touches tmux, ttyd,
// kubectl, or aws in tests.
type fakeRunner struct {
	calls     []string
	starts    int
	outputs   map[string]string // substring of the command line -> stdout
	failCmd   map[string]error  // substring -> Command error
	failStart map[string]error  // substring -> Start error
	scriptErr map[string]error  // parent/base -> Script error
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{
		outputs: map[string]string{}, failCmd: map[string]error{},
		failStart: map[string]error{}, scriptErr: map[string]error{},
	}
}

func match(m map[string]error, line string) error {
	for sub, err := range m {
		if strings.Contains(line, sub) {
			return err
		}
	}
	return nil
}

func (r *fakeRunner) Command(_ context.Context, name string, args ...string) error {
	line := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, line)
	return match(r.failCmd, line)
}

func (r *fakeRunner) Output(_ context.Context, name string, args ...string) (string, error) {
	line := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, line)
	for sub, out := range r.outputs {
		if strings.Contains(line, sub) {
			return out, nil
		}
	}
	return "", nil
}

func (r *fakeRunner) Script(_ context.Context, path, _ string, _ map[string]string) error {
	key := filepath.Base(filepath.Dir(path)) + "/" + filepath.Base(path)
	r.calls = append(r.calls, "script "+key)
	return match(r.scriptErr, key)
}

func (r *fakeRunner) Start(_ context.Context, name string, args ...string) (int, error) {
	line := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, "start "+line)
	if err := match(r.failStart, line); err != nil {
		return 0, err
	}
	r.starts++
	return 40000 + r.starts, nil
}

func (r *fakeRunner) callsMatching(sub string) []string {
	var out []string
	for _, c := range r.calls {
		if strings.Contains(c, sub) {
			out = append(out, c)
		}
	}
	return out
}

const problemYAML = `schema: 1
id: pipeline-meltdown
type: debugging
title: T
summary: S
disciplines: [systems]
levels: [mid]
flavor: kubernetes
time:
  session_minutes: 60
params:
  fault_pack:
    type: choice
    of: [pack-a, pack-b]
visibility:
  candidate: [candidate/**]
`

// testManager builds a manager over a minimal kind scenario with a fake
// runner and a temp workdir.
func testManager(t *testing.T, mutate func(fstest.MapFS)) (*Manager, *fakeRunner, *strings.Builder) {
	t.Helper()
	execFile := &fstest.MapFile{Data: []byte("#!/bin/sh\n"), Mode: 0o755}
	fsys := fstest.MapFS{
		"problem.yaml":                    &fstest.MapFile{Data: []byte(problemYAML)},
		"candidate/brief.md":              &fstest.MapFile{Data: []byte("brief")},
		"env.yaml":                        &fstest.MapFile{Data: []byte("provider: kind\nkind:\n  manifests: env/manifests\n  namespace: shop\nverify: env/verify.sh\n")},
		"env/verify.sh":                   execFile,
		"env/manifests/00-ns.yaml":        &fstest.MapFile{Data: []byte("kind: Namespace")},
		"faults/01-image-typo/fault.yaml": &fstest.MapFile{Data: []byte("id: 01-image-typo\ntitle: Image tag typo\ntier: easy\npacks: [pack-a, pack-b]\n")},
		"faults/01-image-typo/inject.sh":  execFile,
		"faults/01-image-typo/check.sh":   execFile,
		"faults/01-image-typo/fix.sh":     execFile,
		"faults/01-image-typo/notes.md":   &fstest.MapFile{Data: []byte("notes")},
		"faults/02-net-policy/fault.yaml": &fstest.MapFile{Data: []byte("id: 02-net-policy\ntitle: Net policy\ntier: hard\npacks: [pack-b]\n")},
		"faults/02-net-policy/inject.sh":  execFile,
		"faults/02-net-policy/check.sh":   execFile,
		"faults/02-net-policy/fix.sh":     execFile,
		"faults/02-net-policy/notes.md":   &fstest.MapFile{Data: []byte("notes")},
	}
	if mutate != nil {
		mutate(fsys)
	}
	p, issues := content.Load(fsys, taxonomy.Debugging, "pipeline-meltdown")
	if content.Errors(issues) {
		t.Fatalf("fixture invalid: %v", issues)
	}
	s, issues := debug.LoadScenario(p)
	if len(issues) != 0 {
		t.Fatalf("scenario invalid: %v", issues)
	}
	v, err := variant.Resolve("pipeline-meltdown", p.Manifest.Params, "test-seed",
		map[string]string{"fault_pack": "pack-b"})
	if err != nil {
		t.Fatal(err)
	}
	r := newFakeRunner()
	e, err := debug.NewEngine("/problems/pipeline-meltdown", s, v, r, io.Discard, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e.Settle, e.PollInterval, e.FixTimeout, e.VerifyTimeout = 0, 0, 0, 0
	var out strings.Builder
	m := NewManager(e, &out)
	m.now = func() time.Time { return time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC) }
	return m, r, &out
}

// writeState stands in for env up + break having run.
func writeState(t *testing.T, workdir string, injected ...string) {
	t.Helper()
	st := debug.State{
		Problem: "pipeline-meltdown", Seed: "test-seed", Pack: "pack-b",
		Injected: injected, Provider: "kind", CreatedAt: time.Now(),
	}
	raw, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workdir, debug.StateFile), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestStartSpawnsSessionProcesses(t *testing.T) {
	m, r, out := testManager(t, nil)
	wd := m.Engine.Workdir
	writeState(t, wd, "01-image-typo", "02-net-policy")

	info, err := m.Start(context.Background(), StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	name := m.Engine.EnvName()
	for _, want := range []string{
		"tmux new-session -d -s " + name,
		"tmux pipe-pane -o -t " + name + " cat >> " + filepath.Join(wd, RawLogFile),
		"start asciinema rec --overwrite --command tmux new-session -A -s " + name + " " + filepath.Join(wd, CastFile),
		"start ttyd -p 8001 -W tmux attach -t " + name,
		"start ttyd -p 8002 tmux attach -r -t " + name,
	} {
		if len(r.callsMatching(want)) != 1 {
			t.Errorf("no call %q in %v", want, r.calls)
		}
	}

	hex32 := regexp.MustCompile(`^[0-9a-f]{32}$`)
	if !hex32.MatchString(info.CandidateToken) || !hex32.MatchString(info.ObserverToken) {
		t.Errorf("tokens not 32-hex: %q %q", info.CandidateToken, info.ObserverToken)
	}
	if info.CandidateToken == info.ObserverToken {
		t.Error("candidate and observer share a token")
	}
	if info.CandidateURL != "http://127.0.0.1:8001" || info.ObserverURL != "http://127.0.0.1:8002" {
		t.Errorf("local urls = %q %q", info.CandidateURL, info.ObserverURL)
	}

	raw, err := os.ReadFile(filepath.Join(wd, InfoFile))
	if err != nil {
		t.Fatal(err)
	}
	var onDisk Info
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatal(err)
	}
	if onDisk.TmuxSession != name || onDisk.Problem != "pipeline-meltdown" ||
		onDisk.Seed != "test-seed" || onDisk.CandidatePort != 8001 || onDisk.ObserverPort != 8002 ||
		onDisk.CandidateToken != info.CandidateToken || onDisk.StartedAt.IsZero() {
		t.Errorf("session.json = %+v", onDisk)
	}

	for _, pidFile := range []string{"asciinema.pid", "ttyd-candidate.pid", "ttyd-observer.pid"} {
		raw, err := os.ReadFile(filepath.Join(wd, "pids", pidFile))
		if err != nil || !strings.HasPrefix(string(raw), "4000") {
			t.Errorf("pidfile %s = %q, %v", pidFile, raw, err)
		}
	}
	if !strings.Contains(out.String(), info.CandidateURL) || !strings.Contains(out.String(), info.ObserverURL) {
		t.Errorf("urls not printed: %q", out.String())
	}
}

func TestStartWithBaseURLAndTokens(t *testing.T) {
	m, _, _ := testManager(t, nil)
	writeState(t, m.Engine.Workdir, "01-image-typo")

	info, err := m.Start(context.Background(), StartOptions{
		BaseURL:        "https://1.2.3.4.sslip.io/",
		CandidateToken: strings.Repeat("a", 32),
		ObserverToken:  strings.Repeat("b", 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	if info.CandidateURL != "https://1.2.3.4.sslip.io/c/"+strings.Repeat("a", 32) {
		t.Errorf("candidate url = %q", info.CandidateURL)
	}
	if info.ObserverURL != "https://1.2.3.4.sslip.io/o/"+strings.Repeat("b", 32) {
		t.Errorf("observer url = %q", info.ObserverURL)
	}
}

func TestStartRequiresState(t *testing.T) {
	m, r, _ := testManager(t, nil)
	_, err := m.Start(context.Background(), StartOptions{})
	if err == nil || !strings.Contains(err.Error(), "env up") {
		t.Errorf("Start without state = %v", err)
	}
	if len(r.calls) != 0 {
		t.Errorf("processes spawned without state: %v", r.calls)
	}
}

func TestStartToleratesMissingRecorder(t *testing.T) {
	m, r, out := testManager(t, nil)
	writeState(t, m.Engine.Workdir, "01-image-typo")
	r.failStart["asciinema"] = errors.New("asciinema: executable file not found")

	if _, err := m.Start(context.Background(), StartOptions{}); err != nil {
		t.Fatalf("Start must survive a missing recorder: %v", err)
	}
	if !strings.Contains(out.String(), "recording unavailable") {
		t.Errorf("no warning printed: %q", out.String())
	}
	if _, err := os.Stat(filepath.Join(m.Engine.Workdir, "pids", "asciinema.pid")); !os.IsNotExist(err) {
		t.Error("pidfile written for a process that never started")
	}
}

func TestStopKillsProcessesAndBundles(t *testing.T) {
	m, r, _ := testManager(t, nil)
	wd := m.Engine.Workdir
	writeState(t, wd, "01-image-typo")
	if _, err := m.Start(context.Background(), StartOptions{}); err != nil {
		t.Fatal(err)
	}

	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := len(r.callsMatching("kill 4000")); got != 3 {
		t.Errorf("kill calls = %d, want 3 (%v)", got, r.callsMatching("kill"))
	}
	if len(r.callsMatching("tmux kill-session -t "+m.Engine.EnvName())) != 1 {
		t.Errorf("no tmux kill-session in %v", r.calls)
	}
	if entries, err := os.ReadDir(filepath.Join(wd, "pids")); err != nil || len(entries) != 0 {
		t.Errorf("pidfiles left behind: %v, %v", entries, err)
	}
	if _, err := os.Stat(filepath.Join(wd, EvidenceFile)); err != nil {
		t.Errorf("no final evidence bundle: %v", err)
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

const kubeconfigFixture = `apiVersion: v1
clusters:
  - name: kind-iv
    cluster:
      server: https://127.0.0.1:6443
      certificate-authority-data: Y2EtZGF0YQ==
`

func TestKubeconfigAppliesRBACAndWritesConfig(t *testing.T) {
	m, r, _ := testManager(t, nil)
	wd := m.Engine.Workdir
	writeState(t, wd, "01-image-typo")
	if err := os.WriteFile(m.Engine.KubeconfigPath(), []byte(kubeconfigFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	r.outputs["create token candidate"] = "sa-token-123\n"

	path, err := m.Kubeconfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(wd, KubeconfigFile) {
		t.Errorf("path = %q", path)
	}

	kc := m.Engine.KubeconfigPath()
	rbacPath := filepath.Join(wd, RBACFile)
	for _, want := range []string{
		"kubectl --kubeconfig " + kc + " apply -f " + rbacPath,
		"kubectl --kubeconfig " + kc + " -n shop create token candidate --duration 4h",
	} {
		if len(r.callsMatching(want)) != 1 {
			t.Errorf("no call %q in %v", want, r.calls)
		}
	}

	rbac, err := os.ReadFile(rbacPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"kind: ServiceAccount", "namespace: shop",
		"resources: [pods, pods/log, services, endpoints, configmaps, secrets, events]",
		"resources: [deployments, statefulsets]",
		"resources: [pods/exec, pods/portforward]",
		"resources: [nodes]", "kind: ClusterRoleBinding",
	} {
		if !strings.Contains(string(rbac), want) {
			t.Errorf("rbac manifest missing %q", want)
		}
	}

	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"server: https://127.0.0.1:6443",
		"certificate-authority-data: Y2EtZGF0YQ==",
		"token: sa-token-123",
		"namespace: shop",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("candidate kubeconfig missing %q:\n%s", want, out)
		}
	}
}

func TestKubeconfigNeedsKind(t *testing.T) {
	m, _, _ := testManager(t, func(m fstest.MapFS) {
		m["problem.yaml"] = &fstest.MapFile{Data: []byte(strings.Replace(
			problemYAML, "flavor: kubernetes", "flavor: compose-linux", 1))}
		m["env.yaml"] = &fstest.MapFile{Data: []byte("provider: compose\ncompose:\n  file: env/docker-compose.yml\nverify: env/verify.sh\n")}
		m["env/docker-compose.yml"] = &fstest.MapFile{Data: []byte("services: {}")}
		delete(m, "env/manifests/00-ns.yaml")
	})
	writeState(t, m.Engine.Workdir, "01-image-typo")
	if _, err := m.Kubeconfig(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "kind-flavor") {
		t.Errorf("Kubeconfig on compose = %v", err)
	}
}

func TestNewTokenFormat(t *testing.T) {
	a, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(a) {
		t.Errorf("token = %q", a)
	}
	if a == b {
		t.Error("tokens repeat")
	}
}
