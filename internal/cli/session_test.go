package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sean-reid/interviews/internal/grading"
	"github.com/sean-reid/interviews/internal/session"
)

func TestSessionArgErrors(t *testing.T) {
	if code, _, _ := run(t, "session"); code != 2 {
		t.Error("bare session should be a usage error")
	}
	if code, _, _ := run(t, "session", "record", "x"); code != 2 {
		t.Error("unknown session verb should be a usage error")
	}
	if code, _, stderr := run(t, "session", "start", "pipeline-meltdown", "--content", goodRoot); code != 1 ||
		!strings.Contains(stderr, "no session yet") {
		t.Errorf("start without seed: exit %d, stderr %q", code, stderr)
	}
	if code, _, _ := run(t, "session", "timeline", "pipeline-meltdown",
		"--content", goodRoot, "--seed", "s"); code != 2 {
		t.Error("timeline without --once or --for should be a usage error")
	}
	if code, _, _ := run(t, "session", "timeline", "pipeline-meltdown",
		"--content", goodRoot, "--seed", "s", "--once", "--for", "1m"); code != 2 {
		t.Error("timeline with both --once and --for should be a usage error")
	}
	// A non-positive interval would fire continuously against the cluster.
	if code, _, stderr := run(t, "session", "timeline", "pipeline-meltdown",
		"--content", goodRoot, "--seed", "s", "--for", "1m", "--interval", "0"); code != 2 ||
		!strings.Contains(stderr, "--interval") {
		t.Errorf("timeline --interval 0: exit %d, stderr %q, want a usage error naming the flag", code, stderr)
	}
}

func TestSessionStartRequiresState(t *testing.T) {
	code, _, stderr := run(t, "session", "start", "pipeline-meltdown",
		"--content", goodRoot, "--seed", "test-seed", "--workdir", t.TempDir())
	if code != 1 || !strings.Contains(stderr, "env up") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
}

// The timeline and evidence paths run the testdata check scripts for
// real through the exec runner; nothing is faked below the CLI.
func TestSessionTimelineOnce(t *testing.T) {
	wd := stateFor(t, "01-image-typo", "02-net-policy")
	code, _, stderr := run(t, "session", "timeline", "pipeline-meltdown", "--once",
		"--content", goodRoot, "--seed", "test-seed", "--set", "fault_pack=pack-b", "--workdir", wd)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	raw, err := os.ReadFile(filepath.Join(wd, session.TimelineFile))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 3 {
		t.Fatalf("timeline lines = %d: %q", len(lines), raw)
	}
	for _, line := range lines {
		var v map[string]any
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			t.Errorf("invalid JSONL %q: %v", line, err)
		}
	}
}

func TestSessionEvidenceBundles(t *testing.T) {
	wd := stateFor(t, "01-image-typo")
	code, _, stderr := run(t, "session", "evidence", "pipeline-meltdown", "--final",
		"--content", goodRoot, "--seed", "test-seed", "--set", "fault_pack=pack-b", "--workdir", wd)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(wd, session.EvidenceFile)); err != nil {
		t.Errorf("no evidence bundle: %v", err)
	}
	score, err := grading.LoadScore(wd)
	if err != nil || score == nil || score.Total != 1 {
		t.Errorf("score = %+v, %v", score, err)
	}
}

func TestSessionKubeconfigRequiresState(t *testing.T) {
	code, _, stderr := run(t, "session", "kubeconfig", "pipeline-meltdown",
		"--content", goodRoot, "--seed", "test-seed", "--workdir", t.TempDir())
	if code != 1 || !strings.Contains(stderr, "env up") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
}

// The host unit is the only caller of the two-account flags, so a rename on
// either side would otherwise only surface on a provisioned box. Every
// session command the unit runs has to parse here, with the placeholders
// systemd fills in and the content root pointed at the fixtures.
func TestHostUnitCommandsParse(t *testing.T) {
	units, err := filepath.Glob(filepath.Join("..", "..", "session", "host", "iv-*"))
	if err != nil || len(units) == 0 {
		t.Fatalf("no unit files: %v", err)
	}
	subs := strings.NewReplacer(
		"${IV_PROBLEM}", "pipeline-meltdown",
		"${IV_SEED}", "test-seed",
		"${IV_HOSTNAME}", "1.2.3.4.sslip.io",
		"${IV_CANDIDATE_TOKEN}", strings.Repeat("a", 32),
		"${IV_OBSERVER_TOKEN}", strings.Repeat("b", 32),
		"${IV_APP_TOKEN}", strings.Repeat("c", 32),
		"${IV_EVIDENCE_S3}", "s3://bucket/test-seed/",
		// A content root that does not exist: every command parses flags
		// before it touches the registry, so the expected exit 1 stays
		// distinguishable from a parse failure, and env up run from a test
		// cannot build a real environment.
		"/opt/interviews/content", filepath.Join(t.TempDir(), "no-content"),
	)
	found := 0
	for _, unit := range units {
		raw, err := os.ReadFile(unit)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(raw), "\n") {
			cmd, ok := strings.CutPrefix(strings.TrimSpace(line), "ExecStart=")
			if !ok {
				continue
			}
			fields := strings.Fields(subs.Replace(strings.TrimPrefix(cmd, "-")))
			// The ttl unit arms systemd-run through sh; only this binary's
			// own invocations have a flag set to hold them to.
			if len(fields) == 0 || !strings.HasSuffix(fields[0], "/interviews") {
				continue
			}
			found++
			args := fields[1:]
			code, _, stderr := run(t, args...)
			if code == 2 || strings.Contains(stderr, "not defined") {
				t.Errorf("%s: %v: exit %d, stderr %q", filepath.Base(unit), args, code, stderr)
			}
		}
	}
	// Every interviews invocation across the units, so a new ExecStart
	// cannot land unparsed: renaming session evidence's --s3 kept the whole
	// suite green while every sync on a live host would have exited 2.
	if found != 6 {
		t.Errorf("interviews commands across the units = %d, want 6", found)
	}
}

// session start spawns the proxy from an argv another package builds, and
// asserting that string as a substring cannot catch a renamed flag. Parse
// the shared argv through the real flag set; the impossible port makes it
// exit 1 after the parse instead of listening.
func TestProxyCommandParses(t *testing.T) {
	args := session.ProxyArgs(99999)
	code, _, stderr := run(t, args...)
	if code == 2 || strings.Contains(stderr, "not defined") {
		t.Errorf("%v: exit %d, stderr %q", args, code, stderr)
	}
	if code != 1 {
		t.Errorf("%v: exit %d, want 1 from the port validation", args, code)
	}
}
