package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/sean-reid/interviews/internal/interview"
	"github.com/sean-reid/interviews/internal/version"
)

// run drives the CLI against a registry of its own. Without that, a test
// picks up whatever session the developer running it has open, and the
// commands that default to the current one behave differently on every
// machine.
func run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	if os.Getenv(interview.HomeEnv) == "" {
		t.Setenv(interview.HomeEnv, t.TempDir())
	}
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

// Asking about a command is the obvious thing to type, and it used to print
// the menu you were already looking at.
func TestHelpForOneCommand(t *testing.T) {
	code, stdout, stderr := run(t, "help", "session")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	// The subcommands used to appear only when the invocation was wrong.
	for _, want := range []string{"session start", "stop:", "evidence:", "timeline:", "kubeconfig:"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("help session missing %q:\n%s", want, stdout)
		}
	}
	if code, _, stderr := run(t, "help", "bogus"); code != 2 || !strings.Contains(stderr, `no command "bogus"`) {
		t.Errorf("help bogus: exit %d, stderr %q", code, stderr)
	}
}

// env --help printed a flag list that never mentioned up, verify, or down.
func TestCommandHelpListsVerbsAndFlags(t *testing.T) {
	for _, tc := range []struct{ cmd, wantVerb, wantFlag string }{
		{"env", "up:", "-purge"},
		{"fault", "status:", "-seed"},
		{"grade", "sheet:", ""},
		{"sessions", "show:", "-all"},
	} {
		_, stdout, stderr := run(t, tc.cmd, "--help")
		out := stdout + stderr
		if !strings.Contains(out, "usage: interviews "+tc.cmd) {
			t.Errorf("%s --help has no synopsis:\n%s", tc.cmd, out)
		}
		if !strings.Contains(out, tc.wantVerb) {
			t.Errorf("%s --help does not list %s:\n%s", tc.cmd, tc.wantVerb, out)
		}
		if tc.wantFlag != "" && !strings.Contains(out, tc.wantFlag) {
			t.Errorf("%s --help does not list %s:\n%s", tc.cmd, tc.wantFlag, out)
		}
	}
}

func TestVersionFlags(t *testing.T) {
	for _, arg := range []string{"version", "--version", "-v"} {
		code, stdout, stderr := run(t, arg)
		if code != 0 {
			t.Fatalf("%s: exit %d, stderr %q", arg, code, stderr)
		}
		if strings.TrimSpace(stdout) != version.Version {
			t.Errorf("%s printed %q, want %q", arg, stdout, version.Version)
		}
	}
}

// The menu has to say which commands run an interview: eleven in one list
// does not, and the audit read prove as a pre-flight check to run first.
func TestTopLevelHelpGroupsByAudience(t *testing.T) {
	_, stdout, _ := run(t, "help")
	for _, want := range []string{"Running an interview:", "Authoring and CI:", "doctor", "start", "prove"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("help missing %q", want)
		}
	}
	if i, j := strings.Index(stdout, "start "), strings.Index(stdout, "prove "); i < 0 || j < 0 || i > j {
		t.Error("prove is listed above start, so the menu reads as a sequence to follow")
	}
}
