package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sean-reid/interviews/internal/grading"
	"github.com/sean-reid/interviews/internal/interview"
	"github.com/sean-reid/interviews/internal/registry"
	"github.com/sean-reid/interviews/internal/session"
	"github.com/sean-reid/interviews/internal/variant"
)

func cmdGrade(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usageErr("grade", stderr)
	}
	switch args[0] {
	case "sheet":
		return gradeSheet(args[1:], stdout, stderr)
	case "score":
		return gradeScore(args[1:], stdout, stderr)
	case "hint":
		return gradeHint(args[1:], stdout, stderr)
	default:
		return usageErr("grade", stderr)
	}
}

// gradeTarget resolves the shared plumbing: problem, variant, workdir.
func gradeTarget(contentRoot, problemID, seed, workdir string, sets []string, stderr io.Writer) (*registry.Entry, *variant.Resolved, string, string, error) {
	r, err := resolveProblem(contentRoot, problemID, seed, workdir, sets, stderr)
	if err != nil {
		return nil, nil, "", "", err
	}
	return r.entry, r.variant, r.workdir, r.seed, nil
}

func gradeSheet(args []string, stdout, stderr io.Writer) int {
	fs, contentRoot := newFlagSet("grade sheet", stderr)
	seed := fs.String("seed", "", "interview id")
	workdir := fs.String("workdir", "", "session state directory")
	outPath := fs.String("o", "", "write the sheet to a file instead of stdout")
	rubricPath := fs.String("rubric", "", "override the built-in rubric")
	hintsPath := fs.String("hints", "",
		"a second hints ledger to merge in, as a directory or a file (see grade hint --workdir)")
	levelFlag := fs.String("level", "", "level to grade against (default: what the session recorded)")
	var sets repeatedFlag
	fs.Var(&sets, "set", "override a parameter (name=value, repeatable)")
	pos, err := parsePermuted(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) != 1 {
		return usageErr("grade sheet", stderr)
	}

	entry, v, wd, resolvedSeed, err := gradeTarget(*contentRoot, pos[0], *seed, *workdir, sets, stderr)
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
	if *hintsPath != "" {
		// Hints are logged wherever the interviewer is sitting, which for a
		// remote session is not where the evidence comes from.
		logged, err := grading.LoadHintsFrom(*hintsPath)
		if err != nil {
			fmt.Fprintf(stderr, "interviews grade sheet: %v\n", err)
			return 1
		}
		hints = grading.MergeHints(hints, logged)
	}

	level, err := parseLevel(*levelFlag)
	if err != nil {
		fmt.Fprintf(stderr, "interviews grade sheet: %v\n", err)
		return 2
	}
	if level == "" {
		if rec, lerr := interview.Load(resolvedSeed); lerr == nil {
			level = rec.Level
		}
	}
	data := grading.SheetData{
		Rubric: rubric, Manifest: entry.Problem.Manifest,
		Variant: v, Score: score, Hints: hints, Level: level,
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
		return usageErr("grade score", stderr)
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
		return usageErr("grade hint", stderr)
	}
	min, err := strconv.Atoi(*minute)
	if err != nil || min < 0 {
		fmt.Fprintf(stderr, "interviews grade hint: --minute %q is not a non-negative integer\n", *minute)
		return 2
	}
	_, _, wd, _, err := gradeTarget(*contentRoot, pos[0], *seed, *workdir, sets, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "interviews grade hint: %v\n", err)
		return 1
	}
	// A hint goes into an existing ledger unless the interviewer named the
	// directory. The default one is derived from the seed, so creating it on
	// demand turns a typo in --seed into a ledger nobody will ever read: this
	// is the command that gets typed most often and mid-conversation.
	if *workdir == "" {
		if _, err := os.Stat(wd); err != nil {
			fmt.Fprintf(stderr, "interviews grade hint: no session for --seed %s at %s\n", *seed, wd)
			fmt.Fprintf(stderr, "check the seed, or pass --workdir <dir> to log hints for a session running elsewhere\n")
			return 1
		}
	} else if err := os.MkdirAll(wd, 0o755); err != nil {
		fmt.Fprintf(stderr, "interviews grade hint: %v\n", err)
		return 1
	}
	if err := grading.AppendHint(wd, grading.Hint{Minute: min, Text: pos[1], At: time.Now()}); err != nil {
		fmt.Fprintf(stderr, "interviews grade hint: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "logged hint at minute %d in %s\n", min, filepath.Join(wd, grading.HintsFile))
	return 0
}
