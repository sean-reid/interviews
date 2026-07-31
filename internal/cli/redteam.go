package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/sean-reid/interviews/internal/debug"
	"github.com/sean-reid/interviews/internal/redteam"
	"github.com/sean-reid/interviews/internal/taxonomy"
)

func cmdRedteam(args []string, stdout, stderr io.Writer) int {
	usage := "usage: interviews redteam <problem-id> [--driver claude|api] [--budget 30m] | interviews redteam ledger"
	if len(args) > 0 && args[0] == "ledger" {
		return redteamLedger(args[1:], stdout, stderr)
	}

	fs, contentRoot := newFlagSet("redteam", stderr)
	ledgerRoot := fs.String("ledger-root", "", "where calibration/ledger.jsonl lives (default: beside the content tree)")
	driverName := fs.String("driver", "claude", "agent driver: claude (CLI, no API key) or api")
	model := fs.String("model", "", "model override for the driver")
	budget := fs.Duration("budget", 0, "wall-clock budget (default: the problem's session length)")
	packFlag := fs.String("pack", "", "calibrate only this fault pack")
	pos, err := parsePermuted(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) != 1 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	problemID := pos[0]

	reg, err := openRegistry(*contentRoot, false, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "interviews redteam: %v\n", err)
		return 1
	}
	entry, ok := reg.Get(problemID)
	if !ok {
		fmt.Fprintf(stderr, "interviews redteam: no problem %q\n", problemID)
		return 1
	}
	if entry.Type != taxonomy.Debugging {
		fmt.Fprintf(stderr, "interviews redteam: %s is a %s problem; only debugging calibration has landed\n",
			problemID, entry.Type)
		return 1
	}

	driver, err := redteam.NewDriver(*driverName, *model)
	if err != nil {
		fmt.Fprintf(stderr, "interviews redteam: %v\n", err)
		return 1
	}
	runBudget := *budget
	if runBudget == 0 {
		runBudget = time.Duration(entry.Problem.Manifest.Time.SessionMinutes) * time.Minute
	}

	packs, code := provePacks(*contentRoot, problemID, *packFlag, stderr)
	if code != 0 {
		return code
	}
	ctx := context.Background()
	for _, pack := range packs {
		e, err := engineFor(*contentRoot, problemID, calibrateSeed, "",
			[]string{debug.PackParam + "=" + pack}, stdout, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "interviews redteam: %v\n", err)
			return 1
		}
		result, runErr := redteam.DebugRun(ctx, e, driver, stdout, runBudget)
		if err := e.Down(ctx); err != nil {
			fmt.Fprintf(stderr, "interviews redteam: teardown: %v\n", err)
		}
		if runErr != nil {
			fmt.Fprintf(stderr, "interviews redteam: %v\n", runErr)
			return 1
		}
		if err := redteam.Append(ledgerDir(*contentRoot, *ledgerRoot), *result); err != nil {
			fmt.Fprintf(stderr, "interviews redteam: writing ledger: %v\n", err)
			return 1
		}
	}
	return 0
}

// ledgerDir puts the ledger beside the content tree by default, so a
// checkout's calibration history travels with its content.
func ledgerDir(contentRoot, override string) string {
	if override != "" {
		return override
	}
	return filepath.Dir(filepath.Clean(contentRoot))
}

func redteamLedger(args []string, stdout, stderr io.Writer) int {
	fs, contentRoot := newFlagSet("redteam ledger", stderr)
	ledgerRoot := fs.String("ledger-root", "", "where calibration/ledger.jsonl lives (default: beside the content tree)")
	asJSON := fs.Bool("json", false, "machine-readable output")
	staleOnly := fs.Bool("stale", false, "only problems whose latest verdict says rework them")
	if _, err := parsePermuted(fs, args); err != nil {
		return 2
	}

	entries, err := redteam.Load(ledgerDir(*contentRoot, *ledgerRoot))
	if err != nil {
		fmt.Fprintf(stderr, "interviews redteam ledger: %v\n", err)
		return 1
	}
	if *staleOnly {
		entries = redteam.Stale(entries)
	}
	if *asJSON {
		return writeJSON(stdout, stderr, entries)
	}
	if len(entries) == 0 {
		fmt.Fprintln(stdout, "no calibration runs recorded")
		return 0
	}
	w := tabwriter.NewWriter(stdout, 2, 8, 2, ' ', 0)
	fmt.Fprintln(w, "PROBLEM\tWHEN\tDRIVER\tFIXED\tVERIFIED\tVERDICT")
	for _, e := range entries {
		fmt.Fprintf(w, "%s\t%s\t%s\t%d/%d\t%v\t%s\n", e.Problem,
			e.At.Format("2006-01-02"), e.Driver, e.Fixed, e.Total, e.Verified, e.Verdict)
	}
	if err := w.Flush(); err != nil {
		return 1
	}
	if stale := redteam.Stale(entries); len(stale) > 0 {
		fmt.Fprintf(stdout, "\n%d problem(s) flagged too-easy: rework them before using them again\n", len(stale))
	}
	return 0
}
