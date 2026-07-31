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
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sean-reid/interviews/internal/debug"
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
// URL tokens; locally the URLs point straight at them.
const (
	CandidatePort = 8001
	ObserverPort  = 8002
)

// Info is the on-disk record of a live session, workdir/session.json.
type Info struct {
	Problem        string    `json:"problem"`
	Seed           string    `json:"interview_id"`
	TmuxSession    string    `json:"tmux_session"`
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
}

// NewManager wraps an engine for session operations.
func NewManager(e *debug.Engine, out io.Writer) *Manager {
	return &Manager{Engine: e, Out: out, now: time.Now}
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
type StartOptions struct {
	BaseURL        string
	CandidateToken string
	ObserverToken  string
}

// Start brings up the live layer over an already broken environment: a
// detached tmux session with raw pane logging, an asciinema recording of
// it, and the two ttyd endpoints. It writes session.json and prints the
// candidate and observer URLs.
func (m *Manager) Start(ctx context.Context, opts StartOptions) (*Info, error) {
	st, err := debug.LoadState(m.Engine.Workdir)
	if err != nil {
		return nil, fmt.Errorf("no environment state in %s; run interviews env up and interviews break first: %w", m.Engine.Workdir, err)
	}

	info := &Info{
		Problem: st.Problem, Seed: st.Seed,
		TmuxSession:   m.Engine.EnvName(),
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
	name := info.TmuxSession
	if err := r.Command(ctx, "tmux", "new-session", "-d", "-s", name); err != nil {
		return nil, err
	}
	rawLog := filepath.Join(m.Engine.Workdir, RawLogFile)
	if err := r.Command(ctx, "tmux", "pipe-pane", "-o", "-t", name, "cat >> "+rawLog); err != nil {
		return nil, err
	}

	// The recording is evidence, not the session itself: warn and keep
	// going when asciinema is not installed (a local dry run).
	cast := filepath.Join(m.Engine.Workdir, CastFile)
	err = m.startProcess(ctx, "asciinema", "asciinema",
		"rec", "--overwrite", "--command", "tmux new-session -A -s "+name, cast)
	if err != nil {
		fmt.Fprintf(m.Out, "warning: recording unavailable: %v\n", err)
	}

	if err := m.startProcess(ctx, "ttyd-candidate", "ttyd",
		"-p", strconv.Itoa(CandidatePort), "-W", "tmux", "attach", "-t", name); err != nil {
		return nil, err
	}
	if err := m.startProcess(ctx, "ttyd-observer", "ttyd",
		"-p", strconv.Itoa(ObserverPort), "tmux", "attach", "-r", "-t", name); err != nil {
		return nil, err
	}

	raw, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(m.Engine.Workdir, InfoFile), raw, 0o600); err != nil {
		return nil, err
	}
	fmt.Fprintf(m.Out, "candidate: %s\nobserver:  %s\n", info.CandidateURL, info.ObserverURL)
	return info, nil
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

// startProcess launches a background process through the Runner and
// records its pid under workdir/pids/.
func (m *Manager) startProcess(ctx context.Context, pidName, bin string, args ...string) error {
	pid, err := m.Engine.Runner.Start(ctx, bin, args...)
	if err != nil {
		return err
	}
	dir := filepath.Join(m.Engine.Workdir, pidsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, pidName+".pid"), []byte(strconv.Itoa(pid)+"\n"), 0o644)
}

// Stop kills every recorded background process and the tmux session,
// then takes a final evidence bundle. Kill failures are reported, not
// fatal: the processes may already be gone.
func (m *Manager) Stop(ctx context.Context) error {
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
	if err := m.Engine.Runner.Command(ctx, "tmux", "kill-session", "-t", m.Engine.EnvName()); err != nil {
		fmt.Fprintf(m.Out, "tmux kill-session: %v\n", err)
	}
	return m.Evidence(ctx, EvidenceOptions{Final: true})
}
