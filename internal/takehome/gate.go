package takehome

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/sean-reid/interviews/internal/leak"
)

// forbidden are byte strings that must never appear in a bundle file:
// references into the protected directories and unrendered template holes.
// Fail-closed: even a dataset that happens to contain one is rejected.
var forbidden = []string{"interviewer/", "faults/", "{{"}

// checkGate proves the finished bundle leaks nothing. It re-classifies
// every written file with the problem's own classifier and fails on
// anything that is not candidate-visible, then greps every file outside
// .git for the forbidden markers. This runs over the output directory, not
// the inputs, so a bug anywhere upstream still cannot ship a leak.
// writtenPaths lists every file in the finished bundle.
func writtenPaths(fsys fs.FS) []string {
	var out []string
	_ = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			out = append(out, p)
		}
		return nil
	})
	return out
}

func checkGate(dir string, c *leak.Classifier) error {
	fsys := os.DirFS(dir)
	leaks, err := leak.Leaks(fsys, c)
	if err != nil {
		return err
	}
	var bad []string
	// A written path that names a protected directory is a leak even if the
	// classifier were somehow persuaded otherwise, so check the paths too and
	// not only what the classifier says about them.
	for _, name := range writtenPaths(fsys) {
		if leak.Protected(name) && name != AboutName && !strings.HasPrefix(name, ".git/") {
			bad = append(bad, name)
		}
	}
	for _, name := range leaks {
		// The bundle adds exactly two things beyond the candidate files:
		// ABOUT.md and the git repository. Everything else is fatal.
		if name == AboutName || strings.HasPrefix(name, ".git/") {
			continue
		}
		bad = append(bad, name)
	}
	if len(bad) > 0 {
		return fmt.Errorf("leak gate: bundle contains files that are not candidate-visible: %s",
			strings.Join(bad, ", "))
	}

	return fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		for _, needle := range forbidden {
			if bytes.Contains(data, []byte(needle)) {
				return fmt.Errorf("leak gate: %s contains %q", name, needle)
			}
		}
		return nil
	})
}

// writeTarball packs dir into a gzipped tarball at outPath, every entry
// under a top-level root directory. Entries come out in sorted order with
// fixed ownership and times, so the same tree always packs to the same
// bytes.
func writeTarball(dir, root, outPath string) (err error) {
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(outPath)
		}
	}()

	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = path.Join(root, filepath.ToSlash(rel))
		if d.IsDir() {
			hdr.Name += "/"
		}
		hdr.ModTime = time.Unix(0, 0).UTC()
		hdr.AccessTime, hdr.ChangeTime = time.Time{}, time.Time{}
		hdr.Uid, hdr.Gid = 0, 0
		hdr.Uname, hdr.Gname = "", ""
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		src, err := os.Open(p)
		if err != nil {
			return err
		}
		defer func() { _ = src.Close() }()
		_, err = io.Copy(tw, src)
		return err
	})
	if err != nil {
		return err
	}
	if err = tw.Close(); err != nil {
		return err
	}
	if err = gz.Close(); err != nil {
		return err
	}
	return f.Close()
}
