package redteam

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// claudeDriver runs the local claude CLI headlessly. It needs no API key,
// only an authenticated CLI, which is the common case on a laptop.
type claudeDriver struct {
	model string
}

func (d *claudeDriver) Name() string { return "claude" }

func (d *claudeDriver) Available(ctx context.Context) error {
	if _, err := exec.LookPath("claude"); err != nil {
		return fmt.Errorf("claude CLI not on PATH: install it or use --driver api")
	}
	out, err := exec.CommandContext(ctx, "claude", "--version").Output()
	if err != nil {
		return fmt.Errorf("claude CLI not usable: %w", err)
	}
	if len(strings.TrimSpace(string(out))) == 0 {
		return errors.New("claude --version printed nothing")
	}
	return nil
}

func (d *claudeDriver) Run(ctx context.Context, t Task) (*Attempt, error) {
	ctx, cancel := context.WithTimeout(ctx, t.Budget)
	defer cancel()

	transcript := filepath.Join(t.Dir, "transcript.jsonl")
	f, err := os.Create(transcript)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	args := []string{
		"-p", t.Prompt,
		"--output-format", "stream-json",
		"--verbose",
		// The agent has to be able to act on the environment for the run to
		// mean anything, and the environment is disposable.
		"--permission-mode", "acceptEdits",
	}
	if d.model != "" {
		args = append(args, "--model", d.model)
	}
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = t.Dir
	cmd.Env = t.environ()
	cmd.Stdout = f
	cmd.Stderr = f

	start := time.Now()
	runErr := cmd.Run()
	elapsed := time.Since(start)

	a := &Attempt{
		Driver:     d.Name(),
		Model:      d.model,
		Duration:   elapsed,
		Transcript: transcript,
		TimedOut:   errors.Is(ctx.Err(), context.DeadlineExceeded),
	}
	a.Turns = countTurns(transcript)
	// A budget kill is the expected ending: these problems outlast the
	// clock by design, so only other failures are errors.
	if runErr != nil && !a.TimedOut {
		return a, fmt.Errorf("claude run failed: %w (transcript at %s)", runErr, transcript)
	}
	return a, nil
}

// countTurns counts assistant messages in a stream-json transcript. Best
// effort: the accounting is informational, and a format change must not
// break calibration runs.
func countTurns(path string) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n := 0
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.Contains(line, `"type":"assistant"`) {
			n++
		}
	}
	return n
}
