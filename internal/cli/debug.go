package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/sean-reid/interviews/internal/content"
	"github.com/sean-reid/interviews/internal/debug"
	"github.com/sean-reid/interviews/internal/taxonomy"
	"github.com/sean-reid/interviews/internal/variant"
)

// engineFor loads a debugging problem and builds its engine. Every
// debugging command funnels through here.
func engineFor(contentRoot, problemID, seed, workdir string, sets []string, stdout, stderr io.Writer) (*debug.Engine, error) {
	if seed == "" {
		return nil, fmt.Errorf("--seed is required (the interview id; it selects the variant)")
	}
	overrides, err := parseOverrides(sets)
	if err != nil {
		return nil, err
	}
	reg, err := openRegistry(contentRoot, false, stderr)
	if err != nil {
		return nil, err
	}
	entry, ok := reg.Get(problemID)
	if !ok {
		return nil, fmt.Errorf("no problem %q (try interviews list)", problemID)
	}
	if entry.Type != taxonomy.Debugging {
		return nil, fmt.Errorf("%s is a %s problem; only debugging problems run environments", problemID, entry.Type)
	}
	scenario, issues := debug.LoadScenario(entry.Problem)
	if scenario == nil || content.Errors(issues) {
		return nil, fmt.Errorf("scenario invalid; run interviews validate: %v", issues)
	}
	v, err := variant.Resolve(problemID, entry.Problem.Manifest.Params, seed, overrides)
	if err != nil {
		return nil, err
	}
	dir, err := filepath.Abs(filepath.Join(contentRoot, entry.Dir))
	if err != nil {
		return nil, err
	}
	runner := &debug.ExecRunner{Stdout: stdout, Stderr: stderr}
	return debug.NewEngine(dir, scenario, v, runner, stdout, workdir)
}

// debugFlags parses the flags every debugging command shares.
func debugFlags(name string, args []string, stderr io.Writer) (contentRoot, seed, workdir string, sets []string, positional []string, ok bool) {
	fs, content := newFlagSet(name, stderr)
	seedFlag := fs.String("seed", "", "interview id selecting the variant")
	workdirFlag := fs.String("workdir", "", "session state directory (default: per-variant cache dir)")
	var setFlags repeatedFlag
	fs.Var(&setFlags, "set", "override a parameter (name=value, repeatable)")
	pos, err := parsePermuted(fs, args)
	if err != nil {
		return "", "", "", nil, nil, false
	}
	return *content, *seedFlag, *workdirFlag, setFlags, pos, true
}

func cmdEnv(args []string, stdout, stderr io.Writer) int {
	usage := "usage: interviews env up|verify|down <problem-id> --seed <id> [--set k=v]"
	contentRoot, seed, workdir, sets, pos, ok := debugFlags("env", args, stderr)
	if !ok {
		return 2
	}
	if len(pos) != 2 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	verb, problemID := pos[0], pos[1]
	if verb != "up" && verb != "verify" && verb != "down" {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	e, err := engineFor(contentRoot, problemID, seed, workdir, sets, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "interviews env: %v\n", err)
		return 1
	}
	ctx := context.Background()
	switch verb {
	case "up":
		err = e.Up(ctx)
	case "verify":
		if err = e.Verify(ctx); err == nil {
			fmt.Fprintln(stdout, "healthy")
		}
	case "down":
		err = e.Down(ctx)
	}
	if err != nil {
		fmt.Fprintf(stderr, "interviews env %s: %v\n", verb, err)
		return 1
	}
	return 0
}

func cmdBreak(args []string, stdout, stderr io.Writer) int {
	contentRoot, seed, workdir, sets, pos, ok := debugFlags("break", args, stderr)
	if !ok {
		return 2
	}
	if len(pos) != 1 {
		fmt.Fprintln(stderr, "usage: interviews break <problem-id> --seed <id>")
		return 2
	}
	e, err := engineFor(contentRoot, pos[0], seed, workdir, sets, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "interviews break: %v\n", err)
		return 1
	}
	if err := e.Break(context.Background()); err != nil {
		fmt.Fprintf(stderr, "interviews break: %v\n", err)
		return 1
	}
	return 0
}

func cmdFault(args []string, stdout, stderr io.Writer) int {
	usage := "usage: interviews fault status|fix <problem-id> [fault-id] --seed <id>"
	contentRoot, seed, workdir, sets, pos, ok := debugFlags("fault", args, stderr)
	if !ok {
		return 2
	}
	if len(pos) < 2 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	verb, problemID := pos[0], pos[1]
	if verb != "status" && verb != "fix" {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	e, err := engineFor(contentRoot, problemID, seed, workdir, sets, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "interviews fault: %v\n", err)
		return 1
	}
	ctx := context.Background()
	switch verb {
	case "status":
		statuses, err := e.Status(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "interviews fault status: %v\n", err)
			return 1
		}
		w := tabwriter.NewWriter(stdout, 2, 8, 2, ' ', 0)
		fmt.Fprintln(w, "FAULT\tTIER\tSTATE\tTITLE")
		for _, s := range statuses {
			state := "BROKEN"
			if s.Fixed {
				state = "FIXED"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", s.ID, s.Tier, state, s.Title)
		}
		if err := w.Flush(); err != nil {
			return 1
		}
		return 0
	case "fix":
		faultID := ""
		if len(pos) > 2 {
			faultID = pos[2]
		}
		if err := e.Fix(ctx, faultID); err != nil {
			fmt.Fprintf(stderr, "interviews fault fix: %v\n", err)
			return 1
		}
	}
	return 0
}

func cmdProve(args []string, stdout, stderr io.Writer) int {
	fs, contentRoot := newFlagSet("prove", stderr)
	packFlag := fs.String("pack", "", "prove only this fault pack")
	keep := fs.Bool("keep", false, "leave the environment up after proving")
	var sets repeatedFlag
	fs.Var(&sets, "set", "pin a parameter for the proven variant (name=value, repeatable)")
	pos, err := parsePermuted(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) != 1 {
		fmt.Fprintln(stderr, "usage: interviews prove <problem-id> [--pack name] [--set k=v] [--keep]")
		return 2
	}
	problemID := pos[0]
	for _, s := range sets {
		if strings.HasPrefix(s, debug.PackParam+"=") {
			fmt.Fprintf(stderr, "interviews prove: pick the pack with --pack, not --set %s\n", debug.PackParam)
			return 2
		}
	}

	// Prove pins the pack by override, with a deterministic per-pack seed,
	// so CI covers every pack regardless of what real interviews draw. --set
	// pins the rest, for proving that a fault's scripts hold for a variant
	// other than the one those seeds happen to draw.
	packs, code := provePacks(*contentRoot, problemID, *packFlag, stderr)
	if code != 0 {
		return code
	}
	for _, pack := range packs {
		e, err := engineFor(*contentRoot, problemID, "prove-"+pack, "",
			append([]string{debug.PackParam + "=" + pack}, sets...), stdout, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "interviews prove: %v\n", err)
			return 1
		}
		ctx := context.Background()
		proveErr := e.Prove(ctx)
		if !*keep {
			if err := e.Down(ctx); err != nil {
				fmt.Fprintf(stderr, "interviews prove: teardown: %v\n", err)
			}
		}
		if proveErr != nil {
			fmt.Fprintf(stderr, "interviews prove: %v\n", proveErr)
			return 1
		}
	}
	return 0
}

func provePacks(contentRoot, problemID, only string, stderr io.Writer) ([]string, int) {
	reg, err := openRegistry(contentRoot, false, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "interviews prove: %v\n", err)
		return nil, 1
	}
	entry, ok := reg.Get(problemID)
	if !ok || entry.Type != taxonomy.Debugging {
		fmt.Fprintf(stderr, "interviews prove: no debugging problem %q\n", problemID)
		return nil, 1
	}
	packs := entry.Problem.Manifest.Params[debug.PackParam].Of
	if len(packs) == 0 {
		fmt.Fprintf(stderr, "interviews: %s declares no %s values, so there is nothing to prove\n",
			problemID, debug.PackParam)
		return nil, 1
	}
	if only == "" {
		return packs, 0
	}
	for _, p := range packs {
		if p == only {
			return []string{only}, 0
		}
	}
	fmt.Fprintf(stderr, "interviews prove: no pack %q (have %v)\n", only, packs)
	return nil, 1
}
