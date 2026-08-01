package debug

import (
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strings"

	"github.com/sean-reid/interviews/internal/content"
)

var faultIDRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Scenario is a debugging problem's executable half: the environment spec
// and its faults, loaded and cross-checked against the manifest.
type Scenario struct {
	Problem *content.Problem
	Env     EnvSpec
	// Faults are sorted by id, and that order is the order they inject and
	// fix in. Numeric prefixes therefore carry meaning: a fault that takes a
	// dependency down has to sort before one whose inject or fix needs that
	// dependency up, or injecting the pack fails partway.
	Faults []Fault
}

// PackFaults returns the faults belonging to one fault_pack value, in
// injection order.
func (s *Scenario) PackFaults(pack string) []Fault {
	var out []Fault
	for _, f := range s.Faults {
		if slices.Contains(f.Spec.Packs, pack) {
			out = append(out, f)
		}
	}
	return out
}

// Packs returns the declared fault_pack values from the manifest.
func (s *Scenario) Packs() []string {
	return s.Problem.Manifest.Params[PackParam].Of
}

// Fault returns one fault by id.
func (s *Scenario) Fault(id string) (Fault, bool) {
	for _, f := range s.Faults {
		if f.Spec.ID == id {
			return f, true
		}
	}
	return Fault{}, false
}

// LoadScenario reads env.yaml and every fault directory. Like content.Load
// it collects all issues instead of failing fast; the Scenario is nil only
// when env.yaml itself cannot be read.
func LoadScenario(p *content.Problem) (*Scenario, []content.Issue) {
	var issues []content.Issue
	addIssue := func(path, format string, args ...any) {
		issues = append(issues, content.Issue{Path: path, Msg: fmt.Sprintf(format, args...)})
	}

	var env EnvSpec
	if err := readYAML(p.FS, EnvManifest, &env); err != nil {
		return nil, []content.Issue{{Path: EnvManifest, Msg: err.Error()}}
	}
	s := &Scenario{Problem: p, Env: env}

	allowed := providersByFlavor[p.Manifest.Flavor]
	if !slices.Contains(allowed, env.Provider) {
		addIssue(EnvManifest, "provider: %q is not valid for flavor %s (want one of %v)",
			env.Provider, p.Manifest.Flavor, allowed)
	}
	switch env.Provider {
	case "kind":
		if env.Compose != nil {
			addIssue(EnvManifest, "compose: section set but provider is kind")
		}
		if env.Kind == nil {
			addIssue(EnvManifest, "kind: section required for the kind provider")
		} else {
			if env.Kind.Namespace == "" {
				addIssue(EnvManifest, "kind.namespace: required")
			}
			issues = append(issues, requireDir(p.FS, env.Kind.Manifests, "kind.manifests")...)
			for i, b := range env.Kind.Build {
				if b.Image == "" {
					addIssue(EnvManifest, "kind.build[%d].image: required", i)
				}
				issues = append(issues, requireDir(p.FS, b.Context, fmt.Sprintf("kind.build[%d].context", i))...)
			}
		}
	case "compose":
		if env.Kind != nil {
			addIssue(EnvManifest, "kind: section set but provider is compose")
		}
		if env.Compose == nil {
			addIssue(EnvManifest, "compose: section required for the compose provider")
		} else {
			issues = append(issues, requireFile(p.FS, env.Compose.File, "compose.file")...)
			if env.Compose.Configs != "" {
				issues = append(issues, requireDir(p.FS, env.Compose.Configs, "compose.configs")...)
			}
		}
	}
	issues = append(issues, requireExecutable(p.FS, env.Verify, "verify")...)
	if app := env.App; app != nil {
		if app.Port == "" {
			addIssue(EnvManifest, "app.port: required, since it is what the candidate's browser reaches")
		}
		switch env.Provider {
		case "kind":
			if app.Service == "" {
				addIssue(EnvManifest, "app.service: required on kind, to know what to forward to")
			}
		case "compose":
			if app.Service != "" {
				addIssue(EnvManifest, "app.service: kind only; a compose app publishes app.port itself")
			}
		}
	}

	issues = append(issues, s.loadFaults()...)
	issues = append(issues, s.checkPacks()...)
	return s, issues
}

func (s *Scenario) loadFaults() []content.Issue {
	var issues []content.Issue
	p := s.Problem

	entries, err := fs.ReadDir(p.FS, FaultsDir)
	if err != nil {
		return []content.Issue{{Path: FaultsDir, Msg: "a debugging problem needs at least one fault"}}
	}
	for _, e := range entries {
		dir := FaultsDir + "/" + e.Name()
		if !e.IsDir() {
			issues = append(issues, content.Issue{Path: dir, Msg: "faults/ may only contain fault directories"})
			continue
		}
		var spec FaultSpec
		if err := readYAML(p.FS, dir+"/fault.yaml", &spec); err != nil {
			issues = append(issues, content.Issue{Path: dir + "/fault.yaml", Msg: err.Error()})
			continue
		}
		f := Fault{Spec: spec, Dir: dir}
		issues = append(issues, validateFault(p.FS, f, e.Name())...)
		s.Faults = append(s.Faults, f)
	}
	if len(s.Faults) == 0 && len(issues) == 0 {
		issues = append(issues, content.Issue{Path: FaultsDir, Msg: "a debugging problem needs at least one fault"})
	}
	slices.SortFunc(s.Faults, func(a, b Fault) int {
		return strings.Compare(a.Spec.ID, b.Spec.ID)
	})
	return issues
}

func validateFault(fsys fs.FS, f Fault, dirName string) []content.Issue {
	var issues []content.Issue
	manifest := f.Dir + "/fault.yaml"
	add := func(format string, args ...any) {
		issues = append(issues, content.Issue{Path: manifest, Msg: fmt.Sprintf(format, args...)})
	}
	if !faultIDRe.MatchString(f.Spec.ID) {
		add("id: %q must be kebab-case", f.Spec.ID)
	} else if f.Spec.ID != dirName {
		add("id: %q must equal the directory name %q", f.Spec.ID, dirName)
	}
	if f.Spec.Title == "" {
		add("title: required")
	}
	if !slices.Contains(Tiers, f.Spec.Tier) {
		add("tier: %q is not easy, medium, or hard", f.Spec.Tier)
	}
	if len(f.Spec.Packs) == 0 {
		add("packs: at least one required")
	}
	if f.Spec.SettleSeconds < 0 || f.Spec.FixTimeoutSeconds < 0 {
		add("settle_seconds and fix_timeout_seconds must not be negative")
	}
	for _, name := range faultFiles {
		path := f.Script(name)
		if name == "notes.md" {
			issues = append(issues, requireFile(fsys, path, name)...)
		} else {
			issues = append(issues, requireExecutable(fsys, path, name)...)
		}
	}
	return issues
}

// checkPacks cross-checks fault pack membership against the manifest's
// fault_pack parameter: both halves of that contract live in one problem
// and must agree.
func (s *Scenario) checkPacks() []content.Issue {
	var issues []content.Issue
	spec, ok := s.Problem.Manifest.Params[PackParam]
	if !ok || spec.Type != content.Choice {
		return []content.Issue{{Path: content.ManifestName,
			Msg: fmt.Sprintf("params.%s: debugging problems need this choice parameter to select the fault pack", PackParam)}}
	}
	declared := spec.Of
	for _, f := range s.Faults {
		for _, pack := range f.Spec.Packs {
			if !slices.Contains(declared, pack) {
				issues = append(issues, content.Issue{Path: f.Dir + "/fault.yaml",
					Msg: fmt.Sprintf("packs: %q is not a declared %s value %v", pack, PackParam, declared)})
			}
		}
		for _, masked := range f.Spec.Masks {
			if _, ok := s.Fault(masked); !ok {
				issues = append(issues, content.Issue{Path: f.Dir + "/fault.yaml",
					Msg: fmt.Sprintf("masks: no fault named %q", masked)})
			}
		}
	}
	for _, pack := range declared {
		if len(s.PackFaults(pack)) == 0 {
			issues = append(issues, content.Issue{Path: content.ManifestName,
				Msg: fmt.Sprintf("params.%s: pack %q has no faults", PackParam, pack)})
		}
	}
	return issues
}

func requireFile(fsys fs.FS, path, field string) []content.Issue {
	if path == "" {
		return []content.Issue{{Path: EnvManifest, Msg: field + ": required"}}
	}
	info, err := fs.Stat(fsys, path)
	if err != nil || info.IsDir() {
		return []content.Issue{{Path: path, Msg: field + ": file does not exist"}}
	}
	return nil
}

func requireDir(fsys fs.FS, path, field string) []content.Issue {
	if path == "" {
		return []content.Issue{{Path: EnvManifest, Msg: field + ": required"}}
	}
	entries, err := fs.ReadDir(fsys, path)
	if err != nil || len(entries) == 0 {
		return []content.Issue{{Path: path, Msg: field + ": directory missing or empty"}}
	}
	return nil
}

func requireExecutable(fsys fs.FS, path, field string) []content.Issue {
	if issues := requireFile(fsys, path, field); issues != nil {
		return issues
	}
	info, _ := fs.Stat(fsys, path)
	if info.Mode()&0o111 == 0 {
		return []content.Issue{{Path: path, Msg: field + ": must be executable"}}
	}
	return nil
}
