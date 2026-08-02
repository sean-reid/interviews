package debug

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/sean-reid/interviews/internal/fileio"
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
	// TornDownAt marks a state file kept only so grading can still read what
	// the environment was built from. The environment itself is gone.
	TornDownAt time.Time `json:"torn_down_at,omitzero"`
}

// Live reports whether the environment this state describes still exists.
func (s *State) Live() bool { return s.TornDownAt.IsZero() }

// CheckState is what one fault's check script reported.
type CheckState string

// Check states, from the fault check contract in spec.go.
const (
	// CheckFixed means the check passed: the fault is gone.
	CheckFixed CheckState = "fixed"
	// CheckBroken means the check failed: the fault is still present.
	CheckBroken CheckState = "broken"
	// CheckCannotRun means the check script could not run, so it reports
	// nothing about the fault.
	CheckCannotRun CheckState = "check-cannot-run"
)

// FaultStatus is one injected fault's current check result.
type FaultStatus struct {
	ID    string
	Title string
	Tier  Tier
	State CheckState
}

// Fixed reports whether the check saw the fault gone. A check that could
// not run is not a fixed fault and not a broken one.
func (s FaultStatus) Fixed() bool { return s.State == CheckFixed }

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

// EnvName is the stable environment name for this engine's variant; the
// session stack names its tmux session after it.
func (e *Engine) EnvName() string { return envName(e.Variant) }

// ProviderName reports which provider hosts this scenario.
func (e *Engine) ProviderName() string { return e.provider.Name() }

// KubeconfigPath is the engine-owned kubeconfig for kind environments,
// exported per cluster so concurrent environments and the interviewer's
// own kubectl context never fight over current-context.
func (e *Engine) KubeconfigPath() string { return filepath.Join(e.Workdir, "kubeconfig") }

// KindNamespace is the rendered scenario namespace on kind problems.
func (e *Engine) KindNamespace() (string, error) {
	if e.Scenario.Env.Kind == nil {
		return "", fmt.Errorf("%s has no kind environment", e.Variant.Problem)
	}
	return e.RenderString(e.Scenario.Env.Kind.Namespace)
}

// envName is the stable per-variant environment name, safe for cluster and
// compose-project identifiers. The resolved parameters are part of it, in
// sorted order: two parameter sets on one interview id are two different
// environments, and a shared name would have them share a cluster, a
// workdir, and a state file.
func envName(v *variant.Resolved) string {
	problem := v.Problem
	if len(problem) > 20 {
		problem = problem[:20]
	}
	parts := []string{v.Problem, v.InterviewID}
	for _, name := range slices.Sorted(maps.Keys(v.Params)) {
		parts = append(parts, name, fmt.Sprint(v.Params[name]))
	}
	h := sha256.New()
	for _, s := range parts {
		// Length-prefix each part so no two part lists share a digest.
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(s)))
		h.Write(n[:])
		h.Write([]byte(s))
	}
	return "iv-" + problem + "-" + hex.EncodeToString(h.Sum(nil)[:4])
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
	// Bringing an environment up again is the natural response to a verify
	// timeout, and it must not erase the record of faults that are still in
	// there: a fault the app survives leaves verify passing.
	injected := []string{}
	if prev, err := e.loadState(); err == nil && prev.Live() && prev.Pack == pack {
		injected = prev.Injected
	}
	return e.saveState(&State{
		Problem: e.Variant.Problem, Seed: e.Variant.InterviewID,
		Overrides: e.Variant.Overrides, Pack: pack,
		Injected: injected, Provider: e.provider.Name(), CreatedAt: time.Now(),
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

// engineOwned is what Up puts in a workdir. Everything else there was put
// there by a session and is evidence.
var engineOwned = []string{"rendered", "kubeconfig"}

// Down tears the environment down. It removes what Up created and keeps
// everything else: the workdir is also where the recording, the score, and
// the hints live, and teardown is the last step of an interview, so
// deleting the directory wholesale destroys the evidence of the session it
// is ending. Kept returns the paths left behind, empty when the workdir was
// removed because it held nothing but environment files. Purge deletes the
// evidence too, for authoring and CI.
func (e *Engine) Down(ctx context.Context, purge bool) (kept []string, err error) {
	if err := e.provider.Down(ctx); err != nil {
		return nil, err
	}
	if purge {
		return nil, os.RemoveAll(e.Workdir)
	}
	for _, name := range engineOwned {
		if err := os.RemoveAll(filepath.Join(e.Workdir, name)); err != nil {
			return nil, err
		}
	}
	// The state file outlives the environment: grading re-resolves the variant
	// from the parameters it recorded, so removing it would change the sheet a
	// teardown renders. Stamping it keeps every later command able to say the
	// environment is gone instead of failing on a script against nothing.
	if st, err := e.loadState(); err == nil && st.Live() {
		st.TornDownAt = time.Now()
		if err := e.saveState(st); err != nil {
			return nil, err
		}
	}
	entries, err := os.ReadDir(e.Workdir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	for _, ent := range entries {
		if ent.Name() == StateFile || emptyDir(filepath.Join(e.Workdir, ent.Name()), ent) {
			continue
		}
		kept = append(kept, filepath.Join(e.Workdir, ent.Name()))
	}
	if len(kept) == 0 {
		// Only when the state file is gone too. It is excluded from kept
		// above, so a workdir holding nothing else looked empty and was
		// removed, taking with it the recorded parameters the comment above
		// says grading re-resolves the variant from.
		if _, serr := os.Stat(filepath.Join(e.Workdir, StateFile)); errors.Is(serr, os.ErrNotExist) {
			return nil, os.RemoveAll(e.Workdir)
		}
	}
	return kept, nil
}

// emptyDir reports a directory with nothing in it, which is scratch space
// something forgot to clear rather than evidence worth naming.
func emptyDir(path string, ent fs.DirEntry) bool {
	if !ent.IsDir() {
		return false
	}
	inner, err := os.ReadDir(path)
	return err == nil && len(inner) == 0
}

// Break injects the variant's fault pack in lexical fault-id order.
func (e *Engine) Break(ctx context.Context) error {
	st, err := e.liveState()
	if err != nil {
		return err
	}
	// Record each fault as it lands, and never drop what is already recorded.
	// A pack that fails partway has still broken the environment, and a state
	// file that forgets which faults are in there leaves nothing able to check
	// or fix them. Clearing the list up front turned a re-break whose first
	// inject failed into a state claiming an untouched cluster, while every
	// fault from the earlier break was still live and the score read 0/0.
	for _, f := range e.Scenario.PackFaults(st.Pack) {
		e.logf("inject %s (%s)", f.Spec.ID, f.Spec.Tier)
		if err := e.script(ctx, f.Script("inject.sh"), nil); err != nil {
			if saveErr := e.saveState(st); saveErr != nil {
				return fmt.Errorf("inject %s: %w (and recording progress failed: %v)", f.Spec.ID, err, saveErr)
			}
			return fmt.Errorf("inject %s: %w", f.Spec.ID, err)
		}
		if !slices.Contains(st.Injected, f.Spec.ID) {
			st.Injected = append(st.Injected, f.Spec.ID)
		}
		if err := e.saveState(st); err != nil {
			return err
		}
	}
	return nil
}

// Status checks every injected fault.
func (e *Engine) Status(ctx context.Context) ([]FaultStatus, error) {
	st, err := e.liveState()
	if err != nil {
		return nil, err
	}
	var out []FaultStatus
	for _, id := range st.Injected {
		f, ok := e.Scenario.Fault(id)
		if !ok {
			return nil, fmt.Errorf("state names unknown fault %q", id)
		}
		out = append(out, FaultStatus{ID: id, Title: f.Spec.Title, Tier: f.Spec.Tier,
			State: e.check(ctx, f)})
	}
	return out, nil
}

// check runs one fault's check script and maps its exit status onto the
// fault check contract.
func (e *Engine) check(ctx context.Context, f Fault) CheckState {
	err := e.script(ctx, f.Script("check.sh"), nil)
	switch {
	case err == nil:
		return CheckFixed
	case exitCode(err) == CheckCannotRunExit:
		return CheckCannotRun
	default:
		return CheckBroken
	}
}

// Fix applies the answer key for one injected fault, or all of them when
// id is empty. Fixing everything sets IV_FIX_FAST=1 so fixes skip their
// individual convergence waits; the caller verifies once at the end.
func (e *Engine) Fix(ctx context.Context, id string) error {
	st, err := e.liveState()
	if err != nil {
		return err
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

// StateFile is the environment state's name inside a session workdir.
const StateFile = "state.json"

func (e *Engine) statePath() string { return filepath.Join(e.Workdir, StateFile) }

func (e *Engine) saveState(st *State) error {
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	// Timers read this file every 30 seconds while commands write it, so it
	// swaps into place rather than truncating in front of a reader. It names
	// every injected fault, so it stays owner-only.
	return fileio.WriteAtomic(e.statePath(), raw, 0o600)
}

func (e *Engine) loadState() (*State, error) { return LoadState(e.Workdir) }

// liveState is loadState for the commands that need something to act on, so
// a torn-down environment reads as a step to redo rather than as a pile of
// script failures against a cluster that is not there.
func (e *Engine) liveState() (*State, error) {
	st, err := e.loadState()
	if err != nil {
		return nil, fmt.Errorf("no environment state in %s; run interviews env up first: %w", e.Workdir, err)
	}
	if !st.Live() {
		return nil, fmt.Errorf("environment %s was torn down at %s; run interviews env up to build it again",
			e.EnvName(), st.TornDownAt.Format(time.RFC3339))
	}
	return st, nil
}

// LoadState reads a workdir's environment state.
func LoadState(workdir string) (*State, error) {
	raw, err := os.ReadFile(filepath.Join(workdir, StateFile))
	if err != nil {
		return nil, err
	}
	var st State
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, err
	}
	return &st, nil
}
