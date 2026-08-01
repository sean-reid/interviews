package debug

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/sean-reid/interviews/internal/content"
	"github.com/sean-reid/interviews/internal/taxonomy"
)

const kindEnv = `provider: kind
kind:
  manifests: env/manifests
  namespace: shop
  build:
    - image: shop-api
      context: app/api
verify: env/verify.sh
`

const composeEnv = `provider: compose
compose:
  file: env/docker-compose.yml
verify: env/verify.sh
`

func faultYAML(id, tier, packs string) string {
	return "id: " + id + "\ntitle: T\ntier: " + tier + "\npacks: [" + packs + "]\n"
}

// scenarioFS builds a valid kind-flavored problem tree, then applies mutate.
func scenarioFS(mutate func(fstest.MapFS)) fstest.MapFS {
	exec := &fstest.MapFile{Data: []byte("#!/bin/sh\n"), Mode: 0o755}
	fsys := fstest.MapFS{
		"problem.yaml": &fstest.MapFile{Data: []byte(`schema: 1
id: pipeline-meltdown
type: debugging
title: T
summary: S
disciplines: [systems]
levels: [mid]
flavor: kubernetes
time:
  session_minutes: 60
params:
  fault_pack:
    type: choice
    of: [pack-a, pack-b]
visibility:
  candidate: [candidate/**]
`)},
		"candidate/brief.md":              &fstest.MapFile{Data: []byte("brief")},
		"env.yaml":                        &fstest.MapFile{Data: []byte(kindEnv)},
		"env/verify.sh":                   exec,
		"env/manifests/00-ns.yaml":        &fstest.MapFile{Data: []byte("kind: Namespace")},
		"app/api/Dockerfile":              &fstest.MapFile{Data: []byte("FROM scratch")},
		"faults/01-image-typo/fault.yaml": &fstest.MapFile{Data: []byte(faultYAML("01-image-typo", "easy", "pack-a, pack-b"))},
		"faults/01-image-typo/inject.sh":  exec,
		"faults/01-image-typo/check.sh":   exec,
		"faults/01-image-typo/fix.sh":     exec,
		"faults/01-image-typo/notes.md":   &fstest.MapFile{Data: []byte("notes")},
		"faults/02-net-policy/fault.yaml": &fstest.MapFile{Data: []byte(faultYAML("02-net-policy", "hard", "pack-b") + "masks: [01-image-typo]\n")},
		"faults/02-net-policy/inject.sh":  exec,
		"faults/02-net-policy/check.sh":   exec,
		"faults/02-net-policy/fix.sh":     exec,
		"faults/02-net-policy/notes.md":   &fstest.MapFile{Data: []byte("notes")},
	}
	if mutate != nil {
		mutate(fsys)
	}
	return fsys
}

func loadScenario(t *testing.T, mutate func(fstest.MapFS)) (*Scenario, []content.Issue) {
	t.Helper()
	fsys := scenarioFS(mutate)
	p, issues := content.Load(fsys, taxonomy.Debugging, "pipeline-meltdown")
	if content.Errors(issues) {
		t.Fatalf("fixture problem invalid before scenario load: %v", issues)
	}
	return LoadScenario(p)
}

func wantScenarioIssue(t *testing.T, mutate func(fstest.MapFS), substr string) {
	t.Helper()
	_, issues := loadScenario(t, mutate)
	for _, i := range issues {
		if strings.Contains(i.Msg, substr) {
			return
		}
	}
	t.Errorf("no issue containing %q in %v", substr, issues)
}

func TestValidScenarioLoads(t *testing.T) {
	s, issues := loadScenario(t, nil)
	if len(issues) != 0 {
		t.Fatalf("issues on valid scenario: %v", issues)
	}
	if s.Env.Provider != "kind" || s.Env.Kind.Namespace != "shop" {
		t.Errorf("env not populated: %+v", s.Env)
	}
	if len(s.Faults) != 2 || s.Faults[0].Spec.ID != "01-image-typo" {
		t.Errorf("faults = %+v", s.Faults)
	}
	if got := s.PackFaults("pack-a"); len(got) != 1 || got[0].Spec.ID != "01-image-typo" {
		t.Errorf("PackFaults(pack-a) = %+v", got)
	}
	if got := s.PackFaults("pack-b"); len(got) != 2 {
		t.Errorf("PackFaults(pack-b) = %+v", got)
	}
	if got := s.Packs(); len(got) != 2 || got[0] != "pack-a" {
		t.Errorf("Packs() = %v", got)
	}
}

func TestMissingEnvManifest(t *testing.T) {
	fsys := scenarioFS(func(m fstest.MapFS) { delete(m, "env.yaml") })
	p, _ := content.Load(fsys, taxonomy.Debugging, "pipeline-meltdown")
	s, issues := LoadScenario(p)
	if s != nil || len(issues) == 0 {
		t.Error("missing env.yaml must return nil scenario with an issue")
	}
}

func TestEnvRules(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(fstest.MapFS)
		want   string
	}{
		{"unknown env field", func(m fstest.MapFS) {
			m["env.yaml"] = &fstest.MapFile{Data: []byte(kindEnv + "bogus: true\n")}
		}, "cannot decode"},
		{"wrong provider for flavor", func(m fstest.MapFS) {
			m["env.yaml"] = &fstest.MapFile{Data: []byte(strings.Replace(kindEnv, "provider: kind", "provider: compose", 1))}
		}, "not valid for flavor"},
		{"kind section missing", func(m fstest.MapFS) {
			m["env.yaml"] = &fstest.MapFile{Data: []byte("provider: kind\nverify: env/verify.sh\n")}
		}, "kind: section required"},
		{"namespace missing", func(m fstest.MapFS) {
			m["env.yaml"] = &fstest.MapFile{Data: []byte(strings.Replace(kindEnv, "  namespace: shop\n", "", 1))}
		}, "kind.namespace"},
		{"manifests dir missing", func(m fstest.MapFS) {
			delete(m, "env/manifests/00-ns.yaml")
		}, "kind.manifests"},
		{"build context missing", func(m fstest.MapFS) {
			delete(m, "app/api/Dockerfile")
		}, "kind.build[0].context"},
		{"verify missing", func(m fstest.MapFS) {
			delete(m, "env/verify.sh")
		}, "verify"},
		{"verify not executable", func(m fstest.MapFS) {
			m["env/verify.sh"] = &fstest.MapFile{Data: []byte("#!/bin/sh\n"), Mode: 0o644}
		}, "must be executable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantScenarioIssue(t, tt.mutate, tt.want)
		})
	}
}

// composeProblem turns the kind fixture into a compose-flavored one.
func composeProblem(m fstest.MapFS) {
	m["problem.yaml"] = &fstest.MapFile{Data: []byte(strings.Replace(
		string(m["problem.yaml"].Data), "flavor: kubernetes", "flavor: compose-linux", 1))}
	m["env.yaml"] = &fstest.MapFile{Data: []byte(composeEnv)}
	m["env/docker-compose.yml"] = &fstest.MapFile{Data: []byte("services: {}")}
}

func TestComposeEnvRules(t *testing.T) {
	s, issues := loadScenario(t, composeProblem)
	if len(issues) != 0 {
		t.Fatalf("issues on valid compose scenario: %v", issues)
	}
	if s.Env.Compose.File != "env/docker-compose.yml" {
		t.Errorf("compose spec = %+v", s.Env.Compose)
	}

	wantScenarioIssue(t, func(m fstest.MapFS) {
		composeProblem(m)
		delete(m, "env/docker-compose.yml")
	}, "compose.file")

	wantScenarioIssue(t, func(m fstest.MapFS) {
		composeProblem(m)
		m["env.yaml"] = &fstest.MapFile{Data: []byte(composeEnv + "kind:\n  manifests: x\n  namespace: y\n")}
	}, "kind: section set but provider is compose")
}

func TestFaultRules(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(fstest.MapFS)
		want   string
	}{
		{"no faults", func(m fstest.MapFS) {
			for k := range m {
				if strings.HasPrefix(k, "faults/") {
					delete(m, k)
				}
			}
		}, "at least one fault"},
		{"id mismatch", func(m fstest.MapFS) {
			m["faults/01-image-typo/fault.yaml"] = &fstest.MapFile{Data: []byte(faultYAML("wrong-id", "easy", "pack-a"))}
		}, "directory name"},
		{"bad tier", func(m fstest.MapFS) {
			m["faults/01-image-typo/fault.yaml"] = &fstest.MapFile{Data: []byte(faultYAML("01-image-typo", "brutal", "pack-a"))}
		}, "not easy, medium, or hard"},
		{"no packs", func(m fstest.MapFS) {
			m["faults/01-image-typo/fault.yaml"] = &fstest.MapFile{Data: []byte("id: 01-image-typo\ntitle: T\ntier: easy\npacks: []\n")}
		}, "packs: at least one"},
		{"undeclared pack", func(m fstest.MapFS) {
			m["faults/01-image-typo/fault.yaml"] = &fstest.MapFile{Data: []byte(faultYAML("01-image-typo", "easy", "pack-z"))}
		}, "not a declared fault_pack value"},
		{"unknown masked fault", func(m fstest.MapFS) {
			m["faults/02-net-policy/fault.yaml"] = &fstest.MapFile{Data: []byte(faultYAML("02-net-policy", "hard", "pack-b") + "masks: [99-ghost]\n")}
		}, "no fault named"},
		{"missing notes", func(m fstest.MapFS) {
			delete(m, "faults/01-image-typo/notes.md")
		}, "notes.md"},
		{"script not executable", func(m fstest.MapFS) {
			m["faults/01-image-typo/inject.sh"] = &fstest.MapFile{Data: []byte("#!/bin/sh\n"), Mode: 0o644}
		}, "must be executable"},
		{"stray file in faults", func(m fstest.MapFS) {
			m["faults/README.md"] = &fstest.MapFile{Data: []byte("stray")}
		}, "only contain fault directories"},
		{"negative settle", func(m fstest.MapFS) {
			m["faults/01-image-typo/fault.yaml"] = &fstest.MapFile{Data: []byte(faultYAML("01-image-typo", "easy", "pack-a") + "settle_seconds: -1\n")}
		}, "must not be negative"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantScenarioIssue(t, tt.mutate, tt.want)
		})
	}
}

func TestPackCoverageRules(t *testing.T) {
	// A pack declared in the manifest with no faults is an authoring error.
	wantScenarioIssue(t, func(m fstest.MapFS) {
		m["faults/01-image-typo/fault.yaml"] = &fstest.MapFile{Data: []byte(faultYAML("01-image-typo", "easy", "pack-b"))}
	}, `pack "pack-a" has no faults`)

	// Debugging problems must declare the fault_pack parameter at all.
	wantScenarioIssue(t, func(m fstest.MapFS) {
		m["problem.yaml"] = &fstest.MapFile{Data: []byte(strings.Replace(
			string(m["problem.yaml"].Data), "fault_pack", "some_other", 1))}
	}, "need this choice parameter")
}

// An app declaration has to say enough to be served, and only kind can be
// served for now, so a compose problem declaring one has to hear about it
// at validate time rather than at session start.
func TestValidateAppDeclaration(t *testing.T) {
	kindEnv := "provider: kind\nkind:\n  manifests: env/manifests\n  namespace: shop\nverify: env/verify.sh\n"
	for _, tc := range []struct{ env, want string }{
		{kindEnv + "app:\n  port: \"80\"\n", "app.service"},
		{kindEnv + "app:\n  service: frontend\n", "app.port"},
	} {
		env := tc.env
		wantScenarioIssue(t, func(m fstest.MapFS) {
			m["env.yaml"] = &fstest.MapFile{Data: []byte(env)}
		}, tc.want)
	}
}
