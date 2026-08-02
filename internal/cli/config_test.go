package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sean-reid/interviews/internal/interview"
)

// The platform and the problems live in separate repositories, so the
// content root has to come from somewhere other than the working directory.
func TestConfigSetsTheContentRootForEveryCommand(t *testing.T) {
	t.Setenv(interview.HomeEnv, t.TempDir())
	abs, err := filepath.Abs(goodRoot)
	if err != nil {
		t.Fatal(err)
	}

	if code, stdout, stderr := run(t, "config"); code != 0 ||
		!strings.Contains(stdout, "(default)") || !strings.Contains(stdout, "config set content") {
		t.Errorf("config with nothing set: exit %d, stdout %q stderr %q", code, stdout, stderr)
	}
	if code, stdout, stderr := run(t, "config", "set", "content", abs); code != 0 {
		t.Fatalf("exit %d, stdout %q stderr %q", code, stdout, stderr)
	}

	// The point of the setting: a command run from anywhere finds the content.
	t.Chdir(t.TempDir())
	code, stdout, stderr := run(t, "list")
	if code != 0 {
		t.Fatalf("list outside the tree: exit %d, stderr %q", code, stderr)
	}
	if !strings.Contains(stdout, "pipeline-meltdown") {
		t.Errorf("list found no problems through the config:\n%s", stdout)
	}
}

// A typo here breaks every command afterwards, and the errors those produce
// name the path rather than this setting.
func TestConfigRefusesAPathWithNoProblems(t *testing.T) {
	t.Setenv(interview.HomeEnv, t.TempDir())
	code, _, stderr := run(t, "config", "set", "content", filepath.Join(t.TempDir(), "nope"))
	if code == 0 {
		t.Error("accepted a path that holds no content")
	}
	if !strings.Contains(stderr, "content root") {
		t.Errorf("stderr = %q", stderr)
	}
	if interview.LoadConfig().ContentRoot != "" {
		t.Error("saved the bad path anyway")
	}
}

func TestConfigResolutionOrder(t *testing.T) {
	t.Setenv(interview.HomeEnv, t.TempDir())
	abs, err := filepath.Abs(goodRoot)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(interview.ContentEnv, abs)
	if root, from := interview.ContentRoot(); root != abs || from != interview.FromEnv {
		t.Errorf("env: root = %q from %q", root, from)
	}
	if err := interview.SaveConfig(&interview.Config{ContentRoot: "/from/config"}); err != nil {
		t.Fatal(err)
	}
	if root, from := interview.ContentRoot(); root != "/from/config" || from != interview.FromConfig {
		t.Errorf("config should beat the environment: root = %q from %q", root, from)
	}

	// And the flag beats the config, which the flag's own default carries.
	if code, stdout, stderr := run(t, "list", "--content", abs); code != 0 ||
		!strings.Contains(stdout, "pipeline-meltdown") {
		t.Errorf("--content ignored: exit %d, stderr %q", code, stderr)
	}
}

func TestConfigUnset(t *testing.T) {
	t.Setenv(interview.HomeEnv, t.TempDir())
	abs, err := filepath.Abs(goodRoot)
	if err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := run(t, "config", "set", "content", abs); code != 0 {
		t.Fatalf("set failed: %q", stderr)
	}
	if code, stdout, stderr := run(t, "config", "unset", "content"); code != 0 ||
		!strings.Contains(stdout, "default") {
		t.Errorf("exit %d, stdout %q stderr %q", code, stdout, stderr)
	}
	if interview.LoadConfig().ContentRoot != "" {
		t.Error("still set after unset")
	}
}

func TestConfigUsageErrors(t *testing.T) {
	t.Setenv(interview.HomeEnv, t.TempDir())
	for _, args := range [][]string{
		{"config", "set"},
		{"config", "set", "content"},
		{"config", "unset", "wat"},
		{"config", "frobnicate"},
	} {
		if code, _, _ := run(t, args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}

// A broken config must not stop an interview: every setting has a fallback.
func TestBrokenConfigIsAnEmptyConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv(interview.HomeEnv, home)
	if err := os.WriteFile(filepath.Join(home, interview.ConfigFile), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if root, from := interview.ContentRoot(); from != interview.FromFallback || root != interview.FallbackContentRoot {
		t.Errorf("root = %q from %q, want the fallback", root, from)
	}
}
