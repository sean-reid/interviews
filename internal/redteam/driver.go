// Package redteam drives a frontier coding agent at a problem the way an
// unassisted candidate would meet it, then records what it managed and how
// fast. As models improve, problems get easier; the ledger this package
// keeps is how that erosion becomes visible instead of surprising.
package redteam

import (
	"context"
	"fmt"
	"time"
)

// Task is what an agent is given. The working directory is a scratch copy
// outside the content tree: an agent must never see interviewer material,
// so nothing here carries a path into the problem directory.
type Task struct {
	Problem string
	Prompt  string
	Dir     string
	Budget  time.Duration
}

// Attempt is one agent run's raw record.
type Attempt struct {
	Driver     string        `json:"driver"`
	Model      string        `json:"model,omitempty"`
	Turns      int           `json:"turns,omitempty"`
	Duration   time.Duration `json:"duration_ns"`
	Transcript string        `json:"transcript_path,omitempty"`
	// TimedOut records that the budget cut the run short, which is the
	// normal outcome: these problems are meant to outlast the clock.
	TimedOut bool `json:"timed_out"`
}

// Driver runs an agent against a task.
type Driver interface {
	Name() string
	// Available reports whether this driver can run here, so a missing API
	// key or CLI is a clear message rather than a confusing failure.
	Available(ctx context.Context) error
	Run(ctx context.Context, t Task) (*Attempt, error)
}

// NewDriver returns the named driver. The claude CLI is the default because
// it needs no API key: anyone with the CLI authenticated can calibrate.
func NewDriver(name, model string) (Driver, error) {
	switch name {
	case "", "claude":
		return &claudeDriver{model: model}, nil
	case "api":
		return &apiDriver{model: model}, nil
	default:
		return nil, fmt.Errorf("no driver %q (claude, api)", name)
	}
}
