package debug

import (
	"context"
	"io"
	"strconv"
	"strings"
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

// docker compose prints warnings on stderr as raw logfmt with a timestamp,
// in the middle of otherwise hand-written output. They are rewritten into
// the plain "warning: ..." everything else here speaks; anything that is
// not a logfmt warning, an error above all, passes through untouched.
func TestStderrRewritesComposeLogfmtWarnings(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"timestamped warning",
			`time="2026-08-02T12:00:00Z" level=warning msg="a network exists but was not created by compose" extra=1` + "\n",
			"warning: a network exists but was not created by compose\n"},
		{"bare warning with an escaped quote",
			`level=warning msg="the \"attach\" flag is deprecated"` + "\n",
			"warning: the \"attach\" flag is deprecated\n"},
		{"error stays raw",
			`time="2026-08-02T12:00:00Z" level=error msg="boom"` + "\n",
			`time="2026-08-02T12:00:00Z" level=error msg="boom"` + "\n"},
		{"plain line stays raw", "Step 1/3 : FROM alpine\n", "Step 1/3 : FROM alpine\n"},
	} {
		var out strings.Builder
		l := &stderrLines{w: &out}
		// One byte at a time: compose does not write on line boundaries.
		for i := range tc.in {
			if _, err := l.Write([]byte{tc.in[i]}); err != nil {
				t.Fatal(err)
			}
		}
		l.flush()
		if out.String() != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, out.String(), tc.want)
		}
	}
}

// A process that exits without a final newline still gets its last line
// out; a warning there is rewritten like any other.
func TestStderrFlushesAnUnterminatedLine(t *testing.T) {
	var out strings.Builder
	l := &stderrLines{w: &out}
	if _, err := l.Write([]byte(`level=warning msg="tail"`)); err != nil {
		t.Fatal(err)
	}
	l.flush()
	if out.String() != "warning: tail\n" {
		t.Errorf("got %q", out.String())
	}
}

// Through the real runner and a real process, because the wrapping lives in
// the runner's methods and a unit test of the writer cannot see a method
// that forgot to use it.
func TestExecRunnerCleansWarningsOnStderr(t *testing.T) {
	var errOut strings.Builder
	r := &ExecRunner{Stdout: io.Discard, Stderr: &errOut}
	script := `echo 'time="2026-08-02T12:00:00Z" level=warning msg="orphan containers"' >&2; echo 'real error' >&2`
	if err := r.Command(context.Background(), "sh", "-c", script); err != nil {
		t.Fatal(err)
	}
	if got := errOut.String(); got != "warning: orphan containers\nreal error\n" {
		t.Errorf("stderr = %q", got)
	}
}
