package cli

import (
	"strings"
	"testing"

	"github.com/sean-reid/interviews/internal/version"
)

func run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errBuf strings.Builder
	code = Run(args, &out, &errBuf)
	return code, out.String(), errBuf.String()
}

func TestVersion(t *testing.T) {
	code, stdout, stderr := run(t, "version")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr)
	}
	if got, want := strings.TrimSpace(stdout), version.Version; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestNoArgsPrintsUsage(t *testing.T) {
	code, stdout, _ := run(t)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout, "Usage:") {
		t.Errorf("stdout missing usage, got %q", stdout)
	}
}

func TestHelp(t *testing.T) {
	for _, arg := range []string{"help", "-h", "--help"} {
		code, stdout, _ := run(t, arg)
		if code != 0 {
			t.Errorf("%s: exit code = %d, want 0", arg, code)
		}
		if !strings.Contains(stdout, "Usage:") {
			t.Errorf("%s: stdout missing usage", arg)
		}
	}
}

func TestUnknownCommand(t *testing.T) {
	code, stdout, stderr := run(t, "frobnicate")
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, `unknown command "frobnicate"`) {
		t.Errorf("stderr missing unknown-command message, got %q", stderr)
	}
}
