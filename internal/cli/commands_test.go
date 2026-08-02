package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

const goodRoot = "testdata/content"

func TestListTable(t *testing.T) {
	code, stdout, stderr := run(t, "list", "--content", goodRoot)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	for _, want := range []string{
		"ID", "TYPE", "KIND", "LEVELS", "TITLE",
		"pipeline-meltdown", "kubernetes", "mid,senior",
		"slow-aligner", "optimization-ladder",
		"global-feed", "sysdesign",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("list output missing %q:\n%s", want, stdout)
		}
	}
	// sysdesign has neither flavor nor class, so its kind column is a dash.
	for _, line := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(line, "global-feed") && !strings.Contains(line, " - ") {
			t.Errorf("sysdesign kind column not blanked: %q", line)
		}
	}
}

// list is read in an ordinary terminal, and with real content it grew to
// 149 columns, which stops being a table at all. The title is the elastic
// column: everything else is identity.
func TestListStaysInsideTheWidthBudget(t *testing.T) {
	items := []listItem{
		{
			ID: "pipeline-meltdown", Type: "debugging", Flavor: "kubernetes",
			Levels: []string{"junior", "mid", "senior", "staff"},
			Title:  strings.Repeat("a title that runs on and on ", 6),
		},
		{ID: "slow-aligner", Type: "takehome", Class: "optimization-ladder", Levels: []string{"mid"}, Title: "Short"},
	}
	var out strings.Builder
	if err := renderList(&out, items); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		if len(line) > listWidth {
			t.Errorf("row is %d columns, over the %d budget: %q", len(line), listWidth, line)
		}
	}
	if !strings.Contains(out.String(), "...") {
		t.Errorf("the long title was not marked as cut:\n%s", out.String())
	}
	// A short title is never touched.
	if !strings.Contains(out.String(), "Short") {
		t.Errorf("short title mangled:\n%s", out.String())
	}
}

func TestListJSONShape(t *testing.T) {
	code, stdout, stderr := run(t, "list", "--content", goodRoot, "--json")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	var items []listItem
	if err := json.Unmarshal([]byte(stdout), &items); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, stdout)
	}
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3", len(items))
	}
	// Sorted by directory: debugging, sysdesign, takehome.
	if items[0].ID != "pipeline-meltdown" || items[1].ID != "global-feed" || items[2].ID != "slow-aligner" {
		t.Errorf("unexpected order: %v", []string{items[0].ID, items[1].ID, items[2].ID})
	}
	if items[0].Flavor != "kubernetes" || items[0].Class != "" {
		t.Errorf("debugging item kinds wrong: %+v", items[0])
	}
	if items[2].Class != "optimization-ladder" || items[2].Flavor != "" {
		t.Errorf("takehome item kinds wrong: %+v", items[2])
	}
	if got := items[0].Disciplines; len(got) != 2 || got[0] != "systems" {
		t.Errorf("disciplines = %v", got)
	}
}

func TestListTypeFilter(t *testing.T) {
	code, stdout, _ := run(t, "list", "--content", goodRoot, "--type", "takehome", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var items []listItem
	if err := json.Unmarshal([]byte(stdout), &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "slow-aligner" {
		t.Errorf("filtered list = %+v", items)
	}

	if code, _, stderr := run(t, "list", "--content", goodRoot, "--type", "quiz"); code != 2 {
		t.Errorf("unknown type: exit %d, stderr %q", code, stderr)
	}
}

func TestListMissingRoot(t *testing.T) {
	code, _, stderr := run(t, "list", "--content", "testdata/nope")
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if !strings.Contains(stderr, "content root") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestDescribeText(t *testing.T) {
	code, stdout, stderr := run(t, "describe", "--content", goodRoot, "pipeline-meltdown")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	for _, want := range []string{
		"pipeline-meltdown - Pipeline meltdown (debugging/kubernetes)",
		"disciplines: systems, infra",
		"levels: mid, senior",
		"time: 60 minute session",
		"files: 1 candidate-visible, 20 interviewer-only",
		"fault_pack: choice of pack-a, pack-b",
		"scale: int 3..9",
		"team_name: string (default umbrella)",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("describe output missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "variant for") {
		t.Error("variant shown without --seed")
	}
}

func TestDescribeSoftBudget(t *testing.T) {
	_, stdout, _ := run(t, "describe", "--content", goodRoot, "global-feed")
	if !strings.Contains(stdout, "time: 4 hour soft budget") {
		t.Errorf("soft budget not rendered:\n%s", stdout)
	}
}

func TestDescribeWithSeedIsDeterministic(t *testing.T) {
	_, first, _ := run(t, "describe", "--content", goodRoot, "pipeline-meltdown", "--seed", "calm-bison-0731", "--json")
	_, second, _ := run(t, "describe", "--content", goodRoot, "pipeline-meltdown", "--seed", "calm-bison-0731", "--json")
	if first != second {
		t.Error("same seed produced different output")
	}
	var out describeOut
	if err := json.Unmarshal([]byte(first), &out); err != nil {
		t.Fatal(err)
	}
	if out.Variant == nil {
		t.Fatal("no variant in output")
	}
	if out.Variant.InterviewID != "calm-bison-0731" {
		t.Errorf("interview id = %q", out.Variant.InterviewID)
	}
	if out.Variant.Params["team_name"] != "umbrella" {
		t.Errorf("pinned default not applied: %v", out.Variant.Params)
	}
	scale, ok := out.Variant.Params["scale"].(float64) // JSON numbers
	if !ok || scale < 3 || scale > 9 {
		t.Errorf("scale = %v, want 3..9", out.Variant.Params["scale"])
	}
}

func TestDescribeOverrides(t *testing.T) {
	_, stdout, _ := run(t, "describe", "--content", goodRoot, "pipeline-meltdown",
		"--seed", "s", "--set", "scale=7", "--set", "fault_pack=pack-b", "--json")
	var out describeOut
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatal(err)
	}
	if out.Variant.Params["scale"].(float64) != 7 || out.Variant.Params["fault_pack"] != "pack-b" {
		t.Errorf("overrides not applied: %v", out.Variant.Params)
	}
	if out.Variant.Overrides["scale"] != "7" {
		t.Errorf("overrides not recorded: %v", out.Variant.Overrides)
	}

	if code, _, stderr := run(t, "describe", "--content", goodRoot, "pipeline-meltdown",
		"--seed", "s", "--set", "scale=99"); code != 1 || !strings.Contains(stderr, "outside") {
		t.Errorf("out-of-range override: exit %d, stderr %q", code, stderr)
	}
	if code, _, stderr := run(t, "describe", "--content", goodRoot, "pipeline-meltdown",
		"--seed", "s", "--set", "noequals"); code != 2 || !strings.Contains(stderr, "name=value") {
		t.Errorf("malformed override: exit %d, stderr %q", code, stderr)
	}
	if code, _, stderr := run(t, "describe", "--content", goodRoot, "pipeline-meltdown",
		"--set", "scale=7"); code != 2 || !strings.Contains(stderr, "requires --seed") {
		t.Errorf("--set without --seed: exit %d, stderr %q", code, stderr)
	}
}

func TestDescribeCandidateFilesExcludeInterviewerOnly(t *testing.T) {
	_, stdout, _ := run(t, "describe", "--content", goodRoot, "pipeline-meltdown", "--json")
	var out describeOut
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatal(err)
	}
	for _, f := range out.CandidateFiles {
		if strings.HasPrefix(f, "interviewer/") || f == "problem.yaml" {
			t.Errorf("candidate file list contains interviewer-only path %q", f)
		}
	}
	if len(out.CandidateFiles) != 1 || out.CandidateFiles[0] != "candidate/brief.md" {
		t.Errorf("candidate files = %v", out.CandidateFiles)
	}
}

func TestDescribeArgErrors(t *testing.T) {
	if code, _, _ := run(t, "describe", "--content", goodRoot); code != 2 {
		t.Errorf("no id: exit %d, want 2", code)
	}
	if code, _, _ := run(t, "describe", "--content", goodRoot, "a", "b"); code != 2 {
		t.Errorf("two ids: exit %d, want 2", code)
	}
	if code, _, stderr := run(t, "describe", "--content", goodRoot, "nope"); code != 1 ||
		!strings.Contains(stderr, "no problem") {
		t.Errorf("unknown id: exit %d, stderr %q", code, stderr)
	}
}

func TestFlagsWorkOnEitherSideOfPositional(t *testing.T) {
	_, before, _ := run(t, "describe", "--content", goodRoot, "--json", "pipeline-meltdown")
	_, after, _ := run(t, "describe", "--content", goodRoot, "pipeline-meltdown", "--json")
	if before != after {
		t.Errorf("flag position changed output:\nbefore: %s\nafter: %s", before, after)
	}
	if !strings.HasPrefix(strings.TrimSpace(after), "{") {
		t.Errorf("trailing --json ignored: %s", after)
	}
}

func TestStrayPositionalsRejected(t *testing.T) {
	if code, _, stderr := run(t, "list", "--content", goodRoot, "extra"); code != 2 ||
		!strings.Contains(stderr, "unexpected argument") {
		t.Errorf("list with stray arg: exit %d, stderr %q", code, stderr)
	}
	if code, _, stderr := run(t, "validate", "--content", goodRoot, "extra"); code != 2 ||
		!strings.Contains(stderr, "unexpected argument") {
		t.Errorf("validate with stray arg: exit %d, stderr %q", code, stderr)
	}
}

func TestValidateCleanTree(t *testing.T) {
	code, stdout, stderr := run(t, "validate", "--content", goodRoot)
	if code != 0 {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "3 problems, 0 errors, 0 warnings") {
		t.Errorf("summary = %q", stdout)
	}
}

func TestValidateReportsErrors(t *testing.T) {
	code, stdout, _ := run(t, "validate", "--content", "testdata/broken")
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if !strings.Contains(stdout, "brief is required") {
		t.Errorf("missing brief not reported: %q", stdout)
	}
}

func TestValidateJSON(t *testing.T) {
	code, stdout, _ := run(t, "validate", "--content", "testdata/broken", "--json")
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	var out validateOut
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}
	if out.Problems != 1 || len(out.Errors) == 0 {
		t.Errorf("validate JSON = %+v", out)
	}
}

// The listing has had a LEVELS column since the start with no way to filter
// on it, which is the question being asked of it.
func TestListFiltersByLevel(t *testing.T) {
	code, stdout, stderr := run(t, "list", "--content", goodRoot, "--level", "senior")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if !strings.Contains(stdout, "pipeline-meltdown") {
		t.Errorf("senior problem missing:\n%s", stdout)
	}
	if code, _, stderr := run(t, "list", "--content", goodRoot, "--level", "wizard"); code != 2 ||
		!strings.Contains(stderr, "--level") {
		t.Errorf("bad level: exit %d, stderr %q", code, stderr)
	}
}
