package debug

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strings"
	"syscall"
)

// Runner executes external processes. Providers and fault scripts go
// through it, so engine logic tests against a fake and never touches
// docker or a cluster.
type Runner interface {
	// Command runs a binary (kind, kubectl, docker) and streams its output.
	Command(ctx context.Context, name string, args ...string) error
	// Output runs a binary and returns its stdout.
	Output(ctx context.Context, name string, args ...string) (string, error)
	// Script runs an executable file from dir with extra environment
	// merged over the process environment.
	Script(ctx context.Context, path, dir string, env map[string]string) error
	// Start launches a long-lived background process (ttyd, a recorder)
	// and returns its pid. The process must outlive the CLI, so the
	// context gates startup only; callers stop it by pid.
	Start(ctx context.Context, name string, args ...string) (int, error)
}

// ExecRunner is the real Runner.
type ExecRunner struct {
	Stdout io.Writer
	Stderr io.Writer
}

func (r *ExecRunner) Command(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = r.Stdout
	cmd.Stderr = r.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

func (r *ExecRunner) Output(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stderr = r.Stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return string(out), nil
}

func (r *ExecRunner) Start(_ context.Context, name string, args ...string) (int, error) {
	cmd := exec.Command(name, args...)
	// A new session detaches the process from the CLI's terminal, so it
	// survives the CLI exiting and the terminal closing.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	go func() { _ = cmd.Wait() }()
	return cmd.Process.Pid, nil
}

func (r *ExecRunner) Script(ctx context.Context, path, dir string, env map[string]string) error {
	cmd := exec.CommandContext(ctx, path)
	cmd.Dir = dir
	cmd.Stdout = r.Stdout
	cmd.Stderr = r.Stderr
	cmd.Env = os.Environ()
	for _, k := range slices.Sorted(maps.Keys(env)) {
		cmd.Env = append(cmd.Env, k+"="+env[k])
	}
	return cmd.Run()
}
