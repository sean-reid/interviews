package redteam

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/sean-reid/interviews/internal/debug"
)

// DebugRun calibrates one debugging problem: bring the broken environment up,
// hand an agent exactly what a candidate gets, then score with the same fault
// checks that grade a real session.
//
// The agent works in a scratch directory outside the content tree, so it
// cannot read fault scripts or interviewer notes even if it goes looking.
func DebugRun(ctx context.Context, e *debug.Engine, d Driver, out io.Writer, budget time.Duration) (*Entry, error) {
	if err := d.Available(ctx); err != nil {
		return nil, err
	}
	scratch, err := os.MkdirTemp("", "interviews-redteam-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(scratch) }()

	pack, err := e.Pack()
	if err != nil {
		return nil, err
	}
	entry := &Entry{
		Problem: e.Variant.Problem, Type: "debugging", Seed: e.Variant.InterviewID,
		Driver: d.Name(), At: time.Now(), Budget: budget.String(),
	}

	fmt.Fprintf(out, "== calibrating %s pack %s against %s (budget %s)\n",
		entry.Problem, pack, d.Name(), budget)
	if err := e.Up(ctx); err != nil {
		return nil, fmt.Errorf("environment: %w", err)
	}
	if err := e.Break(ctx); err != nil {
		return nil, fmt.Errorf("injecting faults: %w", err)
	}

	kubeconfig := filepath.Join(e.Workdir, "kubeconfig")
	if _, err := os.Stat(kubeconfig); err != nil {
		kubeconfig = ""
	}
	prompt, err := candidatePrompt(e.Scenario.Problem, e.Variant, debugEnvNotes(kubeconfig, namespaceOf(e)))
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(scratch, "BRIEF.md"), []byte(prompt), 0o644); err != nil {
		return nil, err
	}
	if kubeconfig != "" {
		// The agent's shell needs the same access a candidate's would have.
		if err := os.Setenv("KUBECONFIG", kubeconfig); err != nil {
			return nil, err
		}
	}

	attempt, runErr := d.Run(ctx, Task{
		Problem: entry.Problem, Prompt: prompt, Dir: scratch, Budget: budget,
	})
	if attempt != nil {
		entry.Model, entry.Turns = attempt.Model, attempt.Turns
	}
	if runErr != nil {
		entry.Verdict, entry.Notes = Inconclusive, runErr.Error()
		return entry, nil
	}

	statuses, err := e.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("scoring: %w", err)
	}
	for _, s := range statuses {
		entry.Total++
		if s.Fixed {
			entry.Fixed++
		}
	}
	entry.Verified = e.Verify(ctx) == nil
	entry.Verdict = Judge(entry.Fixed, entry.Total, entry.Verified)
	if attempt != nil && attempt.TimedOut {
		entry.Notes = "budget reached, which is the expected ending"
	}

	fmt.Fprintf(out, "== %s: fixed %d/%d, verify %v, verdict %s\n",
		entry.Problem, entry.Fixed, entry.Total, entry.Verified, entry.Verdict)
	return entry, nil
}

func namespaceOf(e *debug.Engine) string {
	if e.Scenario.Env.Kind == nil {
		return ""
	}
	ns, err := e.RenderString(e.Scenario.Env.Kind.Namespace)
	if err != nil {
		return e.Scenario.Env.Kind.Namespace
	}
	return ns
}
