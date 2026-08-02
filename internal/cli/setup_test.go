package cli

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Terraform releases its state lock on an interrupt and not on a kill, and a
// kill is what exec.CommandContext does by default. A destroy that ran past
// its bound therefore left a lock in the bucket blocking every later end,
// from any machine, while the host kept billing.
//
// This asserts the wiring rather than driving a real signal through a shell:
// whether a given /bin/sh runs a trap is the shell's business, and what this
// code owns is asking before killing.
func TestBoundedCommandsAreAskedToStopBeforeBeingKilled(t *testing.T) {
	cmd := boundedCmd(context.Background(), io.Discard, io.Discard, nil, t.TempDir(), "sleep", "30")
	if cmd.Cancel == nil {
		t.Fatal("no Cancel, so the context deadline kills outright and terraform never unlocks")
	}
	if cmd.WaitDelay != unlockGrace {
		t.Errorf("WaitDelay = %s, want the %s unlock grace", cmd.WaitDelay, unlockGrace)
	}

	// Cancel has to deliver an interrupt, not something else. Start a real
	// process, call it, and see which signal arrives.
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Cancel(); err != nil {
		t.Fatal(err)
	}
	err := cmd.Wait()
	var ws *exec.ExitError
	if !asExitError(err, &ws) {
		t.Fatalf("wait returned %v, want an exit error carrying the signal", err)
	}
	status, ok := ws.Sys().(syscall.WaitStatus)
	if !ok {
		t.Skip("this platform does not report the signal")
	}
	if !status.Signaled() || status.Signal() != syscall.SIGINT {
		t.Errorf("process ended with %v, want SIGINT", status.Signal())
	}
}

func asExitError(err error, out **exec.ExitError) bool {
	ee, ok := err.(*exec.ExitError)
	if ok {
		*out = ee
	}
	return ok
}

// runBounded used to index args[0] to name the command in its errors, so
// calling it with none panicked. Every caller happens to pass one.
func TestRunBoundedWithNoArguments(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "exits.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := runBounded(io.Discard, io.Discard, os.Environ(), dir, 10*time.Second, script)
	if err == nil {
		t.Fatal("a non-zero exit should be reported")
	}
	if !strings.Contains(err.Error(), script) {
		t.Errorf("err = %v, want it to name the command", err)
	}
}
