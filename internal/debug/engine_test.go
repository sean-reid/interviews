package debug

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/sean-reid/interviews/internal/content"
	"github.com/sean-reid/interviews/internal/taxonomy"
	"github.com/sean-reid/interviews/internal/variant"
)

// fakeRunner records every call and serves scripted results per script
// basename-with-parent (e.g. "01-image-typo/check.sh").
type fakeRunner struct {
	mu       sync.Mutex
	calls    []string
	envs     []map[string]string
	scripted map[string][]error // queue per script key; empty queue = nil
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{scripted: map[string][]error{}}
}

func scriptKey(path string) string {
	return filepath.Base(filepath.Dir(path)) + "/" + filepath.Base(path)
}

// on queues results for a script; once drained, further calls return the
// last queued value.
func (r *fakeRunner) on(key string, results ...error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.scripted[key] = append(r.scripted[key], results...)
}

func (r *fakeRunner) Command(_ context.Context, name string, args ...string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	return nil
}

func (r *fakeRunner) Output(_ context.Context, name string, args ...string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	return "", nil
}

func (r *fakeRunner) Start(_ context.Context, name string, args ...string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, "start "+name+" "+strings.Join(args, " "))
	return 40000 + len(r.calls), nil
}

func (r *fakeRunner) Alive(pid int) bool { return pid > 0 }

func (r *fakeRunner) Script(_ context.Context, path, _ string, env map[string]string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := scriptKey(path)
	r.calls = append(r.calls, "script "+key)
	r.envs = append(r.envs, env)
	queue := r.scripted[key]
	if len(queue) == 0 {
		return nil
	}
	result := queue[0]
	if len(queue) > 1 {
		r.scripted[key] = queue[1:]
	}
	return result
}

func (r *fakeRunner) callsMatching(sub string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, c := range r.calls {
		if strings.Contains(c, sub) {
			out = append(out, c)
		}
	}
	return out
}

func testEngine(t *testing.T, mutate func(fstest.MapFS), overrides map[string]string) (*Engine, *fakeRunner) {
	t.Helper()
	fsys := scenarioFS(mutate)
	p, issues := content.Load(fsys, taxonomy.Debugging, "pipeline-meltdown")
	if content.Errors(issues) {
		t.Fatalf("fixture invalid: %v", issues)
	}
	s, issues := LoadScenario(p)
	if len(issues) != 0 {
		t.Fatalf("scenario invalid: %v", issues)
	}
	v, err := variant.Resolve("pipeline-meltdown", p.Manifest.Params, "test-seed", overrides)
	if err != nil {
		t.Fatal(err)
	}
	r := newFakeRunner()
	e, err := NewEngine("/problems/pipeline-meltdown", s, v, r, os.Stderr, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e.Settle, e.PollInterval, e.FixTimeout, e.VerifyTimeout = 0, 0, 0, 0
	return e, r
}

func TestUpRendersBuildsAppliesVerifies(t *testing.T) {
	e, r := testEngine(t, func(m fstest.MapFS) {
		m["env/manifests/00-ns.yaml"] = &fstest.MapFile{Data: []byte("name: shop-{{.fault_pack}}")}
	}, map[string]string{"fault_pack": "pack-a"})
	if err := e.Up(context.Background()); err != nil {
		t.Fatal(err)
	}

	rendered, err := os.ReadFile(filepath.Join(e.Workdir, "rendered/env/manifests/00-ns.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(rendered) != "name: shop-pack-a" {
		t.Errorf("rendered manifest = %q", rendered)
	}

	for _, want := range []string{
		"kind get clusters",
		"kind create cluster --name " + envName(e.Variant),
		"docker build -t shop-api /problems/pipeline-meltdown/app/api",
		"kind load docker-image shop-api --name " + envName(e.Variant),
		"kubectl --kubeconfig " + filepath.Join(e.Workdir, "kubeconfig") + " apply -R -f",
		"script env/verify.sh",
	} {
		if len(r.callsMatching(want)) == 0 {
			t.Errorf("no call matching %q in %v", want, r.calls)
		}
	}

	st, err := e.loadState()
	if err != nil {
		t.Fatal(err)
	}
	if st.Pack != "pack-a" || len(st.Injected) != 0 {
		t.Errorf("state = %+v", st)
	}
}

func TestScriptEnvCarriesVariantAndProvider(t *testing.T) {
	e, r := testEngine(t, nil, map[string]string{"fault_pack": "pack-b"})
	if err := e.Verify(context.Background()); err != nil {
		t.Fatal(err)
	}
	env := r.envs[len(r.envs)-1]
	if env["IV_PARAM_FAULT_PACK"] != "pack-b" || env["IV_PROBLEM"] != "pipeline-meltdown" {
		t.Errorf("script env = %v", env)
	}
	if env["IV_NAMESPACE"] != "shop" || env["IV_CLUSTER"] == "" || env["KUBECONFIG"] == "" {
		t.Errorf("provider env missing: %v", env)
	}
}

func TestBreakInjectsOnlyThePack(t *testing.T) {
	e, r := testEngine(t, nil, map[string]string{"fault_pack": "pack-a"})
	ctx := context.Background()
	if err := e.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.Break(ctx); err != nil {
		t.Fatal(err)
	}
	injects := r.callsMatching("inject.sh")
	if len(injects) != 1 || !strings.Contains(injects[0], "01-image-typo") {
		t.Errorf("pack-a injects = %v", injects)
	}
	st, _ := e.loadState()
	if len(st.Injected) != 1 || st.Injected[0] != "01-image-typo" {
		t.Errorf("state injected = %v", st.Injected)
	}
}

func TestBreakRequiresState(t *testing.T) {
	e, _ := testEngine(t, nil, nil)
	if err := e.Break(context.Background()); err == nil || !strings.Contains(err.Error(), "env up first") {
		t.Errorf("Break without state = %v", err)
	}
}

func TestStatusReflectsChecks(t *testing.T) {
	e, r := testEngine(t, nil, map[string]string{"fault_pack": "pack-b"})
	ctx := context.Background()
	if err := e.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.Break(ctx); err != nil {
		t.Fatal(err)
	}
	r.on("01-image-typo/check.sh", errors.New("broken"))
	statuses, err := e.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 2 {
		t.Fatalf("statuses = %+v", statuses)
	}
	if statuses[0].ID != "01-image-typo" || statuses[0].Fixed {
		t.Errorf("fault 01 should be broken: %+v", statuses[0])
	}
	if statuses[1].ID != "02-net-policy" || !statuses[1].Fixed {
		t.Errorf("fault 02 should be fixed: %+v", statuses[1])
	}
}

func TestFixAllSetsFixFast(t *testing.T) {
	e, r := testEngine(t, nil, map[string]string{"fault_pack": "pack-b"})
	ctx := context.Background()
	if err := e.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.Break(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.Fix(ctx, ""); err != nil {
		t.Fatal(err)
	}
	fastSeen := false
	for _, env := range r.envs {
		if env["IV_FIX_FAST"] == "1" {
			fastSeen = true
		}
	}
	if !fastSeen {
		t.Error("IV_FIX_FAST not set when fixing everything")
	}

	// Fixing one fault must not set it.
	r2 := newFakeRunner()
	e.Runner = r2
	if err := e.Fix(ctx, "01-image-typo"); err != nil {
		t.Fatal(err)
	}
	for _, env := range r2.envs {
		if env["IV_FIX_FAST"] == "1" {
			t.Error("IV_FIX_FAST set for a single-fault fix")
		}
	}
}

func TestProveHappyPath(t *testing.T) {
	e, r := testEngine(t, nil, map[string]string{"fault_pack": "pack-b"})
	// Each fault: broken right after inject, then fixed after fix.sh.
	// Queues: first check fails (post-inject), second passes (post-fix).
	// The final full cycle runs inject+fix again; queue drains to pass.
	r.on("01-image-typo/check.sh", errors.New("broken"), nil)
	r.on("02-net-policy/check.sh", errors.New("broken"), nil)
	// Verify passes for the healthy environment, fails once the whole pack is
	// injected, then passes again after every fix: a real scenario's shape.
	r.on("env/verify.sh", nil, errors.New("app down"), nil)
	if err := e.Prove(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := len(r.callsMatching("inject.sh")); got != 4 { // 2 per-fault + 2 full cycle
		t.Errorf("inject calls = %d, want 4", got)
	}
}

func TestProveFailsWhenFaultDoesNotBreak(t *testing.T) {
	e, _ := testEngine(t, nil, map[string]string{"fault_pack": "pack-a"})
	// check.sh passes immediately after inject: the fault is a no-op.
	err := e.Prove(context.Background())
	if err == nil || !strings.Contains(err.Error(), "does not break anything") {
		t.Errorf("Prove = %v, want does-not-break error", err)
	}
}

func TestProveFailsWhenFixDoesNotConverge(t *testing.T) {
	e, r := testEngine(t, nil, map[string]string{"fault_pack": "pack-a"})
	broken := errors.New("still broken")
	// Only queued values repeat the last entry once drained, so this check
	// stays broken forever.
	r.on("01-image-typo/check.sh", broken)
	err := e.Prove(context.Background())
	if err == nil || !strings.Contains(err.Error(), "after the documented fix") {
		t.Errorf("Prove = %v, want no-convergence error", err)
	}
}

func TestProveWaitsForConvergence(t *testing.T) {
	e, r := testEngine(t, nil, map[string]string{"fault_pack": "pack-a"})
	broken := errors.New("broken")
	// Post-inject broken, then two slow polls, then fixed.
	r.on("01-image-typo/check.sh", broken, broken, broken, nil)
	r.on("env/verify.sh", nil, errors.New("app down"), nil)
	e.FixTimeout = 5e9 // 5s in nanoseconds; polls are instant with 0 interval
	if err := e.Prove(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := len(r.callsMatching("01-image-typo/check.sh")); got < 4 {
		t.Errorf("check polled %d times, want >= 4", got)
	}
}

// The gate must refuse a scenario whose verify script cannot fail, because
// then nothing proves the exercise still breaks.
func TestProveRejectsVerifyThatNeverFails(t *testing.T) {
	e, r := testEngine(t, nil, map[string]string{"fault_pack": "pack-a"})
	r.on("01-image-typo/check.sh", errors.New("broken"), nil)
	err := e.Prove(context.Background())
	if err == nil || !strings.Contains(err.Error(), "verify still passes") {
		t.Errorf("Prove = %v, want a verify-cannot-fail error", err)
	}
}

func TestProveVerifiesRecoveryAfterFullCycle(t *testing.T) {
	e, r := testEngine(t, nil, map[string]string{"fault_pack": "pack-a"})
	r.on("01-image-typo/check.sh", errors.New("broken"), nil)
	// verify: healthy during Up, then permanently broken for the final wait.
	r.on("env/verify.sh", nil, fmt.Errorf("app down"))
	err := e.Prove(context.Background())
	if err == nil || !strings.Contains(err.Error(), "did not recover") {
		t.Errorf("Prove = %v, want did-not-recover error", err)
	}
}

func TestEnvNameStableAndBounded(t *testing.T) {
	v := &variant.Resolved{Problem: "a-very-long-problem-name-that-keeps-going-and-going", InterviewID: "seed"}
	a, b := envName(v), envName(v)
	if a != b {
		t.Error("envName not deterministic")
	}
	if len(a) > 40 {
		t.Errorf("envName too long: %s", a)
	}
	v2 := &variant.Resolved{Problem: v.Problem, InterviewID: "other"}
	if envName(v2) == a {
		t.Error("different seeds share an env name")
	}
}

func TestSessionSeams(t *testing.T) {
	e, _ := testEngine(t, nil, map[string]string{"fault_pack": "pack-a"})
	if e.EnvName() != envName(e.Variant) {
		t.Errorf("EnvName = %q, want %q", e.EnvName(), envName(e.Variant))
	}
	if e.ProviderName() != "kind" {
		t.Errorf("ProviderName = %q", e.ProviderName())
	}
	if got, want := e.KubeconfigPath(), filepath.Join(e.Workdir, "kubeconfig"); got != want {
		t.Errorf("KubeconfigPath = %q, want %q", got, want)
	}
	ns, err := e.KindNamespace()
	if err != nil || ns != "shop" {
		t.Errorf("KindNamespace = %q, %v", ns, err)
	}
	if err := e.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	st, err := LoadState(e.Workdir)
	if err != nil || st.Problem != "pipeline-meltdown" || st.Pack != "pack-a" {
		t.Errorf("LoadState = %+v, %v", st, err)
	}
}

func TestRenderBuiltins(t *testing.T) {
	e, _ := testEngine(t, func(m fstest.MapFS) {
		m["env/manifests/dir.yaml"] = &fstest.MapFile{Data: []byte("dir: {{._dir}}\nwork: {{._workdir}}")}
	}, map[string]string{"fault_pack": "pack-a"})
	if err := e.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	rendered, err := os.ReadFile(filepath.Join(e.Workdir, "rendered/env/manifests/dir.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	want := "dir: /problems/pipeline-meltdown\nwork: " + e.Workdir
	if string(rendered) != want {
		t.Errorf("rendered = %q, want %q", rendered, want)
	}
}
