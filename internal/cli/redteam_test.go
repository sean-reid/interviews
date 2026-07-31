package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sean-reid/interviews/internal/redteam"
)

// Real calibration runs drive a model against a live environment, so the
// tests here cover argument handling and the ledger surface. The scoring and
// prompt-assembly logic is tested in internal/redteam.

func TestRedteamArgErrors(t *testing.T) {
	if code, _, _ := run(t, "redteam", "--content", goodRoot); code != 2 {
		t.Error("redteam without a problem id should be a usage error")
	}
	if code, _, stderr := run(t, "redteam", "nope", "--content", goodRoot); code != 1 ||
		!strings.Contains(stderr, "no problem") {
		t.Errorf("unknown problem: exit %d, stderr %q", code, stderr)
	}
	if code, _, stderr := run(t, "redteam", "slow-aligner", "--content", goodRoot); code != 1 ||
		!strings.Contains(stderr, "only debugging calibration") {
		t.Errorf("takehome problem: exit %d, stderr %q", code, stderr)
	}
	if code, _, stderr := run(t, "redteam", "pipeline-meltdown", "--content", goodRoot,
		"--driver", "gpt"); code != 1 || !strings.Contains(stderr, "no driver") {
		t.Errorf("unknown driver: exit %d, stderr %q", code, stderr)
	}
}

func ledgerWith(t *testing.T, entries ...redteam.Entry) string {
	t.Helper()
	root := t.TempDir()
	for _, e := range entries {
		if err := redteam.Append(root, e); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestRedteamLedgerEmpty(t *testing.T) {
	code, stdout, _ := run(t, "redteam", "ledger", "--ledger-root", t.TempDir())
	if code != 0 || !strings.Contains(stdout, "no calibration runs recorded") {
		t.Errorf("exit %d, stdout %q", code, stdout)
	}
}

func TestRedteamLedgerTable(t *testing.T) {
	at := time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)
	root := ledgerWith(t,
		redteam.Entry{Problem: "orbit-shop", Driver: "claude", At: at,
			Fixed: 2, Total: 7, Verdict: redteam.Holds},
		redteam.Entry{Problem: "relay", Driver: "api", At: at,
			Fixed: 5, Total: 6, Verified: true, Verdict: redteam.TooEasy},
	)
	code, stdout, stderr := run(t, "redteam", "ledger", "--ledger-root", root)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	for _, want := range []string{
		"PROBLEM", "VERDICT", "orbit-shop", "2/7", "holds",
		"relay", "5/6", "too-easy", "1 problem(s) flagged too-easy",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("ledger output missing %q:\n%s", want, stdout)
		}
	}
}

func TestRedteamLedgerStaleAndJSON(t *testing.T) {
	at := time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)
	root := ledgerWith(t,
		redteam.Entry{Problem: "orbit-shop", At: at, Fixed: 1, Total: 7, Verdict: redteam.Holds},
		redteam.Entry{Problem: "relay", At: at, Fixed: 5, Total: 6, Verdict: redteam.TooEasy},
	)
	code, stdout, _ := run(t, "redteam", "ledger", "--ledger-root", root, "--stale", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var entries []redteam.Entry
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}
	if len(entries) != 1 || entries[0].Problem != "relay" {
		t.Errorf("stale entries = %+v, want just relay", entries)
	}
}

func TestRedteamLedgerReportsCorruption(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, redteam.LedgerPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("garbage\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := run(t, "redteam", "ledger", "--ledger-root", root); code != 1 ||
		!strings.Contains(stderr, "ledger.jsonl") {
		t.Errorf("corrupt ledger: exit %d, stderr %q", code, stderr)
	}
}

func TestLedgerDirDefaultsBesideContent(t *testing.T) {
	if got := ledgerDir("./content", ""); got != "." {
		t.Errorf("ledgerDir(./content) = %q, want .", got)
	}
	if got := ledgerDir("/repo/content", ""); got != "/repo" {
		t.Errorf("ledgerDir(/repo/content) = %q, want /repo", got)
	}
	if got := ledgerDir("./content", "/elsewhere"); got != "/elsewhere" {
		t.Errorf("override ignored: %q", got)
	}
}
