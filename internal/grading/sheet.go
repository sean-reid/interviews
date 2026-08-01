package grading

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"text/template"

	"github.com/sean-reid/interviews/internal/content"
	"github.com/sean-reid/interviews/internal/taxonomy"
	"github.com/sean-reid/interviews/internal/variant"
)

// SheetData is everything a rendered grading sheet needs.
type SheetData struct {
	Rubric   *Rubric
	Manifest content.Manifest
	Variant  *variant.Resolved
	Score    *Score // nil until interviews grade score has run
	Hints    []Hint
	// Level is who this interview was calibrated for. Empty leaves the row
	// blank and prints every band, which is the old behavior.
	Level taxonomy.Level
}

// sheetText is the grading sheet template; anchor iteration uses helper
// funcs because nested index expressions read badly in templates.
const sheetText = `# Grading sheet: {{.Manifest.Title}}

| | |
|---|---|
| Candidate | |
| Level targeted | {{if .Level}}{{.Level}}{{else}}(this problem grades: {{join .LevelNames ", "}}){{end}} |
| Interviewer | |
| Date | |
| Interview id | {{.Variant.InterviewID}} |
| Problem | {{.Manifest.ID}} ({{.Manifest.Type}}{{if .Kind}}/{{.Kind}}{{end}}) |
| Variant | {{.ParamsLine}} |

Scoring is rubric-first. The objective section records what happened; the
rubric records how they worked. Nobody is expected to finish: judge method,
not completion.

## Objective record
{{if .Score}}
| Fault | Tier | Fixed? | Identified? | Notes |
|---|---|---|---|---|
{{- range .Score.Faults}}
| {{.ID}}: {{.Title}} | {{.Tier}} | {{if .CheckFailed}}check did not run{{else if .Fixed}}yes{{else}}no{{end}} | | |
{{- end}}

Checks report {{.Score.Fixed}}/{{.Score.Total}} fixed; end-to-end verify {{if .Score.Verified}}passed{{else}}failed{{end}}, {{.ScoreTaken}}. Mark Identified? from the session: did they name the root cause, even without fixing it?
{{- if .ScoreStale}}

That reading predates the last hint you logged, so it is not the state the session ended in. Run ` + "`interviews grade score`" + ` against the live environment and re-render before grading.
{{- end}}
{{- if .BrokenChecks}}

{{.BrokenChecks}} of those checks could not run, so they say nothing about their fault either way. Judge those from the session, and fix the check scripts.
{{- end}}
{{else if .IsDebugging}}
Run ` + "`interviews grade score`" + ` against the live environment to fill this section, then re-render the sheet.
{{else}}
Attach the submitted artifact and note in one line each: what works, what is unfinished, what the stopping-point writeup claims.
{{end}}
## Rubric (score each 1-4)
{{range .Rubric.Dimensions}}{{$d := .}}
### {{.Title}} - score: __

{{.Question}}
{{range $s := anchorScores}}
- **{{$s}}**: {{anchor $d $s}}{{end}}
{{end}}
## Calibration band{{if not .Level}}s{{end}}

| Level | A typical pass |
|---|---|
{{- range .LevelRows}}
| {{.Level}} | {{.Band}} |
{{- end}}

## Where to look ({{.Manifest.Type}})
{{range .EvidencePrompts}}
- {{.}}{{end}}

## Hints given (each one is context for the rubric, not a penalty formula)

| Minute | Hint |
|---|---|
{{- range .Hints}}
| {{.Minute}} | {{.Text}} |
{{- end}}
| | |

## Recommendation

Strong hire / hire / no hire / strong no hire, and the one paragraph of
reasoning you would defend in a debrief:
`

// LevelRow pairs a level with its calibration band for template iteration.
type LevelRow struct {
	Level taxonomy.Level
	Band  string
}

type sheetContext struct {
	SheetData
	ScoreTaken      string
	ScoreStale      bool
	Kind            string
	LevelNames      []string
	ParamsLine      string
	IsDebugging     bool
	BrokenChecks    int
	LevelRows       []LevelRow
	EvidencePrompts []string
}

// RenderSheet writes the markdown grading sheet.
func RenderSheet(w io.Writer, d SheetData) error {
	kind := string(d.Manifest.Flavor)
	if kind == "" {
		kind = string(d.Manifest.Class)
	}
	ctx := sheetContext{
		SheetData:       d,
		Kind:            kind,
		IsDebugging:     d.Manifest.Type == taxonomy.Debugging,
		EvidencePrompts: d.Rubric.Types[d.Manifest.Type],
	}
	if d.Score != nil {
		for _, f := range d.Score.Faults {
			if f.CheckFailed {
				ctx.BrokenChecks++
			}
		}
		// A score is a reading taken at a moment, and the sheet is rendered
		// later. Undated it reads as the final state of the session, which is
		// how a stale one ends up in a hiring decision.
		ctx.ScoreTaken = "measured " + d.Score.At.Format("2006-01-02 15:04 MST")
		if d.Score.At.IsZero() {
			ctx.ScoreTaken = "from a score file that does not say when it was measured"
		}
		for _, h := range d.Hints {
			if !d.Score.At.IsZero() && !h.At.IsZero() && h.At.After(d.Score.At) {
				ctx.ScoreStale = true
			}
		}
	}
	for _, l := range d.Manifest.Levels {
		ctx.LevelNames = append(ctx.LevelNames, string(l))
	}
	for _, l := range taxonomy.Levels {
		// One band when the interview says who it was for. Five bands next to
		// a blank row is a table to read past, not a thing to grade against.
		if d.Level != "" && l != d.Level {
			continue
		}
		ctx.LevelRows = append(ctx.LevelRows, LevelRow{Level: l, Band: d.Rubric.Levels[l]})
	}
	var parts []string
	for _, name := range slices.Sorted(maps.Keys(d.Variant.Params)) {
		if d.Manifest.Params[name].Secret {
			parts = append(parts, name+"=(secret)")
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%v", name, d.Variant.Params[name]))
	}
	ctx.ParamsLine = strings.Join(parts, ", ")

	tmpl := template.New("sheet").Funcs(template.FuncMap{
		"join":         strings.Join,
		"anchorScores": func() []int { return []int{1, 2, 3, 4} },
		"anchor":       func(d Dimension, s int) string { return d.Anchors[s] },
	})
	tmpl, err := tmpl.Parse(sheetText)
	if err != nil {
		return err
	}
	return tmpl.Execute(w, ctx)
}
