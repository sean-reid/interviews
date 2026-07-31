package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/sean-reid/interviews/internal/debug"
	"github.com/sean-reid/interviews/internal/grading"
	"github.com/sean-reid/interviews/internal/registry"
	"github.com/sean-reid/interviews/internal/session"
	"github.com/sean-reid/interviews/internal/variant"
)

func cmdGrade(args []string, stdout, stderr io.Writer) int {
	usage := "usage: interviews grade sheet|score|hint <problem-id> --seed <id> [flags]"
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	switch args[0] {
	case "sheet":
		return gradeSheet(args[1:], stdout, stderr)
	case "score":
		return gradeScore(args[1:], stdout, stderr)
	case "hint":
		return gradeHint(args[1:], stdout, stderr)
	default:
		fmt.Fprintln(stderr, usage)
		return 2
	}
}

// gradeTarget resolves the shared plumbing: problem, variant, workdir.
func gradeTarget(contentRoot, problemID, seed, workdir string, sets []string, stderr io.Writer) (*registry.Entry, *variant.Resolved, string, error) {
	if seed == "" {
		return nil, nil, "", fmt.Errorf("--seed is required (the interview id)")
	}
	overrides, err := parseOverrides(sets)
	if err != nil {
		return nil, nil, "", err
	}
	reg, err := openRegistry(contentRoot, false, stderr)
	if err != nil {
		return nil, nil, "", err
	}
	entry, ok := reg.Get(problemID)
	if !ok {
		return nil, nil, "", fmt.Errorf("no problem %q (try interviews list)", problemID)
	}
	v, err := variant.Resolve(problemID, entry.Problem.Manifest.Params, seed, overrides)
	if err != nil {
		return nil, nil, "", err
	}
	if workdir == "" {
		if workdir, err = debug.DefaultWorkdir(v); err != nil {
			return nil, nil, "", err
		}
	}
	return entry, v, workdir, nil
}

func gradeSheet(args []string, stdout, stderr io.Writer) int {
	fs, contentRoot := newFlagSet("grade sheet", stderr)
	seed := fs.String("seed", "", "interview id")
	workdir := fs.String("workdir", "", "session state directory")
	outPath := fs.String("o", "", "write the sheet to a file instead of stdout")
	rubricPath := fs.String("rubric", "", "override the built-in rubric")
	var sets repeatedFlag
	fs.Var(&sets, "set", "override a parameter (name=value, repeatable)")
	pos, err := parsePermuted(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) != 1 {
		fmt.Fprintln(stderr, "usage: interviews grade sheet <problem-id> --seed <id> [-o file] [--rubric file]")
		return 2
	}

	entry, v, wd, err := gradeTarget(*contentRoot, pos[0], *seed, *workdir, sets, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "interviews grade sheet: %v\n", err)
		return 1
	}
	rubric, err := loadRubric(*rubricPath)
	if err != nil {
		fmt.Fprintf(stderr, "interviews grade sheet: %v\n", err)
		return 1
	}
	score, err := grading.LoadScore(wd)
	if err != nil {
		fmt.Fprintf(stderr, "interviews grade sheet: %v\n", err)
		return 1
	}
	hints, err := grading.LoadHints(wd)
	if err != nil {
		fmt.Fprintf(stderr, "interviews grade sheet: %v\n", err)
		return 1
	}

	data := grading.SheetData{
		Rubric: rubric, Manifest: entry.Problem.Manifest,
		Variant: v, Score: score, Hints: hints,
	}
	if *outPath != "" {
		var buf strings.Builder
		if err := grading.RenderSheet(&buf, data); err != nil {
			fmt.Fprintf(stderr, "interviews grade sheet: %v\n", err)
			return 1
		}
		if err := os.WriteFile(*outPath, []byte(buf.String()), 0o644); err != nil {
			fmt.Fprintf(stderr, "interviews grade sheet: %v\n", err)
			return 1
		}
		return 0
	}
	if err := grading.RenderSheet(stdout, data); err != nil {
		fmt.Fprintf(stderr, "interviews grade sheet: %v\n", err)
		return 1
	}
	return 0
}

func loadRubric(path string) (*grading.Rubric, error) {
	if path == "" {
		return grading.Default()
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return grading.Parse(raw)
}

func gradeScore(args []string, stdout, stderr io.Writer) int {
	contentRoot, seed, workdir, sets, pos, ok := debugFlags("grade score", args, stderr)
	if !ok {
		return 2
	}
	if len(pos) != 1 {
		fmt.Fprintln(stderr, "usage: interviews grade score <problem-id> --seed <id>")
		return 2
	}
	e, err := engineFor(contentRoot, pos[0], seed, workdir, sets, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "interviews grade score: %v\n", err)
		return 1
	}
	score, err := session.RefreshScore(context.Background(), e)
	if err != nil {
		fmt.Fprintf(stderr, "interviews grade score: %v\n", err)
		return 1
	}
	verified := "failed"
	if score.Verified {
		verified = "passed"
	}
	fmt.Fprintf(stdout, "score: %d/%d faults fixed, end-to-end verify %s (wrote %s)\n",
		score.Fixed, score.Total, verified, grading.ScoreFile)
	return 0
}

func gradeHint(args []string, stdout, stderr io.Writer) int {
	fs, contentRoot := newFlagSet("grade hint", stderr)
	seed := fs.String("seed", "", "interview id")
	workdir := fs.String("workdir", "", "session state directory")
	minute := fs.String("minute", "", "minutes into the session")
	var sets repeatedFlag
	fs.Var(&sets, "set", "override a parameter (name=value, repeatable)")
	pos, err := parsePermuted(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) != 2 || *minute == "" {
		fmt.Fprintln(stderr, `usage: interviews grade hint <problem-id> "the hint text" --seed <id> --minute <n>`)
		return 2
	}
	min, err := strconv.Atoi(*minute)
	if err != nil || min < 0 {
		fmt.Fprintf(stderr, "interviews grade hint: --minute %q is not a non-negative integer\n", *minute)
		return 2
	}
	_, _, wd, err := gradeTarget(*contentRoot, pos[0], *seed, *workdir, sets, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "interviews grade hint: %v\n", err)
		return 1
	}
	if err := os.MkdirAll(wd, 0o755); err != nil {
		fmt.Fprintf(stderr, "interviews grade hint: %v\n", err)
		return 1
	}
	if err := grading.AppendHint(wd, grading.Hint{Minute: min, Text: pos[1], At: time.Now()}); err != nil {
		fmt.Fprintf(stderr, "interviews grade hint: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "logged hint at minute %d\n", min)
	return 0
}
