// Package debug runs live debugging scenarios: it deploys a healthy
// environment from a problem's env spec, injects the variant's fault pack,
// checks and fixes faults, and proves in CI that every fault still breaks
// and every documented fix still works.
package debug

import (
	"bytes"
	"fmt"
	"io/fs"

	"gopkg.in/yaml.v3"

	"github.com/sean-reid/interviews/internal/taxonomy"
)

const (
	// EnvManifest declares the environment substrate, relative to the problem.
	EnvManifest = "env.yaml"
	// FaultsDir holds one directory per fault. Always interviewer-only.
	FaultsDir = "faults"
	// PackParam is the manifest parameter that selects the fault pack.
	PackParam = "fault_pack"
)

// A fault's check must test the mechanism it broke, not just whether the app
// is healthy: an end-to-end check can pass while the fault is still present,
// and it cannot tell a real fix from an injection that never took effect.
//
// faultFiles are required in every fault directory besides fault.yaml.
var faultFiles = []string{"inject.sh", "check.sh", "fix.sh", "notes.md"}

// EnvSpec is the parsed env.yaml.
type EnvSpec struct {
	Provider string       `yaml:"provider"`
	Kind     *KindSpec    `yaml:"kind,omitempty"`
	Compose  *ComposeSpec `yaml:"compose,omitempty"`
	// Verify is the health-check script: exit 0 iff the app works end to
	// end. It defines what "fixed" means for the whole scenario.
	Verify string `yaml:"verify"`
}

// KindSpec configures the kind provider.
type KindSpec struct {
	// Manifests is a directory of Kubernetes manifests, rendered as
	// templates with the resolved variant before applying.
	Manifests string `yaml:"manifests"`
	// Namespace is where the app lives; rendered as a template. Candidate
	// RBAC (stage 4) scopes here.
	Namespace string `yaml:"namespace"`
	// NodeImage pins the kind node image; empty means kind's default.
	NodeImage string `yaml:"node_image,omitempty"`
	// Build are local images built from the problem tree and loaded into
	// the cluster, so nothing depends on a registry.
	Build []BuildSpec `yaml:"build,omitempty"`
}

// BuildSpec is one locally built image.
type BuildSpec struct {
	Image   string `yaml:"image"`
	Context string `yaml:"context"`
}

// ComposeSpec configures the compose provider.
type ComposeSpec struct {
	// File is the compose file, rendered as a template.
	File string `yaml:"file"`
	// Configs is an optional directory rendered alongside the compose file,
	// for config files the services mount.
	Configs string `yaml:"configs,omitempty"`
}

// Tier is a fault's difficulty band.
type Tier string

// Fault difficulty tiers.
const (
	Easy   Tier = "easy"
	Medium Tier = "medium"
	Hard   Tier = "hard"
)

// Tiers lists the fault tiers in ascending difficulty.
var Tiers = []Tier{Easy, Medium, Hard}

// FaultSpec is the parsed fault.yaml.
type FaultSpec struct {
	ID    string `yaml:"id"`
	Title string `yaml:"title"`
	Tier  Tier   `yaml:"tier"`
	// Packs are the fault_pack values that include this fault.
	Packs []string `yaml:"packs"`
	// Masks are fault ids whose symptoms this fault hides while unfixed.
	// Interviewer documentation and prove-order sanity, not machinery.
	Masks []string `yaml:"masks,omitempty"`
	// SettleSeconds is the grace period after inject before the broken
	// check runs; some faults take a moment to surface.
	SettleSeconds int `yaml:"settle_seconds,omitempty"`
	// FixTimeoutSeconds bounds convergence polling after fix.sh.
	FixTimeoutSeconds int `yaml:"fix_timeout_seconds,omitempty"`
}

// Fault is one loaded fault directory.
type Fault struct {
	Spec FaultSpec
	Dir  string // faults/<id>, relative to the problem root
}

// Script returns the path of one of the fault's scripts, problem-relative.
func (f Fault) Script(name string) string { return f.Dir + "/" + name }

// providersByFlavor maps each debugging flavor to the providers that can
// host it. env.yaml must pick one of its flavor's providers.
var providersByFlavor = map[taxonomy.Flavor][]string{
	taxonomy.Kubernetes:   {"kind"},
	taxonomy.ComposeLinux: {"compose"},
}

func decodeStrict(raw []byte, out any) error {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	return dec.Decode(out)
}

func readYAML(fsys fs.FS, path string, out any) error {
	raw, err := fs.ReadFile(fsys, path)
	if err != nil {
		return fmt.Errorf("missing %s", path)
	}
	if err := decodeStrict(raw, out); err != nil {
		return fmt.Errorf("cannot decode %s: %v", path, err)
	}
	return nil
}
