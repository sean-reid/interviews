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
	}
	declared := map[string]bool{"team_name": true, "scale": true}
	files := []string{
		"candidate/brief.md", "candidate/typo.md", "candidate/broken.md",
		"candidate/nested.md", "candidate/dataset.bin",
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
