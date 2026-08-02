package debug

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
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
	// Alive reports whether a pid from Start is still running. Start
	// returning is not evidence the process survived its first moment.
	Alive(pid int) bool
}

// exitCode is the process exit status behind a Runner error, or -1 when
// nothing in the chain came from a process. Scripts report meaning through
// their exit codes, so the engine has to read them back.
func exitCode(err error) int {
	var coded interface{ ExitCode() int }
	if errors.As(err, &coded) {
		return coded.ExitCode()
	}
	return -1
}

// ExecRunner is the real Runner.
type ExecRunner struct {
	Stdout io.Writer
	Stderr io.Writer
}

// logfmtWarning is a docker compose warning as logrus prints it. Only
// warnings: an error line, whatever its shape, must reach the user whole.
var logfmtWarning = regexp.MustCompile(`^(?:time="[^"]*" )?level=warning msg="((?:[^"\\]|\\.)*)"`)

// stderrLines rewrites docker compose's raw logfmt warnings, which arrive
// with a timestamp in the middle of otherwise hand-written output, into the
// plain "warning: ..." the rest of this tool speaks. Every other line
// passes through untouched. It buffers to line boundaries, so the caller
// flushes after the process exits.
type stderrLines struct {
	w   io.Writer
	buf []byte
}

func (l *stderrLines) Write(p []byte) (int, error) {
	l.buf = append(l.buf, p...)
	for {
		i := bytes.IndexByte(l.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		line := rewriteWarning(l.buf[:i+1])
		l.buf = l.buf[i+1:]
		if _, err := l.w.Write(line); err != nil {
			return len(p), err
		}
	}
}

// flush writes whatever a process left without a final newline.
func (l *stderrLines) flush() {
	if len(l.buf) == 0 {
		return
	}
	_, _ = l.w.Write(rewriteWarning(l.buf))
	l.buf = nil
}

func rewriteWarning(line []byte) []byte {
	m := logfmtWarning.FindSubmatch(line)
	if m == nil {
		return line
	}
	msg, err := strconv.Unquote(`"` + string(m[1]) + `"`)
	if err != nil {
		return line
	}
	return []byte("warning: " + msg + "\n")
}

func (r *ExecRunner) Command(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	stderr := &stderrLines{w: r.Stderr}
	cmd.Stdout = r.Stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	stderr.flush()
	if err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

func (r *ExecRunner) Output(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	stderr := &stderrLines{w: r.Stderr}
	cmd.Stderr = stderr
	out, err := cmd.Output()
	stderr.flush()
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

// Alive signals 0 at the pid. Start reaps the children it launched, so an
// exited process is gone rather than a zombie that still answers.
func (r *ExecRunner) Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

func (r *ExecRunner) Script(ctx context.Context, path, dir string, env map[string]string) error {
	cmd := exec.CommandContext(ctx, path)
	cmd.Dir = dir
	stderr := &stderrLines{w: r.Stderr}
	cmd.Stdout = r.Stdout
	cmd.Stderr = stderr
	cmd.Env = os.Environ()
	for _, k := range slices.Sorted(maps.Keys(env)) {
		cmd.Env = append(cmd.Env, k+"="+env[k])
	}
	err := cmd.Run()
	stderr.flush()
	return err
}
