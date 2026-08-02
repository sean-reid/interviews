package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"text/tabwriter"

	"github.com/sean-reid/interviews/internal/content"
	"github.com/sean-reid/interviews/internal/debug"
	"github.com/sean-reid/interviews/internal/provenance"
	"github.com/sean-reid/interviews/internal/taxonomy"
)

// answerKeyNotice heads the output that names the injected faults. Both
// commands get run mid-interview, when a screen is often shared.
const answerKeyNotice = "-- interviewer only: this names the faults. Do not share this window."

// engineFor loads a debugging problem and builds its engine. Every
// debugging command funnels through here.
func engineFor(contentRoot, problemID, seed, workdir string, sets []string, stdout, stderr io.Writer) (*debug.Engine, error) {
	r, err := resolveProblem(contentRoot, problemID, seed, workdir, sets, stderr)
	if err != nil {
		return nil, err
	}
	if r.entry.Type != taxonomy.Debugging {
		// Naming the verb that does handle it, because the last thing an
		// error should leave anyone doing is guessing at the command.
		return nil, fmt.Errorf("%s is a %s problem; only debugging problems run environments"+
			" (interviews start hands it out, interviews bundle writes the drop)", problemID, r.entry.Type)
	}
	scenario, issues := debug.LoadScenario(r.entry.Problem)
	if scenario == nil || content.Errors(issues) {
		return nil, fmt.Errorf("scenario invalid; run interviews validate: %v", issues)
	}
	dir, err := filepath.Abs(filepath.Join(contentRoot, r.entry.Dir))
	if err != nil {
		return nil, err
	}
	runner := &debug.ExecRunner{Stdout: stdout, Stderr: stderr}
	return debug.NewEngine(dir, scenario, r.variant, runner, stdout, r.workdir)
}

// originFor is what an environment's provenance records about this machine.
// Only the commands that bring one up ask for it: it reads the content
// checkout, and no other command needs the answer.
func originFor(contentRoot string) provenance.Record {
	return provenance.New(provenance.Running(), contentVersion(contentRoot))
}

// debugFlags parses the flags every debugging command shares.
func debugFlags(name string, args []string, stderr io.Writer) (contentRoot, seed, workdir string, sets []string, positional []string, err error) {
	fs, content := newFlagSet(name, stderr)
	seedFlag := fs.String("seed", "", "interview id selecting the variant")
	workdirFlag := fs.String("workdir", "", "session state directory (default: per-variant cache dir)")
	var setFlags repeatedFlag
	fs.Var(&setFlags, "set", "override a parameter (name=value, repeatable)")
	pos, err := parsePermuted(fs, args)
	if err != nil {
		return "", "", "", nil, nil, err
	}
	return *content, *seedFlag, *workdirFlag, setFlags, pos, nil
}

// envFlags is debugFlags plus the teardown flag; only env down takes one.
func envFlags(name string, args []string, stderr io.Writer) (contentRoot, seed, workdir string, sets []string, purge bool, positional []string, err error) {
	fs, content := newFlagSet(name, stderr)
	seedFlag := fs.String("seed", "", "interview id selecting the variant")
	workdirFlag := fs.String("workdir", "", "session state directory (default: per-variant cache dir)")
	purgeFlag := fs.Bool("purge", false, "on down, delete the session evidence along with the environment")
	var setFlags repeatedFlag
	fs.Var(&setFlags, "set", "override a parameter (name=value, repeatable)")
	pos, err := parsePermuted(fs, args)
	if err != nil {
		return "", "", "", nil, false, nil, err
	}
	return *content, *seedFlag, *workdirFlag, setFlags, *purgeFlag, pos, nil
}

// reportTeardown says where the evidence went. Teardown is the last step of
// an interview, so silence here reads as "the session is filed away" when
// nothing has been filed anywhere.
// purgeTarget reports what --purge would delete, and whether it is there.
// A seed with a typo in it derives a workdir that never existed, and purge
// used to report deleting it in exactly the same words as a real deletion.
func purgeTarget(e *debug.Engine) (string, bool) {
	_, err := os.Stat(e.Workdir)
	return e.Workdir, err == nil
}

func reportTeardown(stdout io.Writer, e *debug.Engine, kept []string, purged, existed bool) {
	if purged {
		if !existed {
			fmt.Fprintf(stdout, "environment %s down; there was no evidence at %s to delete\n", e.EnvName(), e.Workdir)
			return
		}
		fmt.Fprintf(stdout, "environment %s down, %s deleted\n", e.EnvName(), e.Workdir)
		return
	}
	if len(kept) == 0 {
		fmt.Fprintf(stdout, "environment %s down (no session evidence to keep)\n", e.EnvName())
		return
	}
	fmt.Fprintf(stdout, "environment %s down; the session evidence stays in %s:\n", e.EnvName(), e.Workdir)
	for _, path := range kept {
		fmt.Fprintf(stdout, "  %s\n", filepath.Base(path))
	}
	fmt.Fprintf(stdout, "copy that directory somewhere durable; interviews grade sheet %s --seed %s still reads it\n",
		e.Variant.Problem, e.Variant.InterviewID)
}

func cmdEnv(args []string, stdout, stderr io.Writer) int {
	contentRoot, seed, workdir, sets, purge, pos, ferr := envFlags("env", args, stderr)
	if ferr != nil {
		return parseExit(ferr)
	}
	if len(pos) != 2 {
		return usageErr("env", stderr)
	}
	verb, problemID := pos[0], pos[1]
	if verb != "up" && verb != "verify" && verb != "down" {
		return usageErr("env", stderr)
	}
	e, err := engineFor(contentRoot, problemID, seed, workdir, sets, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "interviews env: %v\n", err)
		return 1
	}
	ctx := context.Background()
	switch verb {
	case "up":
		e.Origin = originFor(contentRoot)
		err = e.Up(ctx)
	case "verify":
		if err = e.Verify(ctx); err == nil {
			fmt.Fprintln(stdout, "healthy")
		}
	case "down":
		var kept []string
		_, existed := purgeTarget(e)
		if kept, err = e.Down(ctx, purge); err == nil {
			reportTeardown(stdout, e, kept, purge, existed)
		}
	}
	if err != nil {
		fmt.Fprintf(stderr, "interviews env %s: %v\n", verb, err)
		return 1
	}
	return 0
}

func cmdBreak(args []string, stdout, stderr io.Writer) int {
	contentRoot, seed, workdir, sets, pos, ferr := debugFlags("break", args, stderr)
	if ferr != nil {
		return parseExit(ferr)
	}
	if len(pos) != 1 {
		return usageErr("break", stderr)
	}
	e, err := engineFor(contentRoot, pos[0], seed, workdir, sets, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "interviews break: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, answerKeyNotice)
	if err := e.Break(context.Background()); err != nil {
		fmt.Fprintf(stderr, "interviews break: %v\n", err)
		return 1
	}
	return 0
}

func cmdFault(args []string, stdout, stderr io.Writer) int {
	contentRoot, seed, workdir, sets, pos, ferr := debugFlags("fault", args, stderr)
	if ferr != nil {
		return parseExit(ferr)
	}
	if len(pos) < 2 {
		return usageErr("fault", stderr)
	}
	verb, problemID := pos[0], pos[1]
	if verb != "status" && verb != "fix" {
		return usageErr("fault", stderr)
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
		fmt.Fprintln(stdout, answerKeyNotice)
		w := tabwriter.NewWriter(stdout, 2, 8, 2, ' ', 0)
		fmt.Fprintln(w, "FAULT\tTIER\tSTATE\tTITLE")
		cannotRun := 0
		for _, s := range statuses {
			if s.State == debug.CheckCannotRun {
				cannotRun++
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", s.ID, s.Tier, strings.ToUpper(string(s.State)), s.Title)
		}
		if err := w.Flush(); err != nil {
			return 1
		}
		if cannotRun > 0 {
			fmt.Fprintf(stderr, "%d check script(s) exited %d: they could not run, so those faults have no reading. Fix the scripts.\n",
				cannotRun, debug.CheckCannotRunExit)
		}
		return 0
	case "fix":
		faultID := ""
		if len(pos) > 2 {
			faultID = pos[2]
		}
		// Fixing names the faults just like status does, and with no fault id
		// it applies the answer key to all of them.
		fmt.Fprintln(stdout, answerKeyNotice)
		if err := e.Fix(ctx, faultID); err != nil {
			fmt.Fprintf(stderr, "interviews fault fix: %v\n", err)
			return 1
		}
	}
	return 0
}

// Seeds the unattended commands run under. They are fixed so a rerun lands
// on the same environment as the last one.
const (
	proveSeed     = "prove"
	calibrateSeed = "calibrate"
)

func cmdProve(args []string, stdout, stderr io.Writer) int {
	fs, contentRoot := newFlagSet("prove", stderr)
	packFlag := fs.String("pack", "", "prove only this fault pack")
	keep := fs.Bool("keep", false, "leave the environment up after proving")
	var sets repeatedFlag
	fs.Var(&sets, "set", "pin a parameter for the proven variant (name=value, repeatable)")
	pos, err := parsePermuted(fs, args)
	if err != nil {
		return parseExit(err)
	}
	if len(pos) != 1 {
		return usageErr("prove", stderr)
	}
	problemID := pos[0]
	for _, s := range sets {
		if strings.HasPrefix(s, debug.PackParam+"=") {
			fmt.Fprintf(stderr, "interviews prove: pick the pack with --pack, not --set %s\n", debug.PackParam)
			return 2
		}
	}

	// Prove pins the pack by override on one fixed seed, so CI covers every
	// pack regardless of what real interviews draw, and --set pins the rest,
	// for proving a fault's scripts hold for a variant other than the one
	// that seed happens to draw. Each pack still gets its own cluster and
	// workdir, because the parameters are part of the environment name.
	packs, code := provePacks(*contentRoot, problemID, *packFlag, stderr)
	if code != 0 {
		return code
	}
	// The packs share nothing: different cluster names, different workdirs.
	// Proving them concurrently pays cluster start once in wall time, which
	// is most of the examples CI job. One pack keeps the caller's writers,
	// so the authoring loop still watches live; several buffer per pack and
	// flush in order, so the logs read whole.
	buffered := len(packs) > 1
	outs := make([]bytes.Buffer, len(packs))
	writers := make([]io.Writer, len(packs))
	errWriters := make([]io.Writer, len(packs))
	engines := make([]*debug.Engine, len(packs))
	for i, pack := range packs {
		writers[i], errWriters[i] = stdout, stderr
		if buffered {
			writers[i], errWriters[i] = &outs[i], &outs[i]
		}
		e, err := engineFor(*contentRoot, problemID, proveSeed, "",
			append([]string{debug.PackParam + "=" + pack}, sets...), writers[i], errWriters[i])
		if err != nil {
			fmt.Fprintf(stderr, "interviews prove: %v\n", err)
			return 1
		}
		engines[i] = e
	}
	proveErrs := make([]error, len(packs))
	var wg sync.WaitGroup
	for i := range engines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := context.Background()
			proveErrs[i] = engines[i].Prove(ctx)
			if !*keep {
				// Purge: an unattended run leaves no evidence worth keeping, and CI
				// would accumulate a workdir per proven pack.
				if _, err := engines[i].Down(ctx, true); err != nil {
					fmt.Fprintf(errWriters[i], "interviews prove: teardown: %v\n", err)
				}
			}
		}()
	}
	wg.Wait()
	code = 0
	for i := range packs {
		if buffered {
			if _, err := io.Copy(stdout, &outs[i]); err != nil {
				return 1
			}
		}
		if proveErrs[i] != nil {
			fmt.Fprintf(stderr, "interviews prove: %v\n", proveErrs[i])
			code = 1
		}
	}
	return code
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
