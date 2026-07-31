package debug

import (
	"context"
	"io"
	"strconv"
	"testing"
	"time"
)

// TestExecRunnerAliveFollowsRealProcesses runs the real runner against real
// processes: a fake cannot tell us whether signalling a pid means what the
// session stack assumes it means.
func TestExecRunnerAliveFollowsRealProcesses(t *testing.T) {
	r := &ExecRunner{Stdout: io.Discard, Stderr: io.Discard}
	ctx := context.Background()

	long, err := r.Start(ctx, "sleep", "30")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := r.Command(ctx, "kill", "-9", strconv.Itoa(long)); err != nil {
			t.Log(err)
		}
	}()
	if !r.Alive(long) {
		t.Errorf("Alive(%d) = false for a running process", long)
	}

	short, err := r.Start(ctx, "sh", "-c", "exit 0")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for r.Alive(short) {
		if time.Now().After(deadline) {
			t.Fatalf("Alive(%d) still true after the process exited", short)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if r.Alive(0) {
		t.Error("Alive(0) = true")
	}
}
