package session

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
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
		"start ttyd -i 127.0.0.1 -p 8001 -W tmux attach -t " + name,
		"start ttyd -i 127.0.0.1 -p 8002 tmux attach -r -t " + name,
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

	// The local path is one account: nothing switches user and no explicit
	// socket appears, so an interviewer's own machine behaves as before.
	for _, c := range r.calls {
		if strings.Contains(c, "sudo") || strings.Contains(c, " -S ") {
			t.Errorf("local start used the multi-account path: %q", c)
		}
	}
	if onDisk.TmuxSocket != "" || onDisk.CandidateUser != "" {
		t.Errorf("local session.json records a candidate account: %+v", onDisk)
	}
}

func TestStartAsCandidateUser(t *testing.T) {
	m, r, _ := testManager(t, nil)
	wd := m.Engine.Workdir
	writeState(t, wd, "01-image-typo")
	socket := filepath.Join(t.TempDir(), "candidate.sock")
	kube := filepath.Join(t.TempDir(), "candidate.kubeconfig")
	if err := os.WriteFile(kube, []byte("apiVersion: v1\n"), 0o640); err != nil {
		t.Fatal(err)
	}

	info, err := m.Start(context.Background(), StartOptions{
		CandidateUser: "candidate", TmuxSocket: socket, CandidateKubeconfig: kube,
	})
	if err != nil {
		t.Fatal(err)
	}
	me, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	name := m.Engine.EnvName()
	for _, want := range []string{
		// The server, and only the server, runs as the candidate.
		"sudo -n -u candidate tmux -S " + socket + " new-session -d -s " + name + " -e KUBECONFIG=" + kube,
		"sudo -n -u candidate tmux -S " + socket + " run-shell chmod 0660 " + socket,
		"sudo -n -u candidate tmux -S " + socket + " server-access -a -w " + me.Username,
		"start asciinema rec --overwrite --command tmux -S " + socket +
			" new-session -A -s " + name + " " + filepath.Join(wd, CastFile),
		"start ttyd -i 127.0.0.1 -p 8001 -W tmux -S " + socket + " attach -t " + name,
		"start ttyd -i 127.0.0.1 -p 8002 tmux -S " + socket + " attach -r -t " + name,
	} {
		if len(r.callsMatching(want)) != 1 {
			t.Errorf("no call %q in %v", want, r.calls)
		}
	}

	// Everything that produces evidence or serves a terminal stays with the
	// account that started the session; only the tmux server changes hands.
	for _, c := range r.callsMatching("sudo") {
		if !strings.Contains(c, "tmux -S "+socket) {
			t.Errorf("call runs as another account for no reason: %q", c)
		}
	}
	for _, c := range append(r.callsMatching("ttyd"), r.callsMatching("asciinema")...) {
		if strings.Contains(c, "sudo") {
			t.Errorf("%q must run as the observer account", c)
		}
	}
	// pipe-pane runs inside the candidate's server, so it cannot be trusted
	// with the transcript and is not used here.
	if got := r.callsMatching("pipe-pane"); len(got) != 0 {
		t.Errorf("pipe-pane used with a candidate-owned server: %v", got)
	}
	if _, err := os.Stat(filepath.Join(wd, RawLogFile)); !os.IsNotExist(err) {
		t.Errorf("raw log present: %v", err)
	}

	if info.TmuxSocket != socket || info.CandidateUser != "candidate" {
		t.Errorf("session.json = %+v", info)
	}
	onDisk, err := LoadInfo(wd)
	if err != nil {
		t.Fatal(err)
	}
	if onDisk.TmuxSocket != socket || onDisk.CandidateUser != "candidate" {
		t.Errorf("loaded session.json = %+v", onDisk)
	}
}

func TestStartRejectsCandidateUserWithoutSocket(t *testing.T) {
	m, r, _ := testManager(t, nil)
	writeState(t, m.Engine.Workdir, "01-image-typo")
	_, err := m.Start(context.Background(), StartOptions{CandidateUser: "candidate"})
	if err == nil || !strings.Contains(err.Error(), "tmux socket") {
		t.Errorf("start without a socket = %v", err)
	}
	if len(r.calls) != 0 {
		t.Errorf("processes spawned: %v", r.calls)
	}
}

// Locally a missing recorder is a warning; with a candidate-owned server
// the cast is the only evidence they cannot rewrite, so it is fatal.
func TestStartRequiresRecorderForCandidateUser(t *testing.T) {
	m, r, _ := testManager(t, nil)
	writeState(t, m.Engine.Workdir, "01-image-typo")
	r.failStart["asciinema"] = errors.New("asciinema: executable file not found")

	_, err := m.Start(context.Background(), StartOptions{
		CandidateUser: "candidate", TmuxSocket: filepath.Join(t.TempDir(), "s"),
	})
	if err == nil || !strings.Contains(err.Error(), "recording") {
		t.Errorf("start with no recorder = %v", err)
	}
	if got := r.callsMatching("ttyd"); len(got) != 0 {
		t.Errorf("terminals served without a recording: %v", got)
	}
}

func TestStartWarnsOnMissingKubeconfig(t *testing.T) {
	m, r, out := testManager(t, nil)
	writeState(t, m.Engine.Workdir, "01-image-typo")
	missing := filepath.Join(t.TempDir(), "candidate.kubeconfig")

	if _, err := m.Start(context.Background(), StartOptions{CandidateKubeconfig: missing}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no kubeconfig at "+missing) {
		t.Errorf("no warning printed: %q", out.String())
	}
	// Exported even though it is not there. Leaving KUBECONFIG unset hands
	// the pane whatever cluster the invoking shell points at, which on an
	// interviewer's laptop is a real one.
	if got := r.callsMatching("KUBECONFIG=" + missing); len(got) != 1 {
		t.Errorf("kubeconfig not pinned, so the pane inherits one: %v", r.callsMatching("new-session"))
	}
}

// A local pane opens wherever the command ran, which is a checkout of this
// repository: the first ls lists a directory per fault in the pack.
func TestLocalStartOpensThePaneAwayFromTheCheckout(t *testing.T) {
	m, r, out := testManager(t, nil)
	writeState(t, m.Engine.Workdir, "01-image-typo")

	if _, err := m.Start(context.Background(), StartOptions{}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(m.Engine.Workdir, CandidateDir)
	if got := r.callsMatching("-c " + dir); len(got) != 1 {
		t.Errorf("pane cwd not set: %v", r.callsMatching("new-session"))
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("pane cwd does not exist: %v", err)
	}
	// Local mode cannot keep the candidate off this account, so it has to say
	// so rather than read as an interview-ready setup.
	if !strings.Contains(out.String(), "shell on this account") {
		t.Errorf("local mode did not state what it exposes: %q", out.String())
	}
}

// Unset, the pane inherits the interviewer's kubectl context. The scoped
// candidate kubeconfig exists for exactly this, so local mode mints it
// instead of making it a second command nobody knows to run.
func TestLocalStartMintsTheScopedKubeconfig(t *testing.T) {
	m, r, _ := testManager(t, nil)
	writeState(t, m.Engine.Workdir, "01-image-typo")
	if err := os.WriteFile(m.Engine.KubeconfigPath(), []byte(kubeconfigFixture), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := m.Start(context.Background(), StartOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := r.callsMatching("create token candidate"); len(got) != 1 {
		t.Errorf("no candidate token minted: %v", got)
	}
	want := filepath.Join(m.Engine.Workdir, KubeconfigFile)
	if got := r.callsMatching("KUBECONFIG=" + want); len(got) != 1 {
		t.Errorf("pane did not get the scoped kubeconfig: %v", r.callsMatching("new-session"))
	}
	if got := r.callsMatching("KUBECONFIG=" + m.Engine.KubeconfigPath()); len(got) != 0 {
		t.Error("pane got the admin kubeconfig")
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

func TestStartRefusesASecondLiveSession(t *testing.T) {
	m, r, _ := testManager(t, nil)
	wd := m.Engine.Workdir
	writeState(t, wd, "01-image-typo")
	first, err := m.Start(context.Background(), StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	before := r.starts

	_, err = m.Start(context.Background(), StartOptions{})
	if err == nil {
		t.Fatal("second Start succeeded, duplicating the session stack")
	}
	for _, want := range []string{"already running", "pipeline-meltdown", "test-seed", "session stop"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
	if r.starts != before {
		t.Errorf("starts = %d, want %d: a second generation was spawned", r.starts, before)
	}
	// The first generation's record survives, so stop can still find it.
	onDisk, err := LoadInfo(wd)
	if err != nil {
		t.Fatal(err)
	}
	if onDisk.CandidateToken != first.CandidateToken {
		t.Error("session.json was overwritten by the refused start")
	}
	for _, name := range []string{"asciinema", "ttyd-candidate", "ttyd-observer"} {
		raw, err := os.ReadFile(filepath.Join(wd, "pids", name+".pid"))
		if err != nil {
			t.Fatalf("pidfile %s: %v", name, err)
		}
		if pid := strings.TrimSpace(string(raw)); !r.Alive(atoi(t, pid)) {
			t.Errorf("pidfile %s = %s, not the live first generation", name, pid)
		}
	}
}

func TestStartAfterStopStartsAgain(t *testing.T) {
	m, r, _ := testManager(t, nil)
	writeState(t, m.Engine.Workdir, "01-image-typo")
	ctx := context.Background()
	if _, err := m.Start(ctx, StartOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(ctx, StartOptions{}); err != nil {
		t.Fatalf("Start after Stop = %v", err)
	}
	if r.starts != 6 {
		t.Errorf("starts = %d, want 6", r.starts)
	}
}

func TestStartRefusesWhenAPortIsTaken(t *testing.T) {
	m, r, _ := testManager(t, nil)
	writeState(t, m.Engine.Workdir, "01-image-typo")
	// Another session on this host already holds the observer port.
	r.listening[ObserverPort] = 999

	_, err := m.Start(context.Background(), StartOptions{})
	if err == nil {
		t.Fatal("Start succeeded onto a port it cannot bind")
	}
	for _, want := range []string{"8002", "observer", "one session at a time"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
	if len(r.calls) != 0 {
		t.Errorf("processes spawned before the port check: %v", r.calls)
	}
	if _, err := os.Stat(filepath.Join(m.Engine.Workdir, InfoFile)); !os.IsNotExist(err) {
		t.Error("session.json written for a session that never started")
	}
}

func TestStartFailsWhenAListenerDiesAtOnce(t *testing.T) {
	m, r, _ := testManager(t, nil)
	wd := m.Engine.Workdir
	writeState(t, wd, "01-image-typo")
	r.dieAtOnce["-p 8002"] = true

	_, err := m.Start(context.Background(), StartOptions{})
	if err == nil {
		t.Fatal("Start reported success with a dead observer listener")
	}
	for _, want := range []string{"observer", "8002", "exited immediately"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
	if _, err := os.Stat(filepath.Join(wd, InfoFile)); !os.IsNotExist(err) {
		t.Error("session.json written for a session that never came up")
	}
	// Whatever did start is gone: no orphans, no leftover pidfiles. Only
	// the two live processes are signalled; the dead listener is not.
	if got := len(r.callsMatching("kill 4000")); got != 2 {
		t.Errorf("kill calls = %d, want 2 (%v)", got, r.callsMatching("kill"))
	}
	if len(r.callsMatching("tmux kill-session -t "+m.Engine.EnvName())) != 1 {
		t.Errorf("no tmux kill-session in %v", r.calls)
	}
	if entries, err := os.ReadDir(filepath.Join(wd, pidsDir)); err != nil || len(entries) != 0 {
		t.Errorf("pidfiles left behind: %v, %v", entries, err)
	}
	if len(r.listening) != 0 {
		t.Errorf("ports still held: %v", r.listening)
	}
}

func TestStartFailsWhenAListenerNeverAnswers(t *testing.T) {
	m, r, _ := testManager(t, nil)
	writeState(t, m.Engine.Workdir, "01-image-typo")
	// Alive but never accepting: a listener wedged before its bind.
	m.dial = func(int) error { return errors.New("connection refused") }

	_, err := m.Start(context.Background(), StartOptions{})
	if err == nil || !strings.Contains(err.Error(), "did not accept a connection") {
		t.Fatalf("Start with a silent listener = %v", err)
	}
	if !strings.Contains(err.Error(), "candidate") {
		t.Errorf("error %q does not name the failing listener", err)
	}
	if got := len(r.callsMatching("kill 4000")); got != 3 {
		t.Errorf("kill calls = %d, want 3 (%v)", got, r.callsMatching("kill"))
	}
}

func TestStartWarnsWhenTheRecorderDiesAtOnce(t *testing.T) {
	m, r, out := testManager(t, nil)
	wd := m.Engine.Workdir
	writeState(t, wd, "01-image-typo")
	r.dieAtOnce["asciinema"] = true

	if _, err := m.Start(context.Background(), StartOptions{}); err != nil {
		t.Fatalf("a dead recorder must not fail the session: %v", err)
	}
	if !strings.Contains(out.String(), "recording unavailable") {
		t.Errorf("no warning printed: %q", out.String())
	}
	if _, err := os.Stat(filepath.Join(wd, "pids", "asciinema.pid")); !os.IsNotExist(err) {
		t.Error("pidfile left for a recorder that already exited")
	}
}

// TestDialPortAnswersRealSockets checks the manager's own dial against a
// real listener, since every other test replaces it with a fake.
func TestDialPortAnswersRealSockets(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := dialPort(port); err != nil {
		t.Errorf("dialPort on a live listener = %v", err)
	}
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	if err := dialPort(port); err == nil {
		t.Error("dialPort on a closed port reported success")
	}
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatal(err)
	}
	return n
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
	// The directory goes with the pidfiles: teardown lists what is left in
	// the workdir, and scratch space there reads as evidence.
	if _, err := os.Stat(filepath.Join(wd, pidsDir)); !os.IsNotExist(err) {
		t.Errorf("pid directory left behind: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wd, EvidenceFile)); err != nil {
		t.Errorf("no final evidence bundle: %v", err)
	}
}

// Stop is its own process invocation, so it recovers the shared socket from
// session.json rather than needing the flags start was given.
func TestStopFindsTheSharedSocket(t *testing.T) {
	m, r, _ := testManager(t, nil)
	writeState(t, m.Engine.Workdir, "01-image-typo")
	socket := filepath.Join(t.TempDir(), "candidate.sock")
	if _, err := m.Start(context.Background(), StartOptions{
		CandidateUser: "candidate", TmuxSocket: socket,
	}); err != nil {
		t.Fatal(err)
	}

	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := "tmux -S " + socket + " kill-session -t " + m.Engine.EnvName()
	if len(r.callsMatching(want)) != 1 {
		t.Errorf("no call %q in %v", want, r.calls)
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

	path, err := m.Kubeconfig(context.Background(), KubeconfigOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(wd, KubeconfigFile) {
		t.Errorf("path = %q", path)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("workdir kubeconfig mode = %v, %v", info.Mode().Perm(), err)
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

// The host hands the candidate's account a copy outside the workdir, which
// stays unreadable to it, and the copy must not be writable there either.
func TestKubeconfigWritesASecondCopy(t *testing.T) {
	m, _, _ := testManager(t, nil)
	wd := m.Engine.Workdir
	writeState(t, wd, "01-image-typo")
	if err := os.WriteFile(m.Engine.KubeconfigPath(), []byte(kubeconfigFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "candidate.kubeconfig")

	path, err := m.Kubeconfig(context.Background(), KubeconfigOptions{Out: out, Mode: 0o640})
	if err != nil {
		t.Fatal(err)
	}
	if path != out {
		t.Errorf("path = %q, want the handed-out copy %q", path, out)
	}
	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Errorf("copy mode = %v, want 0640: group read, nobody else, no writers", info.Mode().Perm())
	}
	copied, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	inWorkdir, err := os.ReadFile(filepath.Join(wd, KubeconfigFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(copied) != string(inWorkdir) {
		t.Error("the copy and the bundled kubeconfig differ")
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
	if _, err := m.Kubeconfig(context.Background(), KubeconfigOptions{}); err == nil ||
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

// Stop is the command that stops a session, so it is the one that has to
// say so. The line belongs nowhere else: a start that failed and rolled
// itself back claiming to have stopped a session and taken its evidence
// describes something that never ran.
func TestStopReportsAndFailedStartDoesNot(t *testing.T) {
	m, _, out := testManager(t, nil)
	writeState(t, m.Engine.Workdir, "01-image-typo")
	if _, err := m.Start(context.Background(), StartOptions{}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "stopped") {
		t.Errorf("stop said nothing about stopping: %q", out.String())
	}
	// Scratch, not evidence: teardown lists whatever is left in the workdir.
	if _, err := os.Stat(filepath.Join(m.Engine.Workdir, pidsDir)); !os.IsNotExist(err) {
		t.Errorf("pid directory survived stop: %v", err)
	}

	m2, _, out2 := testManager(t, nil)
	writeState(t, m2.Engine.Workdir, "01-image-typo")
	r2 := m2.Engine.Runner.(*fakeRunner)
	r2.failStart["ttyd"] = errors.New("no ttyd here")
	if _, err := m2.Start(context.Background(), StartOptions{}); err == nil {
		t.Fatal("start with no ttyd succeeded")
	}
	if strings.Contains(out2.String(), "stopped") {
		t.Errorf("a failed start claims it stopped a session: %q", out2.String())
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

// appFixture adds an app declaration to the kind test scenario.
func appFixture(m fstest.MapFS) {
	m["env.yaml"] = &fstest.MapFile{Data: []byte(
		"provider: kind\nkind:\n  manifests: env/manifests\n  namespace: shop\nverify: env/verify.sh\napp:\n  service: storefront\n  port: \"80\"\n  path: /shop\n")}
}

// A candidate debugging a frontend fault should be able to look at the
// frontend. The forwarder normalizes any service port onto one loopback
// port, which is what the fronting proxy and the printed URL both need.
func TestStartServesTheAppOnKind(t *testing.T) {
	m, r, out := testManager(t, appFixture)
	writeState(t, m.Engine.Workdir, "01-image-typo")

	info, err := m.Start(context.Background(), StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if info.AppPort != AppPort {
		t.Errorf("app port = %d, want %d", info.AppPort, AppPort)
	}
	if want := fmt.Sprintf("http://127.0.0.1:%d/shop", AppPort); info.AppURL != want {
		t.Errorf("app url = %q, want %q", info.AppURL, want)
	}
	forwards := r.callsMatching("port-forward")
	if len(forwards) != 1 {
		t.Fatalf("port-forward calls = %v, want one", forwards)
	}
	for _, want := range []string{
		"svc/storefront " + strconv.Itoa(AppPort) + ":80",
		"-n \"shop\"",
		"--address 127.0.0.1",
		// The app is down for much of an interview by design and kubectl
		// port-forward exits with its pod, so it has to come back by itself.
		"while :;",
	} {
		if !strings.Contains(forwards[0], want) {
			t.Errorf("forwarder %q missing %q", forwards[0], want)
		}
	}
	if !strings.Contains(out.String(), "app:") {
		t.Errorf("start did not print the app URL: %q", out.String())
	}

	// The URL survives in the record, since it is printed once.
	saved, err := LoadInfo(m.Engine.Workdir)
	if err != nil {
		t.Fatal(err)
	}
	if saved.AppURL != info.AppURL || saved.AppToken == "" {
		t.Errorf("session.json lost the app route: %+v", saved)
	}
}

// On a host the app is a token route like the terminals, so a scan of the
// hostname does not find it.
func TestAppURLIsATokenRouteBehindAProxy(t *testing.T) {
	m, _, _ := testManager(t, appFixture)
	writeState(t, m.Engine.Workdir, "01-image-typo")

	info, err := m.Start(context.Background(), StartOptions{
		BaseURL: "https://1.2.3.4.sslip.io/", AppToken: strings.Repeat("a", 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://1.2.3.4.sslip.io/a/" + strings.Repeat("a", 32) + "/shop"; info.AppURL != want {
		t.Errorf("app url = %q, want %q", info.AppURL, want)
	}
}

// A problem with nothing worth opening in a browser gets no route and no
// URL, rather than one that never answers.
func TestNoAppMeansNoRoute(t *testing.T) {
	m, r, out := testManager(t, nil)
	writeState(t, m.Engine.Workdir, "01-image-typo")

	info, err := m.Start(context.Background(), StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if info.AppURL != "" || info.AppPort != 0 || info.AppToken != "" {
		t.Errorf("app route invented for a problem without one: %+v", info)
	}
	if got := r.callsMatching("port-forward"); len(got) != 0 {
		t.Errorf("forwarded anyway: %v", got)
	}
	if strings.Contains(out.String(), "app:") {
		t.Errorf("printed an app URL: %q", out.String())
	}
}

// A forwarder that will not start is worth saying and not worth failing
// for: the interview is the terminal.
func TestStartSurvivesAForwarderThatWillNotStart(t *testing.T) {
	m, r, out := testManager(t, appFixture)
	writeState(t, m.Engine.Workdir, "01-image-typo")
	r.failStart["port-forward"] = errors.New("no kubectl here")

	info, err := m.Start(context.Background(), StartOptions{})
	if err != nil {
		t.Fatalf("start failed over the app route: %v", err)
	}
	if info.AppURL != "" {
		t.Errorf("kept an app URL nothing serves: %q", info.AppURL)
	}
	if !strings.Contains(out.String(), "app route unavailable") {
		t.Errorf("said nothing about the missing route: %q", out.String())
	}
	if got := r.callsMatching("ttyd"); len(got) == 0 {
		t.Error("the terminals did not come up")
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

// The candidate kubeconfig is a credential for a cluster about to be
// destroyed, and teardown lists whatever is left in the workdir as evidence
// worth keeping.
func TestStopRemovesTheCandidateKubeconfig(t *testing.T) {
	m, _, _ := testManager(t, nil)
	writeState(t, m.Engine.Workdir, "01-image-typo")
	kube := filepath.Join(m.Engine.Workdir, KubeconfigFile)
	if err := os.WriteFile(kube, []byte("token: sa-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(context.Background(), StartOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(kube); !os.IsNotExist(err) {
		t.Errorf("credential survived the session: %v", err)
	}
}

// A compose app publishes whatever port its variant drew, so it needs
// forwarding onto the one port the fronting proxy and the printed URL name.
// kubectl cannot reach a compose service and there is no dependency to lean
// on, so this binary does it.
func TestComposeAppGetsAProxy(t *testing.T) {
	m, r, _ := testManager(t, func(f fstest.MapFS) {
		f["problem.yaml"] = &fstest.MapFile{Data: []byte(
			strings.Replace(problemYAML, "flavor: kubernetes", "flavor: compose-linux", 1))}
		f["env.yaml"] = &fstest.MapFile{Data: []byte(
			"provider: compose\ncompose:\n  file: env/docker-compose.yml\nverify: env/verify.sh\napp:\n  port: \"8480\"\n")}
		f["env/docker-compose.yml"] = &fstest.MapFile{Data: []byte("services: {}\n")}
	})
	writeState(t, m.Engine.Workdir, "01-image-typo")

	info, err := m.Start(context.Background(), StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if info.AppPort != AppPort {
		t.Errorf("app port = %d, want the fixed %d", info.AppPort, AppPort)
	}
	proxies := r.callsMatching("session proxy")
	if len(proxies) != 1 {
		t.Fatalf("proxy calls = %v, want one", proxies)
	}
	for _, want := range []string{
		fmt.Sprintf("--from %d --to 8480", AppPort),
		// exec, so the pid recorded is the proxy and stop kills it rather
		// than a shell that outlives it.
		"exec ",
	} {
		if !strings.Contains(proxies[0], want) {
			t.Errorf("proxy command %q missing %q", proxies[0], want)
		}
	}
	if got := r.callsMatching("port-forward"); len(got) != 0 {
		t.Errorf("used kubectl for a compose app: %v", got)
	}
}
