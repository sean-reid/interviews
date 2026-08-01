package grading

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	return sheetWith(t, SheetData{Manifest: m, Score: score, Hints: hints})
}

// sheetWith renders a sheet, filling in the rubric and variant every case
// shares.
func sheetWith(t *testing.T, d SheetData) string {
	t.Helper()
	r, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	d.Rubric = r
	if d.Variant == nil {
		d.Variant = &variant.Resolved{
			Problem: d.Manifest.ID, InterviewID: "calm-bison-0731",
			Params: map[string]any{"fault_pack": "pack-a", "scale": 5},
		}
	}
	var sb strings.Builder
	if err := RenderSheet(&sb, d); err != nil {
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
		At: time.Date(2026, 7, 31, 14, 5, 0, 0, time.UTC),
	}
	hints := []Hint{{Minute: 12, Text: "what does describe show you?",
		At: time.Date(2026, 7, 31, 13, 50, 0, 0, time.UTC)}}
	out := sheetFor(t, debuggingManifest(), score, hints)

	for _, want := range []string{
		"# Grading sheet: Pipeline meltdown",
		"| Interview id | calm-bison-0731 |",
		"| Problem | pipeline-meltdown (debugging/kubernetes) |",
		"| Variant | fault_pack=pack-a, scale=5 |",
		"(this problem grades: mid, senior)",
		"| 01-image-typo: Image tag typo | easy | yes | | |",
		"| 02-net-policy: Label mismatch | hard | no | | |",
		"Checks report 1/2 fixed; end-to-end verify failed, measured 2026-07-31 14:05 UTC.",
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

// The sheet must not read a check that could not run as a fault the
// candidate left unfixed.
func TestSheetSeparatesABrokenCheckFromAnUnfixedFault(t *testing.T) {
	score := &Score{
		Faults: []FaultResult{
			{ID: "01-image-typo", Title: "Image tag typo", Tier: "easy"},
			{ID: "02-net-policy", Title: "Label mismatch", Tier: "hard", CheckFailed: true},
		},
		Total: 2,
	}
	out := sheetFor(t, debuggingManifest(), score, nil)
	for _, want := range []string{
		"| 01-image-typo: Image tag typo | easy | no | | |",
		"| 02-net-policy: Label mismatch | hard | check did not run | | |",
		"1 of those checks could not run",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("sheet missing %q:\n%s", want, out)
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

// Hints are evidence. Two logged close together used to read the same
// ledger and write over each other, and one of them was simply gone.
func TestAppendHintKeepsEveryConcurrentHint(t *testing.T) {
	dir := t.TempDir()
	const writers = 8
	var start, done sync.WaitGroup
	start.Add(1)
	for i := range writers {
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()
			if err := AppendHint(dir, Hint{Minute: i, Text: fmt.Sprintf("hint %d", i)}); err != nil {
				t.Error(err)
			}
		}()
	}
	start.Done()
	done.Wait()

	hints, err := LoadHints(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(hints) != writers {
		t.Fatalf("ledger holds %d hints, want %d", len(hints), writers)
	}
	seen := map[string]bool{}
	for _, h := range hints {
		seen[h.Text] = true
	}
	for i := range writers {
		if !seen[fmt.Sprintf("hint %d", i)] {
			t.Errorf("hint %d was lost: %+v", i, hints)
		}
	}
	// The lock is a file beside the ledger; a writer that finished has to
	// have let it go.
	if _, err := os.Stat(filepath.Join(dir, HintsFile+".lock")); !os.IsNotExist(err) {
		t.Errorf("lockfile left behind: %v", err)
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

// An interviewer logs hints beside themselves and pulls the rest of the
// evidence from the session host, so the ledger has to be loadable by name
// and mergeable with whatever the host recorded.
func TestLoadHintsFromFileOrDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := AppendHint(dir, Hint{Minute: 12, Text: "asked about the lag graph"}); err != nil {
		t.Fatal(err)
	}
	byDir, err := LoadHintsFrom(dir)
	if err != nil || len(byDir) != 1 {
		t.Fatalf("LoadHintsFrom(dir) = %+v, %v", byDir, err)
	}
	byFile, err := LoadHintsFrom(filepath.Join(dir, HintsFile))
	if err != nil || len(byFile) != 1 || byFile[0].Text != byDir[0].Text {
		t.Fatalf("LoadHintsFrom(file) = %+v, %v", byFile, err)
	}
	if missing, err := LoadHintsFrom(filepath.Join(t.TempDir(), HintsFile)); err != nil || missing != nil {
		t.Fatalf("LoadHintsFrom(missing) = %+v, %v", missing, err)
	}
}

func TestMergeHints(t *testing.T) {
	host := []Hint{{Minute: 20, Text: "host"}, {Minute: 4, Text: "shared"}}
	laptop := []Hint{{Minute: 4, Text: "shared"}, {Minute: 9, Text: "laptop"}}
	got := MergeHints(host, laptop)
	if len(got) != 3 {
		t.Fatalf("MergeHints = %+v, want 3 hints", got)
	}
	for i, want := range []string{"shared", "laptop", "host"} {
		if got[i].Text != want {
			t.Errorf("hint %d = %q, want %q (order: %+v)", i, got[i].Text, want, got)
		}
	}
	// Merging one ledger with itself must not double it.
	if same := MergeHints(laptop, laptop); len(same) != 2 {
		t.Errorf("MergeHints(x, x) = %+v, want 2 hints", same)
	}
}

// A score is a reading taken at a moment; the sheet is rendered later and
// goes into a hiring decision. One that predates the last hint cannot be
// the state the session ended in, and an undated sheet reads as final.
func TestSheetFlagsAScoreOlderThanTheLastHint(t *testing.T) {
	measured := time.Date(2026, 7, 31, 14, 5, 0, 0, time.UTC)
	score := &Score{
		Faults: []FaultResult{{ID: "01-image-typo", Title: "Image tag typo", Tier: "easy"}},
		Total:  1, At: measured,
	}
	hints := []Hint{{Minute: 40, Text: "look at the events", At: measured.Add(20 * time.Minute)}}
	out := sheetFor(t, debuggingManifest(), score, hints)
	if !strings.Contains(out, "measured 2026-07-31 14:05 UTC") {
		t.Error("sheet does not say when the score was measured")
	}
	if !strings.Contains(out, "predates the last hint") {
		t.Error("sheet renders a score older than the last hint without flagging it")
	}
}

func TestSheetSaysWhenAScoreCarriesNoTimestamp(t *testing.T) {
	score := &Score{Faults: []FaultResult{{ID: "01-image-typo", Tier: "easy"}}, Total: 1}
	out := sheetFor(t, debuggingManifest(), score, nil)
	if !strings.Contains(out, "does not say when it was measured") {
		t.Error("sheet presents an undated score as if it were current")
	}
}

// The level decides how the rubric is read, and it was the one thing the
// tool would not accept: the row was a blank to fill in by hand above all
// five bands, including ones the problem does not grade.
func TestSheetTargetsOneLevel(t *testing.T) {
	out := sheetWith(t, SheetData{Manifest: debuggingManifest(), Level: taxonomy.Senior})
	if !strings.Contains(out, "| Level targeted | senior |") {
		t.Error("level not filled in")
	}
	if !strings.Contains(out, "## Calibration band\n") {
		t.Error("still headed as if it printed every band")
	}
	if !strings.Contains(out, "| senior |") {
		t.Error("the targeted band is missing")
	}
	for _, other := range []string{"| entry |", "| staff |", "| principal |", "| mid |"} {
		if strings.Contains(out, other) {
			t.Errorf("sheet prints %s, which this interview was not calibrated for", other)
		}
	}
}

func TestSheetWithoutALevelKeepsEveryBand(t *testing.T) {
	out := sheetWith(t, SheetData{Manifest: debuggingManifest()})
	if !strings.Contains(out, "(this problem grades: mid, senior)") {
		t.Error("unlevelled sheet does not say what the problem grades")
	}
	for _, l := range taxonomy.Levels {
		if !strings.Contains(out, "| "+string(l)+" |") {
			t.Errorf("band for %s missing", l)
		}
	}
}

// The sheet gets pasted into a hiring thread. A generated credential is not
// something to identify a session by.
func TestSheetRedactsSecretParameters(t *testing.T) {
	m := debuggingManifest()
	m.Params = map[string]content.ParamSpec{
		"db_password": {Type: content.Choice, Secret: true},
		"scale":       {Type: content.Int},
	}
	out := sheetWith(t, SheetData{
		Manifest: m,
		Variant: &variant.Resolved{
			Problem: m.ID, InterviewID: "calm-bison-0731",
			Params: map[string]any{"db_password": "quiet-cargo", "scale": 5},
		},
	})
	if strings.Contains(out, "quiet-cargo") {
		t.Error("sheet prints a secret parameter value")
	}
	for _, want := range []string{"db_password=(secret)", "scale=5"} {
		if !strings.Contains(out, want) {
			t.Errorf("variant line missing %q:\n%s", want, out)
		}
	}
}
