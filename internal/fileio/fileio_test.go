package fileio

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestWriteAtomicReplacesAndSetsMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "score.json")
	if err := WriteAtomic(path, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(path, []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "second" {
		t.Fatalf("file = %q, %v", raw, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %v, want 0600", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("directory holds %d files, want just the target", len(entries))
	}
}

// A reader landing mid-write must not see a half-written file: the timeline
// and evidence timers read score.json and session.json while commands
// write them.
func TestWriteAtomicNeverExposesAPartialFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "score.json")
	payloads := [][]byte{
		[]byte(strings.Repeat("a", 512*1024)),
		[]byte(strings.Repeat("b", 512*1024)),
	}
	if err := WriteAtomic(path, payloads[0], 0o644); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	var stopOnce sync.Once
	halt := func() {
		stopOnce.Do(func() { close(stop) })
		wg.Wait()
	}
	// The writer has to be gone before the test returns, however it ends.
	t.Cleanup(halt)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if err := WriteAtomic(path, payloads[i%2], 0o644); err != nil {
				t.Error(err)
				return
			}
		}
	}()

	for i := range 300 {
		raw, err := os.ReadFile(path)
		if err != nil {
			halt()
			t.Fatalf("read %d: %v", i, err)
		}
		if !bytes.Equal(raw, payloads[0]) && !bytes.Equal(raw, payloads[1]) {
			halt()
			t.Fatalf("read %d saw %d bytes, which is neither whole payload", i, len(raw))
		}
	}
	halt()
}
