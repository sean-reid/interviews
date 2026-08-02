package debug

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sean-reid/interviews/internal/provenance"
	"github.com/sean-reid/interviews/internal/version"
)

// The tools as they really answer. kind prints one line about itself;
// kubectl's json is the client version and whatever else it carries.
const (
	kindSays    = "kind v0.29.0 go1.24.2 linux/amd64\n"
	kubectlSays = `{"clientVersion":{"major":"1","minor":"33","gitVersion":"v1.33.2"},` +
		`"kustomizeVersion":"v5.6.0"}`
)

// Two bundles of the same problem were built on two different Kubernetes
// versions and no artifact said which. The state file the evidence carries
// now records what the environment was produced on, read as it comes up.
func TestUpRecordsWhatTheEnvironmentWasProducedOn(t *testing.T) {
	e, r := testEngine(t, nil, map[string]string{"fault_pack": "pack-a"})
	// A scenario that pins its own Kubernetes, so what lands in the record is
	// what the provider passed rather than the platform constant.
	e.Scenario.Env.Kind.NodeImage = "kindest/node:v1.31.4@sha256:0badc0de"
	e.Origin = provenance.New(provenance.Host, "v-0123456789abcdef")
	r.outputs["kind version"] = kindSays
	r.outputs["-o json"] = kubectlSays

	if err := e.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	st, err := e.loadState()
	if err != nil {
		t.Fatal(err)
	}
	p := st.Provenance
	if p == nil {
		t.Fatal("state records nothing about the machine")
	}
	// Against the command that built the cluster, not against the field the
	// provider read it from: a test that compares the record with its own
	// source agrees with itself and would miss the two coming apart.
	created := r.callsMatching("kind create cluster")
	if len(created) != 1 {
		t.Fatalf("cluster created %d times: %v", len(created), r.calls)
	}
	if p.NodeImage == "" || !strings.Contains(created[0], "--image "+p.NodeImage) {
		t.Errorf("recorded node image %q is not what built the cluster: %q", p.NodeImage, created[0])
	}
	if p.NodeImage == DefaultNodeImage {
		t.Errorf("recorded the platform default over the scenario's pin %q", e.Scenario.Env.Kind.NodeImage)
	}
	if p.Kind != "v0.29.0" {
		t.Errorf("kind version = %q, want what %q reports", p.Kind, strings.TrimSpace(kindSays))
	}
	if p.Kubectl != "v1.33.2" {
		t.Errorf("kubectl version = %q, want what %s reports", p.Kubectl, kubectlSays)
	}
	if p.Where != provenance.Host || p.Content != "v-0123456789abcdef" {
		t.Errorf("origin lost: where %q, content %q", p.Where, p.Content)
	}
	if p.Platform != version.Version {
		t.Errorf("platform version = %q, want %q", p.Platform, version.Version)
	}
	if len(p.Undetermined) != 0 {
		t.Errorf("everything answered and the record still names gaps: %v", p.Undetermined)
	}
	if p.At.IsZero() {
		t.Error("the record does not say when it was taken")
	}
}

// Provenance is a record, not a precondition: a binary that cannot say what
// version it is must cost a field, not the environment.
func TestUpSurvivesAVersionItCannotRead(t *testing.T) {
	e, r := testEngine(t, nil, map[string]string{"fault_pack": "pack-a"})
	e.Origin = provenance.New(provenance.Local, "abc1234")
	r.outputs["kind version"] = kindSays
	r.outputErr["-o json"] = errors.New("exec: \"kubectl\": executable file not found in $PATH")

	if err := e.Up(context.Background()); err != nil {
		t.Fatalf("a version lookup broke the bring-up: %v", err)
	}
	st, err := e.loadState()
	if err != nil {
		t.Fatal(err)
	}
	p := st.Provenance
	if p.Kind != "v0.29.0" || p.NodeImage == "" {
		t.Errorf("one failed lookup took the rest of the record with it: %+v", p)
	}
	if p.Kubectl != "" {
		t.Errorf("kubectl version = %q, want nothing at all", p.Kubectl)
	}
	if len(p.Undetermined) != 1 || !strings.Contains(p.Undetermined[0], "kubectl") {
		t.Errorf("undetermined = %v, want it to name kubectl and why", p.Undetermined)
	}
	// Absent, not empty: a "" version in an evidence bundle reads as a
	// version, and the wrong one.
	if raw := stateJSON(t, e.Workdir); strings.Contains(raw, "kubectl_version") {
		t.Errorf("the state file carries an empty kubectl version: %s", raw)
	}
}

// Output that arrives but says nothing usable is a gap too, and one the
// record has to name rather than pass on.
func TestUpNamesAVersionItCouldNotParse(t *testing.T) {
	e, r := testEngine(t, nil, map[string]string{"fault_pack": "pack-a"})
	r.outputs["kind version"] = "command not found\n"
	r.outputs["-o json"] = kubectlSays

	if err := e.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	st, err := e.loadState()
	if err != nil {
		t.Fatal(err)
	}
	if st.Provenance.Kind != "" {
		t.Errorf("kind version = %q, want nothing from unusable output", st.Provenance.Kind)
	}
	if len(st.Provenance.Undetermined) != 1 || !strings.Contains(st.Provenance.Undetermined[0], "kind") {
		t.Errorf("undetermined = %v, want it to name kind", st.Provenance.Undetermined)
	}
}

// A compose environment has no cluster and no node image. Those fields stay
// out of the file rather than going in empty, and what the platform knows
// regardless still lands.
func TestComposeRecordsWhatItHasAndNothingElse(t *testing.T) {
	e, _ := testEngine(t, composeProblem, map[string]string{"fault_pack": "pack-a"})
	e.Origin = provenance.New(provenance.Local, "abc1234")
	if err := e.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	st, err := e.loadState()
	if err != nil {
		t.Fatal(err)
	}
	if st.Provenance.Platform != version.Version || st.Provenance.Content != "abc1234" {
		t.Errorf("compose kept nothing of the origin: %+v", st.Provenance)
	}
	raw := stateJSON(t, e.Workdir)
	for _, key := range []string{"node_image", "kind_version", "kubectl_version"} {
		if strings.Contains(raw, key) {
			t.Errorf("compose state carries %s: %s", key, raw)
		}
	}
	// Positive control: the keys are spelled the way this looks for them.
	if !strings.Contains(marshalled(t, provenance.Record{NodeImage: "x", Kind: "y", Kubectl: "z"}),
		"node_image") {
		t.Fatal("the field names this test greps for are not the ones the record writes")
	}
}

// An engine nobody told where it is still records the platform it ran, so a
// state file always says at least which build wrote it.
func TestProvenanceFallsBackToThisBinary(t *testing.T) {
	e, _ := testEngine(t, nil, map[string]string{"fault_pack": "pack-a"})
	if err := e.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	st, err := e.loadState()
	if err != nil {
		t.Fatal(err)
	}
	if st.Provenance.Platform != version.Version || st.Provenance.At.IsZero() {
		t.Errorf("provenance without an origin = %+v", st.Provenance)
	}
}

// The tests above tell a fake what the tools print. This asks the tools
// themselves wherever they are installed, so a format that moved is caught
// here rather than by a bundle that quietly records nothing.
func TestVersionsParseWhatTheRealBinariesPrint(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		parse func(string) (string, error)
	}{
		{"kind", []string{"version"}, kindVersion},
		{"kubectl", []string{"version", "--client", "-o", "json"}, kubectlVersion},
	} {
		if _, err := exec.LookPath(tc.name); err != nil {
			t.Logf("%s is not installed here, so its format is unchecked", tc.name)
			continue
		}
		r := &ExecRunner{Stdout: io.Discard, Stderr: io.Discard}
		out, err := r.Output(context.Background(), tc.name, tc.args...)
		if err != nil {
			t.Fatalf("%s %v: %v", tc.name, tc.args, err)
		}
		v, err := tc.parse(out)
		if err != nil || !strings.HasPrefix(v, "v") {
			t.Errorf("%s printed %q, which read as %q, %v", tc.name, out, v, err)
		}
	}
}

func stateJSON(t *testing.T, workdir string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(workdir, StateFile))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func marshalled(t *testing.T, r provenance.Record) string {
	t.Helper()
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
