package debug

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sean-reid/interviews/internal/variant"
)

// Engine drives one scenario for one resolved variant: environment
// lifecycle, fault injection, checks, fixes, and the prove cycle.
type Engine struct {
	Scenario *Scenario
	Variant  *variant.Resolved
	Runner   Runner
	Out      io.Writer
	// Dir is the problem directory on disk; scripts execute from here.
	Dir string
	// Workdir holds rendered files and session state.
	Workdir string

	// Timing knobs, defaulted by NewEngine; tests zero them.
	Settle        time.Duration // grace after inject before the broken check
	PollInterval  time.Duration
	FixTimeout    time.Duration
	VerifyTimeout time.Duration

	provider Provider
}

// State is the on-disk record of a running environment, workdir/state.json.
type State struct {
	Problem   string            `json:"problem"`
	Seed      string            `json:"interview_id"`
	Overrides map[string]string `json:"overrides,omitempty"`
	Pack      string            `json:"pack"`
	Injected  []string          `json:"injected"`
	Provider  string            `json:"provider"`
	CreatedAt time.Time         `json:"created_at"`
}

// FaultStatus is one injected fault's current check result.
type FaultStatus struct {
	ID    string
	Title string
	Tier  Tier
	Fixed bool
}

// NewEngine builds an engine for one problem+variant. workdir "" derives
// the default cache location from the problem and seed.
func NewEngine(problemDir string, s *Scenario, v *variant.Resolved, r Runner, out io.Writer, workdir string) (*Engine, error) {
	if workdir == "" {
		var err error
		if workdir, err = DefaultWorkdir(v); err != nil {
			return nil, err
		}
	}
	e := &Engine{
		Scenario: s, Variant: v, Runner: r, Out: out,
		Dir: problemDir, Workdir: workdir,
		Settle:        3 * time.Second,
		PollInterval:  2 * time.Second,
		FixTimeout:    90 * time.Second,
		VerifyTimeout: 240 * time.Second,
	}
	p, err := e.newProvider()
	if err != nil {
		return nil, err
	}
	e.provider = p
	return e, nil
}

// DefaultWorkdir is where a variant's session state lives unless a caller
// overrides it; grading reads scores and hints from the same place.
func DefaultWorkdir(v *variant.Resolved) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, "interviews", envName(v)), nil
}

// envName is the stable per-problem-per-interview environment name, safe
// for cluster and compose-project identifiers.
func envName(v *variant.Resolved) string {
	problem := v.Problem
	if len(problem) > 20 {
		problem = problem[:20]
	}
	sum := sha256.Sum256([]byte(v.Problem + "\x00" + v.InterviewID))
	return "iv-" + problem + "-" + hex.EncodeToString(sum[:4])
}

// Pack returns the variant's fault pack.
func (e *Engine) Pack() (string, error) {
	pack, ok := e.Variant.Params[PackParam].(string)
	if !ok {
		return "", fmt.Errorf("variant has no %s parameter", PackParam)
	}
	return pack, nil
}

// scriptEnv is the environment every scenario script runs with.
func (e *Engine) scriptEnv(extra map[string]string) map[string]string {
	env := map[string]string{
		"IV_PROBLEM": e.Variant.Problem,
		"IV_SEED":    e.Variant.InterviewID,
		"IV_ENV":     envName(e.Variant),
		"IV_WORKDIR": e.Workdir,
	}
	for name, val := range e.Variant.Params {
		env["IV_PARAM_"+strings.ToUpper(name)] = fmt.Sprint(val)
	}
	e.provider.env(env)
	for k, v := range extra {
		env[k] = v
	}
	return env
}

func (e *Engine) script(ctx context.Context, path string, extra map[string]string) error {
	return e.Runner.Script(ctx, filepath.Join(e.Dir, path), e.Dir, e.scriptEnv(extra))
}

// renderVariant is the variant plus engine builtins. Rendered env files can
// reference {{._dir}} (the problem directory on disk, for absolute build
// contexts in compose files) and {{._workdir}}. Builtins are engine-side
// only: candidate-visible text never renders with them, and the template
// lint rejects them there.
func (e *Engine) renderVariant() *variant.Resolved {
	params := make(map[string]any, len(e.Variant.Params)+2)
	for k, v := range e.Variant.Params {
		params[k] = v
	}
	params["_dir"] = e.Dir
	params["_workdir"] = e.Workdir
	return &variant.Resolved{
		Problem:     e.Variant.Problem,
		InterviewID: e.Variant.InterviewID,
		Params:      params,
	}
}

// render writes every file under src (in the problem FS) to the workdir,
// substituting the resolved variant. Returns the rendered directory.
func (e *Engine) render(src string) (string, error) {
	dst := filepath.Join(e.Workdir, "rendered", src)
	fsys := e.Scenario.Problem.FS
	err := fs.WalkDir(fsys, src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(e.Workdir, "rendered", p), 0o755)
		}
		raw, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		rendered, err := variant.Render(string(raw), e.renderVariant())
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		out := filepath.Join(e.Workdir, "rendered", p)
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		return os.WriteFile(out, []byte(rendered), info.Mode().Perm()|0o600)
	})
	if err != nil {
		return "", err
	}
	return dst, nil
}

// RenderString substitutes the variant into one spec string (namespace,
// project names).
func (e *Engine) RenderString(s string) (string, error) {
	return variant.Render(s, e.renderVariant())
}

func (e *Engine) logf(format string, args ...any) {
	fmt.Fprintf(e.Out, format+"\n", args...)
}

// Up creates the environment, deploys the healthy app, and waits until
// verify passes.
func (e *Engine) Up(ctx context.Context) error {
	if err := e.provider.Detect(ctx); err != nil {
		return err
	}
	if err := os.MkdirAll(e.Workdir, 0o755); err != nil {
		return err
	}
	e.logf("environment %s: starting (%s)", envName(e.Variant), e.provider.Name())
	if err := e.provider.Up(ctx); err != nil {
		return err
	}
	if err := e.VerifyWait(ctx); err != nil {
		return err
	}
	pack, err := e.Pack()
	if err != nil {
		return err
	}
	e.logf("environment healthy")
	return e.saveState(&State{
		Problem: e.Variant.Problem, Seed: e.Variant.InterviewID,
		Overrides: e.Variant.Overrides, Pack: pack,
		Injected: []string{}, Provider: e.provider.Name(), CreatedAt: time.Now(),
	})
}

// Verify runs the scenario's verify script once.
func (e *Engine) Verify(ctx context.Context) error {
	return e.script(ctx, e.Scenario.Env.Verify, nil)
}

// VerifyWait polls verify until it passes or the timeout elapses.
func (e *Engine) VerifyWait(ctx context.Context) error {
	deadline := time.Now().Add(e.VerifyTimeout)
	for {
		err := e.Verify(ctx)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("verify did not pass within %s: %w", e.VerifyTimeout, err)
		}
		if err := e.sleep(ctx, e.PollInterval); err != nil {
			return err
		}
	}
}

// Down tears the environment down and removes the workdir.
func (e *Engine) Down(ctx context.Context) error {
	if err := e.provider.Down(ctx); err != nil {
		return err
	}
	return os.RemoveAll(e.Workdir)
}

// Break injects the variant's fault pack in lexical fault-id order.
func (e *Engine) Break(ctx context.Context) error {
	st, err := e.loadState()
	if err != nil {
		return fmt.Errorf("no environment state; run env up first: %w", err)
	}
	faults := e.Scenario.PackFaults(st.Pack)
	for _, f := range faults {
		e.logf("inject %s (%s)", f.Spec.ID, f.Spec.Tier)
		if err := e.script(ctx, f.Script("inject.sh"), nil); err != nil {
			return fmt.Errorf("inject %s: %w", f.Spec.ID, err)
		}
		st.Injected = append(st.Injected, f.Spec.ID)
	}
	return e.saveState(st)
}

// Status checks every injected fault.
func (e *Engine) Status(ctx context.Context) ([]FaultStatus, error) {
	st, err := e.loadState()
	if err != nil {
		return nil, fmt.Errorf("no environment state; run env up first: %w", err)
	}
	var out []FaultStatus
	for _, id := range st.Injected {
		f, ok := e.Scenario.Fault(id)
		if !ok {
			return nil, fmt.Errorf("state names unknown fault %q", id)
		}
		fixed := e.script(ctx, f.Script("check.sh"), nil) == nil
		out = append(out, FaultStatus{ID: id, Title: f.Spec.Title, Tier: f.Spec.Tier, Fixed: fixed})
	}
	return out, nil
}

// Fix applies the answer key for one injected fault, or all of them when
// id is empty. Fixing everything sets IV_FIX_FAST=1 so fixes skip their
// individual convergence waits; the caller verifies once at the end.
func (e *Engine) Fix(ctx context.Context, id string) error {
	st, err := e.loadState()
	if err != nil {
		return fmt.Errorf("no environment state; run env up first: %w", err)
	}
	ids := st.Injected
	var extra map[string]string
	if id != "" {
		ids = []string{id}
	} else if len(ids) > 1 {
		extra = map[string]string{"IV_FIX_FAST": "1"}
	}
	for _, fid := range ids {
		f, ok := e.Scenario.Fault(fid)
		if !ok {
			return fmt.Errorf("unknown fault %q", fid)
		}
		e.logf("fix %s", fid)
		if err := e.script(ctx, f.Script("fix.sh"), extra); err != nil {
			return fmt.Errorf("fix %s: %w", fid, err)
		}
	}
	return nil
}

func (e *Engine) sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

func (e *Engine) statePath() string { return filepath.Join(e.Workdir, "state.json") }

func (e *Engine) saveState(st *State) error {
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(e.statePath(), raw, 0o644)
}

func (e *Engine) loadState() (*State, error) {
	raw, err := os.ReadFile(e.statePath())
	if err != nil {
		return nil, err
	}
	var st State
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, err
	}
	return &st, nil
}
