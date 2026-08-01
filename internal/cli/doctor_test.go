package cli

import (
	"strings"
	"testing"
)

// Nothing else lists what the platform shells out to, so the first time an
// interviewer learns ttyd is needed is otherwise at session start.
func TestDoctorNamesEveryToolAndItsPurpose(t *testing.T) {
	code, stdout, stderr := run(t, "doctor")
	if code != 0 && code != 1 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	for _, want := range []string{
		"docker", "kind", "kubectl", "tmux", "ttyd", "asciinema", "terraform", "aws",
		"debugging environments", "live sessions", "provisioned hosts (optional)",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("doctor says nothing about %q:\n%s", want, stdout)
		}
	}
	// Missing tools have to come with the way to get them.
	if strings.Contains(stdout, "missing") && !strings.Contains(stdout, "install") {
		t.Errorf("doctor reports something missing without saying how to install it:\n%s", stdout)
	}
}

func TestDoctorRejectsArguments(t *testing.T) {
	if code, _, stderr := run(t, "doctor", "everything"); code != 2 ||
		!strings.Contains(stderr, "usage: interviews doctor") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
}
