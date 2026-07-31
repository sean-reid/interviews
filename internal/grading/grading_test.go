package grading

import (
	"strings"
	"testing"
	"time"

	"github.com/sean-reid/interviews/internal/content"
	"github.com/sean-reid/interviews/internal/taxonomy"
	"github.com/sean-reid/interviews/internal/variant"
)

func TestDefaultRubricIsValid(t *testing.T) {
	r, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Dimensions) != 5 {
		t.Errorf("dimensions = %d, want 5", len(r.Dimensions))
	}
}

func TestParseRejections(t *testing.T) {
	valid := string(defaultRubric)
	tests := []struct {
		name   string
		mutate func(string) string
		want   string
	}{
		{"bad version", func(s string) string {
			return strings.Replace(s, "version: 1", "version: 9", 1)
		}, "not supported"},
		{"unknown field", func(s string) string {
			return s + "\nbogus: true\n"
		}, "cannot decode"},
		{"missing anchor", func(s string) string {
			return strings.Replace(s, "      4: Maintains an explicit map", "      5: Maintains an explicit map", 1)
		}, "anchor 4 is missing"},
		{"duplicate dimension", func(s string) string {
			return strings.Replace(s, "key: evidence", "key: decomposition", 1)
		}, "defined twice"},
		{"missing level band", func(s string) string {
			return strings.Replace(s, "  principal:", "  # principal:", 1)
		}, `no calibration band for "principal"`},
		{"missing type prompts", func(s string) string {
			return strings.Replace(s, "  sysdesign:", "  sysdesign_x:", 1)
		}, "not an interview type"},
		{"wrangling removed", func(s string) string {
			return strings.Replace(s, "key: wrangling", "key: tooling", 1)
		}, "wrangling"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.mutate(valid)))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Parse = %v, want error containing %q", err, tt.want)
			}
		})
	}
}

func sheetFor(t *testing.T, m content.Manifest, score *Score, hints []Hint) string {
	t.Helper()
	r, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	v := &variant.Resolved{
		Problem: m.ID, InterviewID: "calm-bison-0731",
		Params: map[string]any{"fault_pack": "pack-a", "scale": 5},
	}
	var sb strings.Builder
	if err := RenderSheet(&sb, SheetData{Rubric: r, Manifest: m, Variant: v, Score: score, Hints: hints}); err != nil {
		t.Fatal(err)
	}
	return sb.String()
}

func debuggingManifest() content.Manifest {
	return content.Manifest{
		ID: "pipeline-meltdown", Type: taxonomy.Debugging, Title: "Pipeline meltdown",
		Flavor: taxonomy.Kubernetes,
		Levels: []taxonomy.Level{taxonomy.Mid, taxonomy.Senior},
	}
}

func TestSheetWithScoreAndHints(t *testing.T) {
	score := &Score{
		Faults: []FaultResult{
			{ID: "01-image-typo", Title: "Image tag typo", Tier: "easy", Fixed: true},
			{ID: "02-net-policy", Title: "Label mismatch", Tier: "hard", Fixed: false},
		},
		Fixed: 1, Total: 2, Verified: false,
	}
	hints := []Hint{{Minute: 12, Text: "what does describe show you?", At: time.Now()}}
	out := sheetFor(t, debuggingManifest(), score, hints)

	for _, want := range []string{
		"# Grading sheet: Pipeline meltdown",
		"| Interview id | calm-bison-0731 |",
		"| Problem | pipeline-meltdown (debugging/kubernetes) |",
		"| Variant | fault_pack=pack-a, scale=5 |",
		"(this problem grades: mid, senior)",
		"| 01-image-typo: Image tag typo | easy | yes | | |",
		"| 02-net-policy: Label mismatch | hard | no | | |",
		"Checks report 1/2 fixed; end-to-end verify failed.",
		"### Tool and AI wrangling - score: __",
		"- **4**: Orchestrates tools deliberately",
		"| senior | 3s across the board or better",
		"| 12 | what does describe show you? |",
		"judge method,\nnot completion",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("sheet missing %q", want)
		}
	}
}

func TestSheetWithoutScorePointsAtScore(t *testing.T) {
	out := sheetFor(t, debuggingManifest(), nil, nil)
	if !strings.Contains(out, "interviews grade score") {
		t.Error("debugging sheet without a score should say how to fill it")
	}
}

func TestSheetForOfflineTypes(t *testing.T) {
	m := content.Manifest{
		ID: "slow-aligner", Type: taxonomy.TakeHome, Title: "Slow aligner",
		Class:  taxonomy.OptimizationLadder,
		Levels: []taxonomy.Level{taxonomy.Senior},
	}
	out := sheetFor(t, m, nil, nil)
	if !strings.Contains(out, "stopping-point writeup") {
		t.Error("take-home sheet missing the artifact section")
	}
	if !strings.Contains(out, "(takehome/optimization-ladder)") {
		t.Error("take-home sheet missing the class")
	}
	if strings.Contains(out, "interviews grade score") {
		t.Error("offline sheet should not reference the live scorer")
	}
}

func TestScoreAndHintsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if s, err := LoadScore(dir); err != nil || s != nil {
		t.Fatalf("LoadScore on empty dir = %v, %v", s, err)
	}
	want := &Score{Problem: "p", InterviewID: "i", Pack: "pack-a", Fixed: 1, Total: 2, At: time.Now().UTC()}
	if err := WriteScore(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadScore(dir)
	if err != nil || got == nil || got.Fixed != 1 || got.Pack != "pack-a" {
		t.Fatalf("LoadScore = %+v, %v", got, err)
	}

	if err := AppendHint(dir, Hint{Minute: 5, Text: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := AppendHint(dir, Hint{Minute: 9, Text: "b"}); err != nil {
		t.Fatal(err)
	}
	hints, err := LoadHints(dir)
	if err != nil || len(hints) != 2 || hints[1].Text != "b" {
		t.Fatalf("LoadHints = %+v, %v", hints, err)
	}
}
