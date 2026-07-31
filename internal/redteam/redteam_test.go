package redteam

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/sean-reid/interviews/internal/content"
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
		got := Judge(tt.fixed, tt.total, tt.verified)
		if got != tt.want {
			t.Errorf("Judge(%d, %d, %v) = %q, want %q", tt.fixed, tt.total, tt.verified, got, tt.want)
		}
	}
}

func TestJudgeThreshold(t *testing.T) {
	// Exactly at the threshold counts as too easy; just under holds.
	if got := Judge(6, 10, false); got != TooEasy {
		t.Errorf("6/10 = %q, want too-easy", got)
	}
	if got := Judge(5, 10, false); got != Holds {
		t.Errorf("5/10 = %q, want holds", got)
	}
}

func TestLedgerRoundTrip(t *testing.T) {
	root := t.TempDir()
	if entries, err := Load(root); err != nil || entries != nil {
		t.Fatalf("Load on empty root = %v, %v", entries, err)
	}

	base := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	for _, e := range []Entry{
		{Problem: "orbit-shop", Type: "debugging", Seed: "cal-1", Driver: "claude",
			At: base, Fixed: 2, Total: 7, Verdict: Holds},
		{Problem: "orbit-shop", Type: "debugging", Seed: "cal-2", Driver: "claude",
			At: base.Add(time.Hour), Fixed: 5, Total: 7, Verdict: TooEasy},
		{Problem: "relay", Type: "debugging", Seed: "cal-1", Driver: "api",
			At: base, Fixed: 1, Total: 6, Verdict: Holds},
	} {
		if err := Append(root, e); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(entries))
	}
	if _, err := os.Stat(filepath.Join(root, LedgerPath)); err != nil {
		t.Errorf("ledger not at %s: %v", LedgerPath, err)
	}

	latest := Latest(entries)
	if got := latest["orbit-shop"]; got.Seed != "cal-2" || got.Verdict != TooEasy {
		t.Errorf("latest orbit-shop = %+v", got)
	}
	if got := latest["relay"]; got.Verdict != Holds {
		t.Errorf("latest relay = %+v", got)
	}

	stale := Stale(entries)
	if len(stale) != 1 || stale[0].Problem != "orbit-shop" {
		t.Errorf("Stale = %+v, want just orbit-shop", stale)
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
