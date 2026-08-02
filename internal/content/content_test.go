package content

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"gopkg.in/yaml.v3"

	"github.com/sean-reid/interviews/internal/taxonomy"
)

// base returns a valid debugging manifest as a mutable map.
func base() map[string]any {
	return map[string]any{
		"schema":      1,
		"id":          "pipeline-meltdown",
		"type":        "debugging",
		"title":       "Pipeline meltdown",
		"summary":     "A multi-tier pipeline is down in several layered ways.",
		"disciplines": []string{"systems"},
		"levels":      []string{"mid", "senior"},
		"flavor":      "kubernetes",
		"time":        map[string]any{"session_minutes": 60},
		"params": map[string]any{
			"fault_pack": map[string]any{"type": "choice", "of": []string{"pack-a", "pack-b"}},
			"scale":      map[string]any{"type": "int", "min": 3, "max": 9},
			"team_name":  map[string]any{"type": "string", "default": "umbrella"},
		},
		"visibility": map[string]any{"candidate": []string{"candidate/**"}},
	}
}

func fsFor(t *testing.T, manifest map[string]any, files map[string]string) fstest.MapFS {
	t.Helper()
	fsys := fstest.MapFS{}
	if manifest != nil {
		raw, err := yaml.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		fsys[ManifestName] = &fstest.MapFile{Data: raw}
	}
	if files == nil {
		files = map[string]string{
			"candidate/brief.md":    "The pipeline is down. Find and fix what you can.",
			"interviewer/rubric.md": "answer key",
		}
	}
	for p, data := range files {
		fsys[p] = &fstest.MapFile{Data: []byte(data)}
	}
	return fsys
}

func load(t *testing.T, m map[string]any, files map[string]string) (*Problem, []Issue) {
	t.Helper()
	return Load(fsFor(t, m, files), taxonomy.Debugging, "pipeline-meltdown")
}

func wantIssue(t *testing.T, issues []Issue, substr string) {
	t.Helper()
	for _, i := range issues {
		if strings.Contains(i.Msg, substr) {
			if i.Warning {
				t.Errorf("issue %q is a warning, want error", i)
			}
			return
		}
	}
	t.Errorf("no issue containing %q in %v", substr, issues)
}

func TestValidProblemLoadsClean(t *testing.T) {
	p, issues := load(t, base(), nil)
	if len(issues) != 0 {
		t.Fatalf("issues on valid problem: %v", issues)
	}
	if p.Manifest.ID != "pipeline-meltdown" || p.Manifest.Flavor != taxonomy.Kubernetes {
		t.Errorf("manifest not populated: %+v", p.Manifest)
	}
}

func TestValidOfflineTypes(t *testing.T) {
	m := base()
	m["id"] = "slow-aligner"
	m["type"] = "takehome"
	m["class"] = "optimization-ladder"
	delete(m, "flavor")
	m["time"] = map[string]any{"soft_budget_hours": 6.0}
	p, issues := Load(fsFor(t, m, nil), taxonomy.TakeHome, "slow-aligner")
	if Errors(issues) {
		t.Fatalf("issues on valid takehome: %v", issues)
	}
	if p.Manifest.Class != taxonomy.OptimizationLadder {
		t.Errorf("class = %q", p.Manifest.Class)
	}

	m = base()
	m["id"] = "global-feed"
	m["type"] = "sysdesign"
	delete(m, "flavor")
	m["time"] = map[string]any{"soft_budget_hours": 4.5}
	_, issues = Load(fsFor(t, m, nil), taxonomy.SysDesign, "global-feed")
	if Errors(issues) {
		t.Fatalf("issues on valid sysdesign: %v", issues)
	}
}

func TestManifestDecodeFailures(t *testing.T) {
	if p, issues := Load(fsFor(t, nil, nil), taxonomy.Debugging, "x"); p != nil || !Errors(issues) {
		t.Error("missing manifest must be a hard error with nil problem")
	}

	fsys := fsFor(t, nil, nil)
	fsys[ManifestName] = &fstest.MapFile{Data: []byte("schema: 1\nbogus_field: true\n")}
	p, issues := Load(fsys, taxonomy.Debugging, "x")
	if p != nil || !Errors(issues) {
		t.Error("unknown manifest field must fail strict decoding")
	}
}

func TestManifestFieldRules(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(m map[string]any)
		want   string
	}{
		{"unsupported schema", func(m map[string]any) { m["schema"] = 2 }, "schema"},
		{"non-kebab id", func(m map[string]any) { m["id"] = "Bad_ID" }, "kebab"},
		{"id dir mismatch", func(m map[string]any) { m["id"] = "other-name" }, "directory name"},
		{"unknown type", func(m map[string]any) { m["type"] = "quiz" }, "not an interview type"},
		{"empty title", func(m map[string]any) { m["title"] = "" }, "title"},
		{"empty summary", func(m map[string]any) { m["summary"] = "" }, "summary"},
		{"no disciplines", func(m map[string]any) { m["disciplines"] = []string{} }, "disciplines"},
		{"unknown discipline", func(m map[string]any) { m["disciplines"] = []string{"astrology"} }, "astrology"},
		{"duplicate discipline", func(m map[string]any) { m["disciplines"] = []string{"systems", "systems"} }, "twice"},
		{"no levels", func(m map[string]any) { m["levels"] = []string{} }, "levels"},
		{"unknown level", func(m map[string]any) { m["levels"] = []string{"intern"} }, "intern"},
		{"missing flavor", func(m map[string]any) { delete(m, "flavor") }, "flavor"},
		{"unknown flavor", func(m map[string]any) { m["flavor"] = "mainframe" }, "flavor"},
		{"class on debugging", func(m map[string]any) { m["class"] = "legacy-rescue" }, "only take-home"},
		{"zero session", func(m map[string]any) { m["time"] = map[string]any{"session_minutes": 0} }, "session_minutes"},
		{"wrong time kind", func(m map[string]any) { m["time"] = map[string]any{"soft_budget_hours": 6.0} }, "session_minutes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := base()
			tt.mutate(m)
			_, issues := load(t, m, nil)
			wantIssue(t, issues, tt.want)
		})
	}
}

func TestOfflineTypeRules(t *testing.T) {
	m := base()
	m["type"] = "takehome"
	delete(m, "flavor")
	m["time"] = map[string]any{"soft_budget_hours": 6.0}
	_, issues := Load(fsFor(t, m, nil), taxonomy.TakeHome, "pipeline-meltdown")
	wantIssue(t, issues, "class: required")

	m["class"] = "trivia"
	_, issues = Load(fsFor(t, m, nil), taxonomy.TakeHome, "pipeline-meltdown")
	wantIssue(t, issues, "not a take-home class")

	m["class"] = "legacy-rescue"
	m["flavor"] = "kubernetes"
	_, issues = Load(fsFor(t, m, nil), taxonomy.TakeHome, "pipeline-meltdown")
	wantIssue(t, issues, "only debugging")

	delete(m, "flavor")
	m["time"] = map[string]any{"session_minutes": 60}
	_, issues = Load(fsFor(t, m, nil), taxonomy.TakeHome, "pipeline-meltdown")
	wantIssue(t, issues, "soft_budget_hours")
}

func TestTypeDirPlacement(t *testing.T) {
	_, issues := Load(fsFor(t, base(), nil), taxonomy.TakeHome, "pipeline-meltdown")
	wantIssue(t, issues, "lives under")
}

func TestParamRules(t *testing.T) {
	tests := []struct {
		name  string
		param map[string]any
		want  string
	}{
		{"bad_name_", map[string]any{"type": "string", "default": "x"}, "name must match"},
		{"empty_choice", map[string]any{"type": "choice", "of": []string{}}, "non-empty"},
		{"dup_choice", map[string]any{"type": "choice", "of": []string{"a", "a"}}, "twice"},
		{"choice_minmax", map[string]any{"type": "choice", "of": []string{"a"}, "min": 1}, "not min/max"},
		{"choice_bad_default", map[string]any{"type": "choice", "of": []string{"a", "b"}, "default": "c"}, "not in of"},
		{"choice_nonstring_default", map[string]any{"type": "choice", "of": []string{"a"}, "default": 3}, "must be a string"},
		{"int_missing_bound", map[string]any{"type": "int", "min": 1}, "both min and max"},
		{"int_inverted", map[string]any{"type": "int", "min": 9, "max": 3}, "min 9 > max 3"},
		{"int_with_of", map[string]any{"type": "int", "min": 1, "max": 2, "of": []string{"a"}}, "not of"},
		{"int_default_range", map[string]any{"type": "int", "min": 1, "max": 3, "default": 7}, "outside"},
		{"int_default_type", map[string]any{"type": "int", "min": 1, "max": 3, "default": "two"}, "must be an integer"},
		{"string_no_default", map[string]any{"type": "string"}, "needs a default"},
		{"string_with_of", map[string]any{"type": "string", "default": "x", "of": []string{"a"}}, "only a default"},
		{"unknown_kind", map[string]any{"type": "float"}, "not choice, int, or string"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := base()
			name := tt.name
			if name == "bad_name_" {
				name = "1bad"
			}
			m["params"] = map[string]any{name: tt.param}
			_, issues := load(t, m, nil)
			wantIssue(t, issues, tt.want)
		})
	}
}

func TestVisibilityRules(t *testing.T) {
	m := base()
	m["visibility"] = map[string]any{"candidate": []string{"bad["}}
	p, issues := load(t, m, nil)
	wantIssue(t, issues, "visibility")
	if p == nil {
		t.Fatal("problem should still load with a fail-closed classifier")
	}
	if got := p.Classifier.Globs(); len(got) != 0 {
		t.Errorf("fallback classifier has globs %v, want none", got)
	}

	m = base()
	m["visibility"] = map[string]any{"candidate": []string{"interviewer/**"}}
	_, issues = load(t, m, nil)
	wantIssue(t, issues, "never be candidate-visible")
}

func TestBriefRules(t *testing.T) {
	_, issues := load(t, base(), map[string]string{"interviewer/rubric.md": "key"})
	wantIssue(t, issues, "brief is required")

	m := base()
	m["visibility"] = map[string]any{"candidate": []string{"candidate/src/**"}}
	_, issues = load(t, m, map[string]string{
		"candidate/brief.md":    "brief",
		"candidate/src/main.go": "package main",
	})
	wantIssue(t, issues, "must be candidate-visible")
}

func TestIrregularFilesAreRejected(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "candidate"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := yaml.Marshal(base())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestName), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, BriefPath), []byte("brief"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("brief.md", filepath.Join(dir, "candidate/copy.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	_, issues := Load(os.DirFS(dir), taxonomy.Debugging, "pipeline-meltdown")
	wantIssue(t, issues, "not a regular file")
}

func TestScanIsAvailableToCallers(t *testing.T) {
	p, _ := load(t, base(), nil)
	if p.Scan == nil {
		t.Fatal("Scan not populated at load time")
	}
	if want := []string{"candidate/brief.md"}; !reflect.DeepEqual(p.Scan.Candidate, want) {
		t.Errorf("Scan.Candidate = %v, want %v", p.Scan.Candidate, want)
	}
	if len(p.Scan.Interviewer) != 2 {
		t.Errorf("Scan.Interviewer = %v, want the manifest and the rubric", p.Scan.Interviewer)
	}
}

func TestZeroMatchGlobWarns(t *testing.T) {
	m := base()
	m["visibility"] = map[string]any{"candidate": []string{"candidate/**", "starter/**"}}
	_, issues := load(t, m, nil)
	found := false
	for _, i := range issues {
		if strings.Contains(i.Msg, `"starter/**" matches no files`) {
			found = true
			if !i.Warning {
				t.Error("zero-match glob must be a warning, not an error")
			}
		}
	}
	if !found {
		t.Errorf("no zero-match warning in %v", issues)
	}
	if Errors(issues) {
		t.Errorf("warnings alone must not count as errors: %v", issues)
	}
}

// statInsensitiveFS answers Stat and Open for any casing of a stored name,
// the way APFS does, while the walk still reports the stored names.
type statInsensitiveFS struct{ fstest.MapFS }

func (f statInsensitiveFS) stored(name string) string {
	for p := range f.MapFS {
		if strings.EqualFold(p, name) {
			return p
		}
	}
	return name
}

func (f statInsensitiveFS) Open(name string) (fs.File, error) {
	return f.MapFS.Open(f.stored(name))
}

func (f statInsensitiveFS) Stat(name string) (fs.FileInfo, error) {
	return f.MapFS.Stat(f.stored(name))
}

// A capitalised brief used to validate on a Mac, because fs.Stat found it
// case-insensitively, and then bundle to nothing, because the glob match is
// case-sensitive. Validation now reads the walked names instead.
func TestBriefRulesSeeTheRealCase(t *testing.T) {
	fsys := statInsensitiveFS{fsFor(t, base(), map[string]string{
		"candidate/Brief.md":    "brief",
		"interviewer/rubric.md": "key",
	})}
	_, issues := Load(fsys, taxonomy.Debugging, "pipeline-meltdown")
	wantIssue(t, issues, "brief is required")
}
