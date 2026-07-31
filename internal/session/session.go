// Package session runs the live layer over a debugging environment: a
// shared tmux session recorded from the start, exposed writable to the
// candidate and read-only to the observer through ttyd, with evidence
// bundling and a fault timeline for grading afterwards.
package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sean-reid/interviews/internal/debug"
	"github.com/sean-reid/interviews/internal/fileio"
)

// Files the session stack writes into the workdir.
const (
	InfoFile       = "session.json"
	CastFile       = "session.cast"
	RawLogFile     = "terminal.raw"
	TimelineFile   = "timeline.jsonl"
	EvidenceFile   = "evidence.tar.gz"
	ScoreErrorFile = "score-error.txt"
	KubeconfigFile = "candidate.kubeconfig"
	RBACFile       = "candidate-rbac.yaml"
	pidsDir        = "pids"
)

// Local ttyd ports. On a real host Caddy fronts both with TLS and secret
// URL tokens; locally the URLs point straight at them. The rendered
// Caddyfile proxies these by number, so a host runs one session at a
// time and Start refuses when either port is already taken.
const (
	CandidatePort = 8001
	ObserverPort  = 8002
)

// Info is the on-disk record of a live session, workdir/session.json.
type Info struct {
	Problem        string    `json:"problem"`
	Seed           string    `json:"interview_id"`
	TmuxSession    string    `json:"tmux_session"`
	TmuxSocket     string    `json:"tmux_socket,omitempty"`
	CandidateUser  string    `json:"candidate_user,omitempty"`
	CandidateToken string    `json:"candidate_token"`
	ObserverToken  string    `json:"observer_token"`
	CandidatePort  int       `json:"candidate_port"`
	ObserverPort   int       `json:"observer_port"`
	CandidateURL   string    `json:"candidate_url"`
	ObserverURL    string    `json:"observer_url"`
	StartedAt      time.Time `json:"started_at"`
}

// Manager drives the live session for one engine. All process control
// goes through the engine's Runner, so everything tests against a fake.
type Manager struct {
	Engine *debug.Engine
	Out    io.Writer
	now    func() time.Time
	// dial reports whether a loopback port accepts a connection.
	dial func(port int) error
	// Bound on waiting for a ttyd listener to answer; tests shorten both.
	listenerWait time.Duration
	listenerPoll time.Duration
}

// NewManager wraps an engine for session operations.
func NewManager(e *debug.Engine, out io.Writer) *Manager {
	return &Manager{
		Engine: e, Out: out, now: time.Now, dial: dialPort,
		listenerWait: 5 * time.Second, listenerPoll: 100 * time.Millisecond,
	}
}

// dialPort connects to a loopback port and hangs up.
func dialPort(port int) error {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if err != nil {
		return err
	}
	return conn.Close()
}

// NewToken returns a 32-hex-character secret URL token.
func NewToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// StartOptions tune session start. Empty tokens are generated; hosts
// provisioned by terraform pass theirs in so the fronting Caddyfile and
// the terraform outputs agree with the running session.
//
// The last three are the two-account host setup and are empty locally,
// where the interviewer is the only account on the machine.
type StartOptions struct {
	BaseURL        string
	CandidateToken string
	ObserverToken  string
	// CandidateUser owns the tmux server, which is what makes every pane
	// that account's shell: a tmux server runs commands as its owner, so
	// nothing the candidate types in the browser can run as the observer.
	CandidateUser string
	// TmuxSocket is an explicit server socket. Required with CandidateUser,
	// whose default per-account socket directory the observer cannot reach.
	TmuxSocket string
	// CandidateKubeconfig is exported into the session environment when the
	// file is there, which is how the candidate's shell gets kubectl.
	CandidateKubeconfig string
}

// tmuxCtl builds one tmux invocation. user runs it as another account,
// for the single case where ownership matters: creating the server.
type tmuxCtl struct {
	socket string
	user   string
}

func (t tmuxCtl) cmd(args ...string) (string, []string) {
	full := []string{}
	if t.socket != "" {
		full = append(full, "-S", t.socket)
	}
	full = append(full, args...)
	if t.user == "" {
		return "tmux", full
	}
	return "sudo", append([]string{"-n", "-u", t.user, "tmux"}, full...)
}

// line renders an invocation for the places that take a command as one
// string: a recorder's --command, a pipe-pane shell command.
func (t tmuxCtl) line(args ...string) string {
	bin, full := t.cmd(args...)
	return bin + " " + strings.Join(full, " ")
}

// listener is one of the two ttyd endpoints: its pidfile name, the label
// a failure names, its port, and the tmux client it serves.
type listener struct {
	pidName string
	label   string
	port    int
	args    []string
}

// Start brings up the live layer over an already broken environment: a
// detached tmux session, an asciinema recording of it, and the two ttyd
// endpoints. It writes session.json and prints the candidate and observer
// URLs. With a candidate user the tmux server belongs to that account and
// the recording replaces the raw pane log; without one, everything runs as
// the account that invoked it, which is the local case.
func (m *Manager) Start(ctx context.Context, opts StartOptions) (*Info, error) {
	if opts.CandidateUser != "" && opts.TmuxSocket == "" {
		return nil, errors.New("a candidate user needs an explicit tmux socket: the default socket directory is unreachable from another account")
	}
	st, err := debug.LoadState(m.Engine.Workdir)
	if err != nil {
		return nil, fmt.Errorf("no environment state in %s; run interviews env up and interviews break first: %w", m.Engine.Workdir, err)
	}
	if err := m.refuseIfRunning(st); err != nil {
		return nil, err
	}

	name := m.Engine.EnvName()
	// The server is the only thing that runs as the candidate. Both ttyd
	// clients stay with this process's account: a tmux client relays
	// keystrokes and executes nothing, so who owns it decides nothing.
	server := tmuxCtl{socket: opts.TmuxSocket, user: opts.CandidateUser}
	client := tmuxCtl{socket: opts.TmuxSocket}
	tmuxBin, attach := client.cmd("attach", "-t", name)
	_, observe := client.cmd("attach", "-r", "-t", name)
	listeners := []listener{
		{"ttyd-candidate", "candidate", CandidatePort, append([]string{"-W", tmuxBin}, attach...)},
		{"ttyd-observer", "observer", ObserverPort, append([]string{tmuxBin}, observe...)},
	}
	for _, l := range listeners {
		if m.dial(l.port) == nil {
			return nil, fmt.Errorf("%s listener cannot bind port %d: something already listens there; the fronting proxy routes these ports by number, so a host runs one session at a time and the other one has to stop first", l.label, l.port)
		}
	}

	info := &Info{
		Problem: st.Problem, Seed: st.Seed,
		TmuxSession: name,
		TmuxSocket:  opts.TmuxSocket, CandidateUser: opts.CandidateUser,
		CandidatePort: CandidatePort, ObserverPort: ObserverPort,
		CandidateToken: opts.CandidateToken, ObserverToken: opts.ObserverToken,
		StartedAt: m.now(),
	}
	if info.CandidateToken == "" {
		if info.CandidateToken, err = NewToken(); err != nil {
			return nil, err
		}
	}
	if info.ObserverToken == "" {
		if info.ObserverToken, err = NewToken(); err != nil {
			return nil, err
		}
	}
	info.CandidateURL, info.ObserverURL = urls(opts.BaseURL, info)

	r := m.Engine.Runner
	create := []string{"new-session", "-d", "-s", name}
	if kc := opts.CandidateKubeconfig; kc != "" {
		if _, err := os.Stat(kc); err == nil {
			create = append(create, "-e", "KUBECONFIG="+kc)
		} else {
			fmt.Fprintf(m.Out, "warning: no kubeconfig at %s: the session gets no kubectl access\n", kc)
		}
	}
	if err := m.runTmux(ctx, server, create...); err != nil {
		return nil, err
	}
	// Everything past the tmux session gets torn down on failure: a half
	// started stack with no session.json is unrecoverable, since nothing
	// records the pids that stop would have to kill.
	var started []proc
	fail := func(err error) (*Info, error) {
		m.cleanup(ctx, started, opts.TmuxSocket)
		return nil, err
	}

	if opts.CandidateUser != "" {
		if err := m.shareServer(ctx, server, opts.TmuxSocket); err != nil {
			return fail(err)
		}
	} else {
		// pipe-pane runs server-side, so with a candidate-owned server this
		// would be the candidate writing their own transcript, and stopping
		// it is one tmux command. There the cast below is the record.
		rawLog := filepath.Join(m.Engine.Workdir, RawLogFile)
		if err := m.runTmux(ctx, client, "pipe-pane", "-o", "-t", name, "cat >> "+rawLog); err != nil {
			return fail(err)
		}
	}

	// The recorder attaches as this process's account and writes into a
	// workdir the candidate cannot reach, so with a candidate-owned server
	// it is the one piece of evidence they can neither edit nor signal.
	// That makes it mandatory there, and evidence rather than the session
	// itself locally, where a missing asciinema is just a dry run.
	cast := filepath.Join(m.Engine.Workdir, CastFile)
	recorder, recErr := m.startProcess(ctx, "asciinema", "asciinema",
		"rec", "--overwrite", "--command", client.line("new-session", "-A", "-s", name), cast)
	if recorder.pid == 0 {
		if opts.CandidateUser != "" {
			return fail(fmt.Errorf("recording is the only evidence a candidate account cannot touch: %w", recErr))
		}
		fmt.Fprintf(m.Out, "warning: recording unavailable: %v\n", recErr)
	} else {
		started = append(started, recorder)
		if recErr != nil {
			return fail(recErr)
		}
	}

	// Both listeners bind loopback: Caddy is the only front door, and it is
	// what enforces the URL tokens. A ttyd on every interface is an
	// unauthenticated shell for anything that can route to the host.
	ttyds := make([]proc, len(listeners))
	for i, l := range listeners {
		args := append([]string{"-i", "127.0.0.1", "-p", strconv.Itoa(l.port)}, l.args...)
		p, err := m.startProcess(ctx, l.pidName, "ttyd", args...)
		if p.pid != 0 {
			started = append(started, p)
		}
		if err != nil {
			return fail(fmt.Errorf("%s listener: %w", l.label, err))
		}
		ttyds[i] = p
	}
	for i, l := range listeners {
		if err := m.waitListening(ctx, ttyds[i], l); err != nil {
			return fail(err)
		}
	}
	if recorder.pid != 0 && !r.Alive(recorder.pid) {
		if opts.CandidateUser != "" {
			return fail(errors.New("recording is the only evidence a candidate account cannot touch: asciinema exited immediately"))
		}
		fmt.Fprintln(m.Out, "warning: recording unavailable: asciinema exited immediately")
		started = slices.DeleteFunc(started, func(p proc) bool { return p.name == recorder.name })
		if err := m.removePid(recorder); err != nil {
			return fail(err)
		}
	}

	raw, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fail(err)
	}
	// The evidence timer reads this file while a session restart writes it.
	if err := fileio.WriteAtomic(filepath.Join(m.Engine.Workdir, InfoFile), raw, 0o600); err != nil {
		return fail(err)
	}
	fmt.Fprintf(m.Out, "candidate: %s\nobserver:  %s\n", info.CandidateURL, info.ObserverURL)
	return info, nil
}

// waitListening blocks until the listener answers on its port, giving up
// as soon as its process is gone. ttyd exits at once when it cannot bind,
// and calling that a live session hands the operator URLs that 502.
func (m *Manager) waitListening(ctx context.Context, p proc, l listener) error {
	deadline := time.Now().Add(m.listenerWait)
	for {
		if m.dial(l.port) == nil {
			return nil
		}
		if !m.Engine.Runner.Alive(p.pid) {
			return fmt.Errorf("%s listener (ttyd on port %d) exited immediately", l.label, l.port)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s listener (ttyd on port %d) did not accept a connection within %s", l.label, l.port, m.listenerWait)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(m.listenerPoll):
		}
	}
}

// refuseIfRunning rejects a second start over a live session. Starting
// again would spawn a second set of processes and overwrite the pidfiles,
// orphaning the first set with nothing left recording its pids, and the
// recording would split across two cast files. Adopting the running
// session instead would silently keep writing to the old cast.
func (m *Manager) refuseIfRunning(st *debug.State) error {
	live, err := m.livePids()
	if err != nil {
		return err
	}
	if len(live) == 0 {
		// Stale pidfiles from a dead session: drop them so a later stop
		// never signals whatever recycled those pids.
		return os.RemoveAll(filepath.Join(m.Engine.Workdir, pidsDir))
	}
	var alive []string
	for _, p := range live {
		alive = append(alive, fmt.Sprintf("%s pid %d", p.name, p.pid))
	}
	started := ""
	if info, err := LoadInfo(m.Engine.Workdir); err == nil {
		started = ", started " + info.StartedAt.Format(time.RFC3339)
	}
	return fmt.Errorf("session already running for %s seed %s%s (%s); run interviews session stop %s --seed %s first",
		st.Problem, st.Seed, started, strings.Join(alive, ", "), st.Problem, st.Seed)
}

// livePids reads the recorded pidfiles and returns those whose processes
// are still running, in pidfile-name order.
func (m *Manager) livePids() ([]proc, error) {
	entries, err := os.ReadDir(filepath.Join(m.Engine.Workdir, pidsDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var live []proc
	for _, ent := range entries {
		name, ok := strings.CutSuffix(ent.Name(), ".pid")
		if !ok {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(m.Engine.Workdir, pidsDir, ent.Name()))
		if err != nil {
			return nil, err
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
		if err != nil {
			continue
		}
		if m.Engine.Runner.Alive(pid) {
			live = append(live, proc{name: name, pid: pid})
		}
	}
	return live, nil
}

// LoadInfo reads a workdir's session record.
func LoadInfo(workdir string) (*Info, error) {
	raw, err := os.ReadFile(filepath.Join(workdir, InfoFile))
	if err != nil {
		return nil, err
	}
	var info Info
	if err := json.Unmarshal(raw, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// runTmux executes one tmux invocation through the engine's runner.
func (m *Manager) runTmux(ctx context.Context, t tmuxCtl, args ...string) error {
	bin, full := t.cmd(args...)
	return m.Engine.Runner.Command(ctx, bin, full...)
}

// shareServer opens a candidate-owned tmux server to this account, which
// the recorder and the observer endpoint both attach to. tmux creates its
// socket private and refuses clients from other accounts, and only the
// server's owner can relax either, so both steps run as that owner. The
// socket's group comes from its setgid directory.
func (m *Manager) shareServer(ctx context.Context, server tmuxCtl, socket string) error {
	if err := m.runTmux(ctx, server, "run-shell", "chmod 0660 "+socket); err != nil {
		return err
	}
	me, err := user.Current()
	if err != nil {
		return fmt.Errorf("resolving this account: %w", err)
	}
	return m.runTmux(ctx, server, "server-access", "-a", "-w", me.Username)
}

// cleanup stops what a failed Start already started. The process that
// caused the failure is usually the one that already exited, so signalling
// only what is still alive keeps the real error at the end of the output.
func (m *Manager) cleanup(ctx context.Context, started []proc, socket string) {
	for _, p := range started {
		if !m.Engine.Runner.Alive(p.pid) {
			if err := m.removePid(p); err != nil {
				fmt.Fprintf(m.Out, "%v\n", err)
			}
			continue
		}
		if err := m.Engine.Runner.Command(ctx, "kill", strconv.Itoa(p.pid)); err != nil {
			fmt.Fprintf(m.Out, "kill %d (%s): %v\n", p.pid, p.name, err)
		}
		if err := m.removePid(p); err != nil {
			fmt.Fprintf(m.Out, "%v\n", err)
		}
	}
	m.killSession(ctx, socket)
}

// killSession ends the tmux session on a socket. A shared server accepts
// this from the observer's account: the access list that let it attach also
// lets it shut the server down.
func (m *Manager) killSession(ctx context.Context, socket string) {
	if err := m.runTmux(ctx, tmuxCtl{socket: socket}, "kill-session", "-t", m.Engine.EnvName()); err != nil {
		fmt.Fprintf(m.Out, "tmux kill-session: %v\n", err)
	}
}

// urls builds the candidate and observer URLs: token paths under the
// fronting base URL, or the bare local ports without one.
func urls(baseURL string, info *Info) (candidate, observer string) {
	if baseURL == "" {
		return fmt.Sprintf("http://127.0.0.1:%d", info.CandidatePort),
			fmt.Sprintf("http://127.0.0.1:%d", info.ObserverPort)
	}
	base := strings.TrimRight(baseURL, "/")
	return base + "/c/" + info.CandidateToken, base + "/o/" + info.ObserverToken
}

// proc is a background process Start launched, tracked so a failure part
// way through can stop what already came up.
type proc struct {
	name string
	pid  int
}

// startProcess launches a background process through the Runner and
// records its pid under workdir/pids/.
func (m *Manager) startProcess(ctx context.Context, pidName, bin string, args ...string) (proc, error) {
	pid, err := m.Engine.Runner.Start(ctx, bin, args...)
	if err != nil {
		return proc{}, err
	}
	p := proc{name: pidName, pid: pid}
	dir := filepath.Join(m.Engine.Workdir, pidsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return p, err
	}
	return p, os.WriteFile(filepath.Join(dir, pidName+".pid"), []byte(strconv.Itoa(pid)+"\n"), 0o644)
}

// removePid drops a process's pidfile so nothing later signals its pid.
func (m *Manager) removePid(p proc) error {
	err := os.Remove(filepath.Join(m.Engine.Workdir, pidsDir, p.name+".pid"))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove pidfile for %s: %w", p.name, err)
	}
	return nil
}

// Stop kills every recorded background process and the tmux session,
// then takes a final evidence bundle. Kill failures are reported, not
// fatal: the processes may already be gone.
func (m *Manager) Stop(ctx context.Context) error {
	// A shared server lives on its own socket, recorded at start.
	socket := ""
	if info, err := LoadInfo(m.Engine.Workdir); err == nil {
		socket = info.TmuxSocket
	}
	dir := filepath.Join(m.Engine.Workdir, pidsDir)
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, ent := range entries {
		if !strings.HasSuffix(ent.Name(), ".pid") {
			continue
		}
		path := filepath.Join(dir, ent.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if pid := strings.TrimSpace(string(raw)); pid != "" {
			if err := m.Engine.Runner.Command(ctx, "kill", pid); err != nil {
				fmt.Fprintf(m.Out, "kill %s (%s): %v\n", pid, ent.Name(), err)
			}
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	m.killSession(ctx, socket)
	return m.Evidence(ctx, EvidenceOptions{Final: true})
}
