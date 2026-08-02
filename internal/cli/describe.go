package cli

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/sean-reid/interviews/internal/content"
	"github.com/sean-reid/interviews/internal/variant"
)

type describeOut struct {
	listItem
	Summary          string            `json:"summary"`
	Time             content.TimeSpec  `json:"time"`
	Params           map[string]string `json:"params,omitempty"` // name -> spec summary
	CandidateFiles   []string          `json:"candidate_files"`
	InterviewerFiles int               `json:"interviewer_files"`
	Variant          *variant.Resolved `json:"variant,omitempty"`
}

func cmdDescribe(args []string, stdout, stderr io.Writer) int {
	fs_, contentRoot := newFlagSet("describe", stderr)
	seed := fs_.String("seed", "", "interview id to resolve the variant for")
	var sets repeatedFlag
	fs_.Var(&sets, "set", "override a parameter (name=value, repeatable)")
	asJSON := fs_.Bool("json", false, "machine-readable output")
	positional, err := parsePermuted(fs_, args)
	if err != nil {
		return parseExit(err)
	}
	if len(positional) != 1 {
		return usageErr("describe", stderr)
	}
	problemID := positional[0]
	if len(sets) > 0 && *seed == "" {
		fmt.Fprintln(stderr, "interviews describe: --set requires --seed")
		return 2
	}

	reg, err := openRegistry(*contentRoot, false, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "interviews describe: %v\n", err)
		return 1
	}
	entry, ok := reg.Get(problemID)
	if !ok {
		fmt.Fprintf(stderr, "interviews describe: no problem %q (try interviews list)\n", problemID)
		return 1
	}
	p := entry.Problem
	m := p.Manifest

	out := describeOut{
		listItem:         itemFor(m),
		Summary:          m.Summary,
		Time:             m.Time,
		Params:           paramSummaries(m.Params),
		CandidateFiles:   p.Scan.Candidate,
		InterviewerFiles: len(p.Scan.Interviewer),
	}

	if *seed != "" {
		overrides, err := parseOverrides(sets)
		if err != nil {
			fmt.Fprintf(stderr, "interviews describe: %v\n", err)
			return 2
		}
		resolved, err := variant.Resolve(m.ID, m.Params, *seed, overrides)
		if err != nil {
			fmt.Fprintf(stderr, "interviews describe: %v\n", err)
			return 1
		}
		out.Variant = resolved
	}

	if *asJSON {
		return writeJSON(stdout, stderr, out)
	}
	writeDescribe(stdout, out)
	return 0
}

func paramSummaries(specs map[string]content.ParamSpec) map[string]string {
	if len(specs) == 0 {
		return nil
	}
	out := make(map[string]string, len(specs))
	for name, s := range specs {
		var desc string
		switch s.Type {
		case content.Choice:
			desc = "choice of " + strings.Join(s.Of, ", ")
		case content.Int:
			// validate rejects an int without bounds, but describe runs on
			// content that has not passed validation yet.
			if s.Min == nil || s.Max == nil {
				desc = "int (bounds missing)"
				break
			}
			desc = fmt.Sprintf("int %d..%d", *s.Min, *s.Max)
		case content.String:
			desc = "string"
		}
		if s.Default != nil {
			desc += fmt.Sprintf(" (default %v)", s.Default)
		}
		out[name] = desc
	}
	return out
}

func writeDescribe(w io.Writer, o describeOut) {
	kind := o.kind()
	if kind != "-" {
		kind = "/" + kind
	} else {
		kind = ""
	}
	fmt.Fprintf(w, "%s - %s (%s%s)\n", o.ID, o.Title, o.Type, kind)
	fmt.Fprintf(w, "  %s\n", o.Summary)
	fmt.Fprintf(w, "  disciplines: %s\n", strings.Join(o.Disciplines, ", "))
	fmt.Fprintf(w, "  levels: %s\n", strings.Join(o.Levels, ", "))
	if o.Time.SessionMinutes > 0 {
		fmt.Fprintf(w, "  time: %d minute session\n", o.Time.SessionMinutes)
	} else {
		fmt.Fprintf(w, "  time: %g hour soft budget\n", o.Time.SoftBudgetHours)
	}
	fmt.Fprintf(w, "  files: %d candidate-visible, %d interviewer-only\n", len(o.CandidateFiles), o.InterviewerFiles)
	if len(o.Params) > 0 {
		fmt.Fprintln(w, "  params:")
		for _, name := range slices.Sorted(maps.Keys(o.Params)) {
			fmt.Fprintf(w, "    %s: %s\n", name, o.Params[name])
		}
	}
	if o.Variant != nil {
		fmt.Fprintf(w, "  variant for %q:\n", o.Variant.InterviewID)
		for _, name := range slices.Sorted(maps.Keys(o.Variant.Params)) {
			fmt.Fprintf(w, "    %s = %v\n", name, o.Variant.Params[name])
		}
	}
}
