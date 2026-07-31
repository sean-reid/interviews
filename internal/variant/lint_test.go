package variant

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestCheckTemplates(t *testing.T) {
	fsys := fstest.MapFS{
		"candidate/brief.md":    {Data: []byte("Team {{.team_name}} runs {{.scale}} nodes.")},
		"candidate/typo.md":     {Data: []byte("Scale is {{.scal}}.")},
		"candidate/broken.md":   {Data: []byte("Unclosed {{.team_name")},
		"candidate/nested.md":   {Data: []byte("{{if .team_name}}{{range .missing_one}}x{{end}}{{end}}")},
		"candidate/dataset.bin": {Data: []byte("{{.not_checked}}")},
		"candidate/table.csv":   {Data: []byte("nodes,{{.scal}}")},
		"candidate/Makefile":    {Data: []byte("run: # {{.scal}}")},
		"candidate/page.html":   {Data: []byte("<p>{{.scal}}</p>")},
	}
	declared := map[string]bool{"team_name": true, "scale": true}
	files := []string{
		"candidate/brief.md", "candidate/typo.md", "candidate/broken.md",
		"candidate/nested.md", "candidate/dataset.bin",
		"candidate/table.csv", "candidate/Makefile", "candidate/page.html",
	}

	issues := CheckTemplates(fsys, files, declared)
	byPath := map[string]string{}
	for _, i := range issues {
		byPath[i.Path] = i.Msg
	}

	if _, ok := byPath["candidate/brief.md"]; ok {
		t.Errorf("valid brief flagged: %s", byPath["candidate/brief.md"])
	}
	if msg := byPath["candidate/typo.md"]; !strings.Contains(msg, "{{.scal}}") {
		t.Errorf("typo not reported, got %q", msg)
	}
	if msg := byPath["candidate/broken.md"]; !strings.Contains(msg, "does not parse") {
		t.Errorf("unparseable template not reported, got %q", msg)
	}
	if msg := byPath["candidate/nested.md"]; !strings.Contains(msg, "missing_one") {
		t.Errorf("undeclared param inside if/range not reported, got %q", msg)
	}
	if _, ok := byPath["candidate/dataset.bin"]; ok {
		t.Error("non-text file was template-checked")
	}
	// Everything the bundler renders must be checked, including files the
	// old list skipped: csv and extensionless.
	if msg := byPath["candidate/table.csv"]; !strings.Contains(msg, "{{.scal}}") {
		t.Errorf("csv typo not reported, got %q", msg)
	}
	if msg := byPath["candidate/Makefile"]; !strings.Contains(msg, "{{.scal}}") {
		t.Errorf("extensionless typo not reported, got %q", msg)
	}
	if msg := byPath["candidate/page.html"]; !strings.Contains(msg, "{{.scal}}") {
		t.Errorf("html typo not reported, got %q", msg)
	}
}

// The linter reads a template's fields through every form the parser
// produces, and it checks both branches of a conditional: it has no
// parameter values, so which branch a real render takes is unknown here.
func TestCheckTemplatesFieldForms(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string // substring of the expected issue; empty means none
	}{
		{"plain field", "Scale {{.scale}}.", ""},
		{"chained field access", "{{(.mystery).x}}", "{{.mystery}}"},
		{"chained declared field", "{{(.team_name).x}}", ""},
		{"chain through a pipeline", `{{(.mystery).x | printf "%v"}}`, "{{.mystery}}"},
		{"else branch", "{{if .team_name}}a{{else}}{{.mystery}}{{end}}", "{{.mystery}}"},
		{"both branches declared", "{{if .team_name}}{{.scale}}{{else}}{{.team_name}}{{end}}", ""},
	}
	declared := map[string]bool{"team_name": true, "scale": true}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fsys := fstest.MapFS{"candidate/brief.md": {Data: []byte(tt.body)}}
			issues := CheckTemplates(fsys, []string{"candidate/brief.md"}, declared)
			if tt.want == "" {
				if len(issues) != 0 {
					t.Fatalf("issues = %v, want none", issues)
				}
				return
			}
			if len(issues) != 1 || !strings.Contains(issues[0].Msg, tt.want) {
				t.Fatalf("issues = %v, want one containing %q", issues, tt.want)
			}
		})
	}
}

func TestIsTemplated(t *testing.T) {
	for name, want := range map[string]bool{
		"candidate/brief.md":       true,
		"harness/run.sh":           true,
		"candidate/gen.py":         true,
		"candidate/main.go":        true,
		"candidate/rows.CSV":       true,
		"candidate/Makefile":       true,
		"candidate/index.html":     true,
		"candidate/app.js":         true,
		"candidate/store.ts":       true,
		"candidate/Panel.tsx":      true,
		"candidate/schema.sql":     true,
		"candidate/main.tf":        true,
		"candidate/pyproject.toml": true,
		"candidate/nginx.conf":     true,
		"candidate/pom.xml":        true,
		"candidate/site.css":       true,
		"candidate/data.bin":       false,
		"candidate/img.png":        false,
	} {
		if got := IsTemplated(name); got != want {
			t.Errorf("IsTemplated(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestCheckTemplatesNoParamsDeclared(t *testing.T) {
	fsys := fstest.MapFS{
		"candidate/brief.md": {Data: []byte("No parameters here at all.")},
		"candidate/uses.md":  {Data: []byte("But {{.something}} here.")},
	}
	issues := CheckTemplates(fsys, []string{"candidate/brief.md", "candidate/uses.md"}, nil)
	if len(issues) != 1 || issues[0].Path != "candidate/uses.md" {
		t.Errorf("issues = %v, want one for candidate/uses.md", issues)
	}
}

func TestCheckTemplatesUnreadableFile(t *testing.T) {
	issues := CheckTemplates(fstest.MapFS{}, []string{"candidate/gone.md"}, nil)
	if len(issues) != 1 || !strings.Contains(issues[0].Msg, "cannot read") {
		t.Errorf("issues = %v, want a cannot-read issue", issues)
	}
}
