package redteam

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/sean-reid/interviews/internal/content"
	"github.com/sean-reid/interviews/internal/debug"
	"github.com/sean-reid/interviews/internal/taxonomy"
	"github.com/sean-reid/interviews/internal/variant"
)

func TestNewDriver(t *testing.T) {
	for _, name := range []string{"", "claude", "api"} {
		d, err := NewDriver(name, "")
		if err != nil || d == nil {
			t.Errorf("NewDriver(%q) = %v, %v", name, d, err)
		}
	}
	if _, err := NewDriver("gpt", ""); err == nil {
		t.Error("unknown driver accepted")
	}
	// The CLI driver is the default because it needs no API key.
	d, _ := NewDriver("", "")
	if d.Name() != "claude" {
		t.Errorf("default driver = %q, want claude", d.Name())
	}
}

func TestJudge(t *testing.T) {
	tests := []struct {
		fixed, total int
		verified     bool
		want         Verdict
	}{
		{0, 7, false, Holds},
		{2, 7, false, Holds},
		{4, 7, false, Holds},   // 0.57, just under the threshold
		{5, 7, false, TooEasy}, // 0.71
		{1, 7, true, TooEasy},  // healthy app end to end, however few faults
		{0, 0, false, Inconclusive},
	}
	for _, tt := range tests {
		got := Judge(Entry{Fixed: tt.fixed, Total: tt.total, Verified: tt.verified})
		if got != tt.want {
			t.Errorf("Judge(%d, %d, %v) = %q, want %q", tt.fixed, tt.total, tt.verified, got, tt.want)
		}
	}
}

func TestJudgeThreshold(t *testing.T) {
	// Exactly at the threshold counts as too easy; just under holds.
	if got := Judge(Entry{Fixed: 6, Total: 10}); got != TooEasy {
		t.Errorf("6/10 = %q, want too-easy", got)
	}
	if got := Judge(Entry{Fixed: 5, Total: 10}); got != Holds {
		t.Errorf("5/10 = %q, want holds", got)
	}
}

func TestLedgerRoundTrip(t *testing.T) {
	root := t.TempDir()
	if entries, err := Load(root); err != nil || entries != nil {
		t.Fatalf("Load on empty root = %v, %v", entries, err)
	}

	base := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	// Several problems land stale, appended out of id order, so the Stale
	// assertion below fails on any map-iteration order.
	for _, e := range []Entry{
		{Problem: "orbit-shop", Type: "debugging", Seed: "cal-1", Driver: "claude",
			At: base, Fixed: 2, Total: 7, Verdict: Holds},
		{Problem: "orbit-shop", Type: "debugging", Seed: "cal-2", Pack: "pack-b", Driver: "claude",
			At: base.Add(time.Hour), Fixed: 5, Total: 7, Verdict: TooEasy},
		{Problem: "relay", Type: "debugging", Seed: "cal-1", Driver: "api",
			At: base, Fixed: 1, Total: 6, Verdict: Holds},
		{Problem: "warp-drive", Type: "debugging", Seed: "cal-1", Driver: "api",
			At: base, Fixed: 6, Total: 7, Verdict: TooEasy},
		{Problem: "aligner", Type: "debugging", Seed: "cal-1", Driver: "api",
			At: base, Fixed: 5, Total: 6, Verdict: TooEasy},
		{Problem: "beacon", Type: "debugging", Seed: "cal-1", Driver: "api",
			At: base, Fixed: 4, Total: 6, Verdict: TooEasy},
	} {
		if err := Append(root, e); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 6 {
		t.Fatalf("entries = %d, want 6", len(entries))
	}
	if _, err := os.Stat(filepath.Join(root, LedgerPath)); err != nil {
		t.Errorf("ledger not at %s: %v", LedgerPath, err)
	}

	latest := Latest(entries)
	// The pack is on the entry, not folded into the seed: every pack has its
	// own difficulty and the ledger only means something per pack.
	if got := latest["orbit-shop"]; got.Seed != "cal-2" || got.Pack != "pack-b" || got.Verdict != TooEasy {
		t.Errorf("latest orbit-shop = %+v", got)
	}
	if got := latest["relay"]; got.Verdict != Holds {
		t.Errorf("latest relay = %+v", got)
	}

	var stale []string
	for _, e := range Stale(entries) {
		stale = append(stale, e.Problem)
	}
	want := []string{"aligner", "beacon", "orbit-shop", "warp-drive"}
	if !slices.Equal(stale, want) {
		t.Errorf("Stale = %v, want %v in that order", stale, want)
	}
}

// A problem whose latest run holds must not stay flagged from an older run.
func TestStaleUsesLatestVerdictOnly(t *testing.T) {
	base := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	entries := []Entry{
		{Problem: "orbit-shop", At: base, Verdict: TooEasy},
		{Problem: "orbit-shop", At: base.Add(time.Hour), Verdict: Holds},
	}
	if got := Stale(entries); len(got) != 0 {
		t.Errorf("Stale = %+v, want none after a holding rerun", got)
	}
}

func TestLoadRejectsCorruptLedger(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, LedgerPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{\"problem\":\"a\"}\nnot json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Errorf("Load = %v, want an error naming line 2", err)
	}
}

func TestShare(t *testing.T) {
	if got := (Entry{Fixed: 3, Total: 6}).Share(); got != 0.5 {
		t.Errorf("Share = %v, want 0.5", got)
	}
	if got := (Entry{}).Share(); got != 0 {
		t.Errorf("Share on empty entry = %v, want 0", got)
	}
}

func problemFor(t *testing.T, brief string) (*content.Problem, *variant.Resolved) {
	t.Helper()
	fsys := fstest.MapFS{
		"problem.yaml": &fstest.MapFile{Data: []byte(`schema: 1
id: orbit-shop
type: debugging
title: T
summary: S
disciplines: [infra]
levels: [mid]
flavor: kubernetes
time:
  session_minutes: 60
params:
  fault_pack:
    type: choice
    of: [pack-a]
  team_name:
    type: string
    default: orbit
visibility:
  candidate: [candidate/**]
`)},
		"candidate/brief.md":    &fstest.MapFile{Data: []byte(brief)},
		"interviewer/keys.md":   &fstest.MapFile{Data: []byte("ANSWER KEY: the image tag is wrong")},
		"faults/01-x/inject.sh": &fstest.MapFile{Data: []byte("#!/bin/sh")},
	}
	p, issues := content.Load(fsys, taxonomy.Debugging, "orbit-shop")
	if content.Errors(issues) {
		t.Fatalf("fixture invalid: %v", issues)
	}
	v, err := variant.Resolve("orbit-shop", p.Manifest.Params, "cal-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	return p, v
}

func TestCandidatePromptRendersBriefOnly(t *testing.T) {
	p, v := problemFor(t, "The {{.team_name}} shop is down. Fix what you can.")
	prompt, err := candidatePrompt(p, v, debugEnvNotes("/tmp/kubeconfig", "shop-orbit"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"The orbit shop is down. Fix what you can.",
		"kubeconfig at /tmp/kubeconfig",
		"scoped to the shop-orbit namespace",
		"decide and act on your own judgement",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

// The prompt is the one place interviewer-only text could reach an agent.
func TestCandidatePromptCarriesNoAnswerKey(t *testing.T) {
	p, v := problemFor(t, "The shop is down.")
	prompt, err := candidatePrompt(p, v, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"ANSWER KEY", "image tag is wrong", "faults/", "interviewer/", "inject.sh"} {
		if strings.Contains(prompt, forbidden) {
			t.Errorf("prompt leaked %q:\n%s", forbidden, prompt)
		}
	}
}

func TestCandidatePromptFailsOnUnrenderableBrief(t *testing.T) {
	p, v := problemFor(t, "Scale is {{.not_a_param}}.")
	if _, err := candidatePrompt(p, v, ""); err == nil {
		t.Error("brief referencing an undeclared param rendered, want error")
	}
}

func TestDebugEnvNotesWithoutKubeconfig(t *testing.T) {
	notes := debugEnvNotes("", "")
	if !strings.Contains(notes, "running on this machine") {
		t.Errorf("notes = %q", notes)
	}
}

// fakeRunner stands in for docker, kind, and kubectl. Calibration logic
// never touches a cluster in tests.
type fakeRunner struct{ calls []string }

func (r *fakeRunner) Command(_ context.Context, name string, args ...string) error {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	return nil
}

func (r *fakeRunner) Output(_ context.Context, name string, args ...string) (string, error) {
	line := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, line)
	if strings.Contains(line, "create token candidate") {
		return "scoped-sa-token\n", nil
	}
	return "", nil
}

func (r *fakeRunner) Script(_ context.Context, path, _ string, _ map[string]string) error {
	r.calls = append(r.calls, "script "+filepath.Base(filepath.Dir(path))+"/"+filepath.Base(path))
	return nil
}

func (r *fakeRunner) Start(_ context.Context, name string, args ...string) (int, error) {
	r.calls = append(r.calls, "start "+name+" "+strings.Join(args, " "))
	return 4242, nil
}

func (r *fakeRunner) Alive(pid int) bool { return pid > 0 }

// fakeDriver records the task and reads what the agent could have read,
// while it can: the scratch directory is gone by the time the run returns.
type fakeDriver struct {
	task       Task
	visible    []string
	kubeconfig string
}

func (d *fakeDriver) Name() string                      { return "fake" }
func (d *fakeDriver) Available(_ context.Context) error { return nil }

func (d *fakeDriver) Run(_ context.Context, t Task) (*Attempt, error) {
	d.task = t
	entries, err := os.ReadDir(t.Dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		d.visible = append(d.visible, e.Name())
	}
	if path := t.Env["KUBECONFIG"]; path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		d.kubeconfig = string(raw)
	}
	return &Attempt{Driver: d.Name(), Turns: 3}, nil
}

// adminKubeconfig is the shape kind exports for the engine: a cluster-admin
// client certificate, which is exactly what an agent must not be handed.
const adminKubeconfig = `apiVersion: v1
clusters:
  - name: kind-iv
    cluster:
      server: https://127.0.0.1:6443
      certificate-authority-data: Y2EtZGF0YQ==
users:
  - name: kind-admin
    user:
      client-certificate-data: YWRtaW4tY2VydA==
`

// calibrationEngine is a kind-flavor engine over a fake runner, with the
// kubeconfig kind would have exported already in place.
func calibrationEngine(t *testing.T) (*debug.Engine, *fakeRunner) {
	t.Helper()
	execFile := &fstest.MapFile{Data: []byte("#!/bin/sh\n"), Mode: 0o755}
	fsys := fstest.MapFS{
		"problem.yaml": &fstest.MapFile{Data: []byte(`schema: 1
id: orbit-shop
type: debugging
title: T
summary: S
disciplines: [infra]
levels: [mid]
flavor: kubernetes
time:
  session_minutes: 60
params:
  fault_pack:
    type: choice
    of: [pack-a]
visibility:
  candidate: [candidate/**]
`)},
		"candidate/brief.md":              &fstest.MapFile{Data: []byte("The shop is down.")},
		"interviewer/keys.md":             &fstest.MapFile{Data: []byte("ANSWER KEY")},
		"env.yaml":                        &fstest.MapFile{Data: []byte("provider: kind\nkind:\n  manifests: env/manifests\n  namespace: shop\nverify: env/verify.sh\n")},
		"env/verify.sh":                   execFile,
		"env/manifests/00-ns.yaml":        &fstest.MapFile{Data: []byte("kind: Namespace")},
		"faults/01-image-typo/fault.yaml": &fstest.MapFile{Data: []byte("id: 01-image-typo\ntitle: Image tag typo\ntier: easy\npacks: [pack-a]\n")},
		"faults/01-image-typo/inject.sh":  execFile,
		"faults/01-image-typo/check.sh":   execFile,
		"faults/01-image-typo/fix.sh":     execFile,
		"faults/01-image-typo/notes.md":   &fstest.MapFile{Data: []byte("notes")},
	}
	p, issues := content.Load(fsys, taxonomy.Debugging, "orbit-shop")
	if content.Errors(issues) {
		t.Fatalf("fixture invalid: %v", issues)
	}
	s, issues := debug.LoadScenario(p)
	if len(issues) != 0 {
		t.Fatalf("scenario invalid: %v", issues)
	}
	v, err := variant.Resolve("orbit-shop", p.Manifest.Params, "calibrate-pack-a", nil)
	if err != nil {
		t.Fatal(err)
	}
	r := &fakeRunner{}
	e, err := debug.NewEngine("/problems/orbit-shop", s, v, r, io.Discard, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e.Settle, e.PollInterval, e.FixTimeout, e.VerifyTimeout = 0, 0, 0, 0
	if err := os.WriteFile(e.KubeconfigPath(), []byte(adminKubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	return e, r
}

// A verdict from a run that held cluster-admin, or that could read which
// faults were injected, says nothing about the problem's difficulty.
func TestDebugRunScopesTheAgentsAccess(t *testing.T) {
	e, _ := calibrationEngine(t)
	d := &fakeDriver{}
	t.Setenv("KUBECONFIG", "/interviewer/own/kubeconfig")

	entry, err := DebugRun(context.Background(), e, d, io.Discard, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Total != 1 || entry.Turns != 3 {
		t.Errorf("entry = %+v", entry)
	}

	kubeconfig := d.task.Env["KUBECONFIG"]
	if kubeconfig != filepath.Join(d.task.Dir, "kubeconfig") {
		t.Fatalf("agent KUBECONFIG = %q, want one inside its own directory %q", kubeconfig, d.task.Dir)
	}
	if !strings.Contains(d.kubeconfig, "token: scoped-sa-token") {
		t.Errorf("agent kubeconfig is not the scoped one:\n%s", d.kubeconfig)
	}
	for _, forbidden := range []string{"client-certificate-data", "YWRtaW4tY2VydA=="} {
		if strings.Contains(d.kubeconfig, forbidden) {
			t.Errorf("agent kubeconfig carries cluster-admin credentials:\n%s", d.kubeconfig)
		}
	}
	if !strings.Contains(d.kubeconfig, "namespace: shop") {
		t.Errorf("agent kubeconfig is not namespaced:\n%s", d.kubeconfig)
	}

	// Nothing the agent is handed leads to the workdir, where the injected
	// fault list lives, and its own directory does not hold one.
	if strings.Contains(d.task.Prompt, e.Workdir) || strings.Contains(kubeconfig, e.Workdir) {
		t.Errorf("the agent was handed a path into %s", e.Workdir)
	}
	for _, name := range d.visible {
		if name == debug.StateFile {
			t.Errorf("the fault list sat in the agent's working directory: %v", d.visible)
		}
	}

	// Calibration goes on to check and fix with its own credentials, so it
	// must not have redirected them.
	if got := os.Getenv("KUBECONFIG"); got != "/interviewer/own/kubeconfig" {
		t.Errorf("KUBECONFIG left as %q", got)
	}
}

func TestAPIDriverAvailabilityNeedsKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	d := &apiDriver{}
	if err := d.Available(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Errorf("Available without a key = %v", err)
	}
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	if err := d.Available(context.Background()); err != nil {
		t.Errorf("Available with a key = %v", err)
	}
}
