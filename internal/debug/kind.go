package debug

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// kindProvider hosts kubernetes-flavor scenarios in a local kind cluster.
// Images build locally and load into the cluster; nothing needs a registry.
type kindProvider struct {
	e *Engine
}

func (p *kindProvider) Name() string { return "kind" }

func (p *kindProvider) cluster() string { return envName(p.e.Variant) }

// kubeconfig is exported per cluster so concurrent environments and the
// interviewer's own kubectl context never fight over current-context.
func (p *kindProvider) kubeconfig() string { return filepath.Join(p.e.Workdir, "kubeconfig") }

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
		args := []string{"create", "cluster", "--name", p.cluster(), "--kubeconfig", p.kubeconfig(), "--wait", "120s"}
		if spec.NodeImage != "" {
			args = append(args, "--image", spec.NodeImage)
		}
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
	return r.Command(ctx, "kubectl", "--kubeconfig", p.kubeconfig(), "apply", "-R", "-f", manifests)
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
