package session

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"

	"github.com/sean-reid/interviews/internal/debug"
)

// candidateRBAC scopes the candidate to the scenario namespace: read and
// mutate the app's own objects, exec and port-forward into pods, plus
// cluster-wide read-only nodes. No RBAC, no namespaces, no kube-system.
const candidateRBAC = `apiVersion: v1
kind: ServiceAccount
metadata:
  name: candidate
  namespace: {{.Namespace}}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: candidate
  namespace: {{.Namespace}}
rules:
  - apiGroups: [""]
    resources: [pods, pods/log, services, endpoints, configmaps, secrets, events]
    verbs: [get, list, watch]
  - apiGroups: [apps]
    resources: [deployments, statefulsets]
    verbs: [get, list, watch]
  - apiGroups: [batch]
    resources: [jobs]
    verbs: [get, list, watch]
  - apiGroups: [apps]
    resources: [deployments, statefulsets]
    verbs: [patch, update, delete]
  - apiGroups: [""]
    resources: [services, configmaps, secrets, pods]
    verbs: [patch, update, delete]
  - apiGroups: [""]
    resources: [pods/exec, pods/portforward]
    verbs: [create]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: candidate
  namespace: {{.Namespace}}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: candidate
subjects:
  - kind: ServiceAccount
    name: candidate
    namespace: {{.Namespace}}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: iv-candidate-nodes
rules:
  - apiGroups: [""]
    resources: [nodes]
    verbs: [get, list, watch]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: iv-candidate-nodes
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: iv-candidate-nodes
subjects:
  - kind: ServiceAccount
    name: candidate
    namespace: {{.Namespace}}
`

const candidateKubeconfig = `apiVersion: v1
kind: Config
clusters:
  - name: interview
    cluster:
      server: {{.Server}}
      certificate-authority-data: {{.CAData}}
users:
  - name: candidate
    user:
      token: {{.Token}}
contexts:
  - name: interview
    context:
      cluster: interview
      user: candidate
      namespace: {{.Namespace}}
current-context: interview
`

// KubeconfigOptions tune where the candidate kubeconfig lands. The workdir
// copy is always written, because evidence bundles it; Out adds a second
// copy for an account that cannot read the workdir.
type KubeconfigOptions struct {
	Out  string
	Mode fs.FileMode
}

// Kubeconfig applies the candidate ServiceAccount and its RBAC to the
// scenario cluster, mints a bounded token, and writes a standalone
// workdir/candidate.kubeconfig pointing at the cluster. Kind-flavor
// problems only. Returns the path callers should hand out.
func (m *Manager) Kubeconfig(ctx context.Context, opts KubeconfigOptions) (string, error) {
	if m.Engine.ProviderName() != "kind" {
		return "", fmt.Errorf("candidate kubeconfigs need a kind-flavor problem (provider is %s)", m.Engine.ProviderName())
	}
	if _, err := debug.LoadState(m.Engine.Workdir); err != nil {
		return "", fmt.Errorf("no environment state in %s; run interviews env up first: %w", m.Engine.Workdir, err)
	}
	ns, err := m.Engine.KindNamespace()
	if err != nil {
		return "", err
	}
	kc := m.Engine.KubeconfigPath()
	server, caData, err := clusterEndpoint(kc)
	if err != nil {
		return "", err
	}

	rbac, err := renderTemplate(candidateRBAC, map[string]string{"Namespace": ns})
	if err != nil {
		return "", err
	}
	rbacPath := filepath.Join(m.Engine.Workdir, RBACFile)
	if err := os.WriteFile(rbacPath, []byte(rbac), 0o644); err != nil {
		return "", err
	}
	r := m.Engine.Runner
	if err := r.Command(ctx, "kubectl", "--kubeconfig", kc, "apply", "-f", rbacPath); err != nil {
		return "", err
	}
	token, err := r.Output(ctx, "kubectl", "--kubeconfig", kc, "-n", ns,
		"create", "token", "candidate", "--duration", "4h")
	if err != nil {
		return "", err
	}

	out, err := renderTemplate(candidateKubeconfig, map[string]string{
		"Server": server, "CAData": caData,
		"Token": strings.TrimSpace(token), "Namespace": ns,
	})
	if err != nil {
		return "", err
	}
	path := filepath.Join(m.Engine.Workdir, KubeconfigFile)
	if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
		return "", err
	}
	if opts.Out == "" {
		return path, nil
	}
	mode := opts.Mode
	if mode == 0 {
		mode = 0o600
	}
	if err := os.WriteFile(opts.Out, []byte(out), mode); err != nil {
		return "", err
	}
	// The file carries a service account token, and which accounts can read
	// it is the whole point of this copy, so do not leave it to the umask.
	if err := os.Chmod(opts.Out, mode); err != nil {
		return "", err
	}
	return opts.Out, nil
}

// clusterEndpoint pulls the server URL and CA bundle out of the
// engine-owned kubeconfig kind exported.
func clusterEndpoint(path string) (server, caData string, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	var kc struct {
		Clusters []struct {
			Cluster struct {
				Server string `yaml:"server"`
				CAData string `yaml:"certificate-authority-data"`
			} `yaml:"cluster"`
		} `yaml:"clusters"`
	}
	if err := yaml.Unmarshal(raw, &kc); err != nil {
		return "", "", fmt.Errorf("%s: %w", path, err)
	}
	if len(kc.Clusters) == 0 || kc.Clusters[0].Cluster.Server == "" {
		return "", "", fmt.Errorf("%s: no cluster server", path)
	}
	return kc.Clusters[0].Cluster.Server, kc.Clusters[0].Cluster.CAData, nil
}

func renderTemplate(text string, data map[string]string) (string, error) {
	t, err := template.New("session").Parse(text)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := t.Execute(&b, data); err != nil {
		return "", err
	}
	return b.String(), nil
}
