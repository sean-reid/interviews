// Shared fixtures for this package's tests: the fake runner everything
// drives, and the scenario fixtures several files build on.
package session

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/sean-reid/interviews/internal/content"
	"github.com/sean-reid/interviews/internal/debug"
	"github.com/sean-reid/interviews/internal/taxonomy"
	"github.com/sean-reid/interviews/internal/variant"
)

// fakeRunner records every call. Session logic never touches tmux, ttyd,
// kubectl, or aws in tests. Started processes model a real one closely
// enough to matter: one that dies is not alive and its port stops
// answering, and a live port cannot be bound twice.
type fakeRunner struct {
	calls     []string
	starts    int
	outputs   map[string]string // substring of the command line -> stdout
	failCmd   map[string]error  // substring -> Command error
	failStart map[string]error  // substring -> Start error
	dieAtOnce map[string]bool   // substring -> Start succeeds, process exits
	scriptErr map[string]error  // parent/base -> Script error
	dead      map[int]bool
	listening map[int]int // port -> pid holding it
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{
		outputs: map[string]string{}, failCmd: map[string]error{},
		failStart: map[string]error{}, dieAtOnce: map[string]bool{},
		scriptErr: map[string]error{}, dead: map[int]bool{}, listening: map[int]int{},
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
	if err := match(r.failCmd, line); err != nil {
		return err
	}
	if name == "kill" && len(args) > 0 {
		r.exit(args[len(args)-1])
	}
	return nil
}

// exit models a killed process: gone, and its port with it.
func (r *fakeRunner) exit(pidArg string) {
	pid, err := strconv.Atoi(pidArg)
	if err != nil {
		return
	}
	r.dead[pid] = true
	for port, holder := range r.listening {
		if holder == pid {
			delete(r.listening, port)
		}
	}
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
	pid := 40000 + r.starts
	for sub, die := range r.dieAtOnce {
		if die && strings.Contains(line, sub) {
			r.dead[pid] = true
			return pid, nil
		}
	}
	// A listener whose port is taken cannot bind it and exits at once,
	// which is exactly what a real ttyd does.
	if port, ok := portFlag(args); ok {
		if _, taken := r.listening[port]; taken {
			r.dead[pid] = true
		} else {
			r.listening[port] = pid
		}
	}
	return pid, nil
}

func (r *fakeRunner) Alive(pid int) bool { return pid > 0 && !r.dead[pid] }

// dialPort stands in for the manager's real loopback dial.
func (r *fakeRunner) dialPort(port int) error {
	if _, ok := r.listening[port]; ok {
		return nil
	}
	return errors.New("connect: connection refused")
}

func portFlag(args []string) (int, bool) {
	for i, a := range args {
		if a == "-p" && i+1 < len(args) {
			port, err := strconv.Atoi(args[i+1])
			return port, err == nil
		}
	}
	return 0, false
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
	m.dial = r.dialPort
	m.listenerWait, m.listenerPoll = 50*time.Millisecond, time.Millisecond
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

const kubeconfigFixture = `apiVersion: v1
clusters:
  - name: kind-iv
    cluster:
      server: https://127.0.0.1:6443
      certificate-authority-data: Y2EtZGF0YQ==
`

// appFixture adds an app declaration to the kind test scenario.
func appFixture(m fstest.MapFS) {
	m["env.yaml"] = &fstest.MapFile{Data: []byte(
		"provider: kind\nkind:\n  manifests: env/manifests\n  namespace: shop\nverify: env/verify.sh\napp:\n  service: storefront\n  port: \"80\"\n  path: /shop\n")}
}
