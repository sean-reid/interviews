package debug

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/sean-reid/interviews/internal/provenance"
)

// DefaultNodeImage is the Kubernetes a scenario gets when it does not ask
// for one. Without it the version comes from whichever kind binary is on the
// machine, so the same problem ran on a laptop and on a provisioned host is
// not the same problem: kind v0.29 defaults to v1.33.1 and v0.31 to v1.35.0.
// This is the default of the kind that session/host/provision.sh installs,
// and the two pins move together. Digest pinned, because the tag is mutable.
const DefaultNodeImage = "kindest/node:v1.33.1@sha256:050072256b9a903bd914c0b2866828150cb229cea0efe5892e2b644d5dd3b34f"

// podSecurityLabels confine the candidate. The Role a problem grants can
// patch workloads, which is enough to rewrite a pod template as privileged
// with a hostPath mount and exec through it onto the host; baseline
// admission is what refuses such pods. The provider sets the labels itself
// so a problem authored without them does not silently open that path.
var podSecurityLabels = []string{
	"pod-security.kubernetes.io/enforce=baseline",
	"pod-security.kubernetes.io/enforce-version=latest",
}

// kindProvider hosts kubernetes-flavor scenarios in a local kind cluster.
// Images build locally and load into the cluster; nothing needs a registry.
type kindProvider struct {
	e *Engine
}

// nodeImage is what the cluster runs: the scenario's pin if it has one, since
// a problem may need a version, and the platform's otherwise.
func (p *kindProvider) nodeImage() string {
	if img := p.e.Scenario.Env.Kind.NodeImage; img != "" {
		return img
	}
	return DefaultNodeImage
}

func (p *kindProvider) Name() string { return "kind" }

func (p *kindProvider) cluster() string { return envName(p.e.Variant) }

func (p *kindProvider) kubeconfig() string { return p.e.KubeconfigPath() }

func (p *kindProvider) namespace() (string, error) {
	return p.e.RenderString(p.e.Scenario.Env.Kind.Namespace)
}

func (p *kindProvider) env(env map[string]string) {
	ns, err := p.namespace()
	if err != nil {
		ns = p.e.Scenario.Env.Kind.Namespace
	}
	env["IV_NAMESPACE"] = ns
	env["IV_CLUSTER"] = p.cluster()
	env["KUBECONFIG"] = p.kubeconfig()
}

// provenance records the image this cluster was built from and the versions
// the two binaries report for themselves. The image is what the provider
// passed, not the constant behind it, so a scenario that pins its own
// Kubernetes records the one it got. The two extra calls sit inside a
// bring-up that is already minutes of cluster build, and neither is allowed
// to fail it.
func (p *kindProvider) provenance(ctx context.Context, r *provenance.Record) {
	r.NodeImage = p.nodeImage()
	r.Kind = p.version(ctx, r, "kind", kindVersion, "version")
	r.Kubectl = p.version(ctx, r, "kubectl", kubectlVersion, "version", "--client", "-o", "json")
}

// version asks one binary what it is and parses the answer. A binary that
// cannot say leaves the field absent and a line saying why.
func (p *kindProvider) version(ctx context.Context, r *provenance.Record,
	name string, parse func(string) (string, error), args ...string) string {
	out, err := p.e.Runner.Output(ctx, name, args...)
	if err != nil {
		r.Missing(name+" version", err)
		return ""
	}
	v, err := parse(out)
	if err != nil {
		r.Missing(name+" version", err)
		return ""
	}
	return v
}

// kindVersion reads "kind v0.29.0 go1.24.2 linux/amd64" down to the version.
func kindVersion(out string) (string, error) {
	line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	fields := strings.Fields(line)
	if len(fields) < 2 || fields[0] != "kind" {
		return "", fmt.Errorf("kind version said %q", line)
	}
	return fields[1], nil
}

// kubectlVersion reads the client version out of kubectl's own json, which
// is a stabler contract than the human line above it.
func kubectlVersion(out string) (string, error) {
	var v struct {
		ClientVersion struct {
			GitVersion string `json:"gitVersion"`
		} `json:"clientVersion"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return "", err
	}
	if v.ClientVersion.GitVersion == "" {
		return "", errors.New("kubectl reported no client gitVersion")
	}
	return v.ClientVersion.GitVersion, nil
}

func (p *kindProvider) Detect(ctx context.Context) error {
	for _, tool := range [][]string{{"docker", "version"}, {"kind", "version"}, {"kubectl", "version", "--client"}} {
		if _, err := p.e.Runner.Output(ctx, tool[0], tool[1:]...); err != nil {
			return fmt.Errorf("%s not usable: %w", tool[0], err)
		}
	}
	return nil
}

func (p *kindProvider) Up(ctx context.Context) error {
	spec := p.e.Scenario.Env.Kind
	r := p.e.Runner

	clusters, err := r.Output(ctx, "kind", "get", "clusters")
	if err != nil {
		return err
	}
	if !hasLine(clusters, p.cluster()) {
		args := []string{"create", "cluster", "--name", p.cluster(), "--kubeconfig", p.kubeconfig(),
			"--wait", "120s", "--image", p.nodeImage()}
		if err := r.Command(ctx, "kind", args...); err != nil {
			return err
		}
	} else if err := r.Command(ctx, "kind", "export", "kubeconfig", "--name", p.cluster(), "--kubeconfig", p.kubeconfig()); err != nil {
		return err
	}

	for _, b := range spec.Build {
		if err := r.Command(ctx, "docker", "build", "-t", b.Image, filepath.Join(p.e.Dir, b.Context)); err != nil {
			return err
		}
		if err := r.Command(ctx, "kind", "load", "docker-image", b.Image, "--name", p.cluster()); err != nil {
			return err
		}
	}

	manifests, err := p.e.render(spec.Manifests)
	if err != nil {
		return err
	}
	if err := r.Command(ctx, "kubectl", "--kubeconfig", p.kubeconfig(), "apply", "-R", "-f", manifests); err != nil {
		return err
	}

	ns, err := p.namespace()
	if err != nil {
		return err
	}
	// The manifests create the namespace, so the labels can only land
	// after apply. That is early enough: the candidate gets in only
	// after Up returns, and admission judges pods at creation, so
	// anything they spawn is already confined. --overwrite keeps
	// manifests that declare the same labels working.
	args := append([]string{"--kubeconfig", p.kubeconfig(), "label", "--overwrite", "namespace", ns}, podSecurityLabels...)
	return r.Command(ctx, "kubectl", args...)
}

func (p *kindProvider) Down(ctx context.Context) error {
	return p.e.Runner.Command(ctx, "kind", "delete", "cluster", "--name", p.cluster())
}

func hasLine(out, want string) bool {
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == want {
			return true
		}
	}
	return false
}
