package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/sean-reid/interviews/internal/debug"
	"github.com/sean-reid/interviews/internal/interview"
)

// A session started with --set records what it was built with, and every
// later command has to act on those parameters rather than re-derive them
// from the seed. Two copies of this resolution existed and only the grading
// one honoured the record, so the copy that touches the environment, the one
// behind fault fix, exported the wrong IV_PARAM_* values to the fix scripts.
func TestResolveProblemHonoursTheRecordedOverrides(t *testing.T) {
	t.Setenv(interview.HomeEnv, t.TempDir())
	workdir := t.TempDir()
	const seed = "calm-bison-0801"

	// Positive control: with nothing recorded the seed decides, and whatever
	// it picks is what the recorded value below has to be seen to override.
	plain, err := resolveProblem(goodRoot, "pipeline-meltdown", seed, workdir, nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	derived, _ := plain.variant.Params["fault_pack"].(string)
	if derived == "" {
		t.Fatal("no fault_pack resolved; this test proves nothing")
	}
	other := "pack-a"
	if derived == "pack-a" {
		other = "pack-b"
	}

	// What start --set wrote when it built the environment.
	raw, err := json.Marshal(debug.State{
		Problem: "pipeline-meltdown", Seed: seed,
		Overrides: map[string]string{"fault_pack": other},
		Pack:      other,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workdir, debug.StateFile), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := resolveProblem(goodRoot, "pipeline-meltdown", seed, workdir, nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if pack, _ := got.variant.Params["fault_pack"].(string); pack != other {
		t.Errorf("fault_pack = %q, want the recorded %q the environment was built with", pack, other)
	}

	// An explicit --set is the interviewer speaking now, so it still wins.
	back, err := resolveProblem(goodRoot, "pipeline-meltdown", seed, workdir,
		[]string{"fault_pack=" + derived}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if pack, _ := back.variant.Params["fault_pack"].(string); pack != derived {
		t.Errorf("fault_pack = %q with an explicit --set, want %q", pack, derived)
	}
}
