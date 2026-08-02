package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestKubeconfigAppliesRBACAndWritesConfig(t *testing.T) {
	m, r, _ := testManager(t, nil)
	wd := m.Engine.Workdir
	writeState(t, wd, "01-image-typo")
	if err := os.WriteFile(m.Engine.KubeconfigPath(), []byte(kubeconfigFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	r.outputs["create token candidate"] = "sa-token-123\n"

	path, err := m.Kubeconfig(context.Background(), KubeconfigOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(wd, KubeconfigFile) {
		t.Errorf("path = %q", path)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("workdir kubeconfig mode = %v, %v", info.Mode().Perm(), err)
	}

	kc := m.Engine.KubeconfigPath()
	rbacPath := filepath.Join(wd, RBACFile)
	for _, want := range []string{
		"kubectl --kubeconfig " + kc + " apply -f " + rbacPath,
		"kubectl --kubeconfig " + kc + " -n shop create token candidate --duration 4h",
	} {
		if len(r.callsMatching(want)) != 1 {
			t.Errorf("no call %q in %v", want, r.calls)
		}
	}

	rbac, err := os.ReadFile(rbacPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"kind: ServiceAccount", "namespace: shop",
		"resources: [pods, pods/log, services, endpoints, configmaps, secrets, events]",
		"resources: [deployments, statefulsets]",
		"resources: [pods/exec, pods/portforward]",
		"resources: [nodes]", "kind: ClusterRoleBinding",
	} {
		if !strings.Contains(string(rbac), want) {
			t.Errorf("rbac manifest missing %q", want)
		}
	}

	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"server: https://127.0.0.1:6443",
		"certificate-authority-data: Y2EtZGF0YQ==",
		"token: sa-token-123",
		"namespace: shop",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("candidate kubeconfig missing %q:\n%s", want, out)
		}
	}
}

// The host hands the candidate's account a copy outside the workdir, which
// stays unreadable to it, and the copy must not be writable there either.
func TestKubeconfigWritesASecondCopy(t *testing.T) {
	m, _, _ := testManager(t, nil)
	wd := m.Engine.Workdir
	writeState(t, wd, "01-image-typo")
	if err := os.WriteFile(m.Engine.KubeconfigPath(), []byte(kubeconfigFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "candidate.kubeconfig")

	path, err := m.Kubeconfig(context.Background(), KubeconfigOptions{Out: out, Mode: 0o640})
	if err != nil {
		t.Fatal(err)
	}
	if path != out {
		t.Errorf("path = %q, want the handed-out copy %q", path, out)
	}
	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Errorf("copy mode = %v, want 0640: group read, nobody else, no writers", info.Mode().Perm())
	}
	copied, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	inWorkdir, err := os.ReadFile(filepath.Join(wd, KubeconfigFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(copied) != string(inWorkdir) {
		t.Error("the copy and the bundled kubeconfig differ")
	}
}

func TestKubeconfigNeedsKind(t *testing.T) {
	m, _, _ := testManager(t, func(m fstest.MapFS) {
		m["problem.yaml"] = &fstest.MapFile{Data: []byte(strings.Replace(
			problemYAML, "flavor: kubernetes", "flavor: compose-linux", 1))}
		m["env.yaml"] = &fstest.MapFile{Data: []byte("provider: compose\ncompose:\n  file: env/docker-compose.yml\nverify: env/verify.sh\n")}
		m["env/docker-compose.yml"] = &fstest.MapFile{Data: []byte("services: {}")}
		delete(m, "env/manifests/00-ns.yaml")
	})
	writeState(t, m.Engine.Workdir, "01-image-typo")
	if _, err := m.Kubeconfig(context.Background(), KubeconfigOptions{}); err == nil ||
		!strings.Contains(err.Error(), "kind-flavor") {
		t.Errorf("Kubeconfig on compose = %v", err)
	}
}
