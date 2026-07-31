// Package registry discovers every problem in a content root and holds the
// cross-problem invariants: the root contains only the known type
// directories, and problem ids are unique across all types.
package registry

import (
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	"github.com/sean-reid/interviews/internal/content"
	"github.com/sean-reid/interviews/internal/debug"
	"github.com/sean-reid/interviews/internal/sysdesign"
	"github.com/sean-reid/interviews/internal/takehome"
	"github.com/sean-reid/interviews/internal/taxonomy"
	"github.com/sean-reid/interviews/internal/variant"
)

// Entry is one discovered problem directory.
type Entry struct {
	Dir     string // path within the content root, e.g. debugging/pipeline-meltdown
	Type    taxonomy.Type
	Problem *content.Problem // nil only when the manifest failed to decode
}

// Finding is one validation issue located within the content root.
type Finding struct {
	Dir string // problem dir, or "." for root-level findings
	content.Issue
}

func (f Finding) String() string {
	return fmt.Sprintf("%s/%s", f.Dir, f.Issue)
}

// Errors reports whether findings contains at least one non-warning.
func Errors(findings []Finding) bool {
	return slices.ContainsFunc(findings, func(f Finding) bool { return !f.Warning })
}

// Registry is a loaded content root.
type Registry struct {
	entries  []*Entry
	byID     map[string]*Entry
	findings []Finding
}

// Load walks the content root. I/O failures return an error; everything
// wrong with the content itself lands in Findings so a validate run can
// report all of it at once.
func Load(fsys fs.FS) (*Registry, error) {
	r := &Registry{byID: map[string]*Entry{}}

	rootEntries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("reading content root: %w", err)
	}
	for _, e := range rootEntries {
		if !taxonomy.ValidType(taxonomy.Type(e.Name())) {
			r.findings = append(r.findings, Finding{Dir: ".", Issue: content.Issue{
				Path: e.Name(),
				Msg:  "content root may only contain the type directories",
			}})
		}
	}

	for _, t := range taxonomy.Types {
		if err := r.loadType(fsys, t); err != nil {
			return nil, err
		}
	}

	slices.SortFunc(r.entries, func(a, b *Entry) int { return strings.Compare(a.Dir, b.Dir) })
	return r, nil
}

func (r *Registry) loadType(fsys fs.FS, t taxonomy.Type) error {
	dirs, err := fs.ReadDir(fsys, string(t))
	if err != nil {
		// A content root need not have every type yet.
		return nil
	}
	for _, d := range dirs {
		dir := path.Join(string(t), d.Name())
		if !d.IsDir() {
			r.findings = append(r.findings, Finding{Dir: string(t), Issue: content.Issue{
				Path: d.Name(),
				Msg:  "type directories may only contain problem directories",
			}})
			continue
		}
		sub, err := fs.Sub(fsys, dir)
		if err != nil {
			return fmt.Errorf("opening %s: %w", dir, err)
		}
		problem, issues := content.Load(sub, t, d.Name())
		for _, i := range issues {
			r.findings = append(r.findings, Finding{Dir: dir, Issue: i})
		}
		if problem != nil {
			declared := make(map[string]bool, len(problem.Manifest.Params))
			for name := range problem.Manifest.Params {
				declared[name] = true
			}
			for _, ti := range variant.CheckTemplates(sub, problem.Scan.Candidate, declared) {
				r.findings = append(r.findings, Finding{Dir: dir,
					Issue: content.Issue{Path: ti.Path, Msg: ti.Msg}})
			}
			switch t {
			case taxonomy.Debugging:
				_, scenarioIssues := debug.LoadScenario(problem)
				for _, i := range scenarioIssues {
					r.findings = append(r.findings, Finding{Dir: dir, Issue: i})
				}
			case taxonomy.SysDesign:
				_, reviewIssues := sysdesign.Load(problem)
				for _, i := range reviewIssues {
					r.findings = append(r.findings, Finding{Dir: dir, Issue: i})
				}
			case taxonomy.TakeHome:
				for _, i := range takehome.Validate(problem) {
					r.findings = append(r.findings, Finding{Dir: dir, Issue: i})
				}
			}
		}
		entry := &Entry{Dir: dir, Type: t, Problem: problem}
		r.entries = append(r.entries, entry)
		if problem == nil {
			continue
		}
		id := problem.Manifest.ID
		if prev, ok := r.byID[id]; ok {
			r.findings = append(r.findings, Finding{Dir: dir, Issue: content.Issue{
				Path: content.ManifestName,
				Msg:  fmt.Sprintf("id %q already used by %s", id, prev.Dir),
			}})
			continue
		}
		r.byID[id] = entry
	}
	return nil
}

// Problems returns every entry, sorted by directory.
func (r *Registry) Problems() []*Entry { return r.entries }

// Get returns the entry whose manifest id is id.
func (r *Registry) Get(id string) (*Entry, bool) {
	e, ok := r.byID[id]
	return e, ok
}

// Findings returns every validation issue found at load time.
func (r *Registry) Findings() []Finding { return r.findings }
