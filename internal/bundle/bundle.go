// Package bundle turns a problem into a candidate drop: the
// candidate-visible files rendered for one variant, plus the front page that
// orients whoever opens it. The bundle is the only artifact that ever leaves
// the content tree, so after writing one the engine re-classifies every
// output file and greps for interviewer-only markers; any hit removes the
// whole bundle (see gate.go).
//
// Each interview type delivered as files declares a spec in about.go. A type
// with no spec cannot be bundled, which is how debugging problems stay out:
// they are delivered as a live session, not as a drop.
package bundle

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"

	"github.com/sean-reid/interviews/internal/content"
	"github.com/sean-reid/interviews/internal/variant"
)

// AboutName is the orientation file written at the bundle root.
const AboutName = "ABOUT.md"

// Write renders p's candidate-visible files for v into outPath: a directory,
// or a gzipped tarball when the path ends in .tar.gz or .tgz.
func Write(p *content.Problem, v *variant.Resolved, outPath string) error {
	sp, ok := specs[p.Manifest.Type]
	if !ok {
		return fmt.Errorf("%s is a %s problem; only take-home and system design problems bundle",
			p.Manifest.ID, p.Manifest.Type)
	}

	if strings.HasSuffix(outPath, ".tar.gz") || strings.HasSuffix(outPath, ".tgz") {
		if _, err := os.Stat(outPath); err == nil {
			return fmt.Errorf("output %s already exists", outPath)
		}
		tmp, err := os.MkdirTemp("", "interviews-bundle-")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		dir := filepath.Join(tmp, p.Manifest.ID)
		if err := writeBundle(p, v, dir, sp); err != nil {
			return err
		}
		return writeTarball(dir, p.Manifest.ID, outPath)
	}

	if err := ensureEmptyDir(outPath); err != nil {
		return err
	}
	if err := writeBundle(p, v, outPath, sp); err != nil {
		_ = os.RemoveAll(outPath) // never leave a partial bundle behind
		return err
	}
	return nil
}

// writeBundle assembles the bundle in dir and runs the leak gate over the
// finished tree. The caller removes dir on error.
func writeBundle(p *content.Problem, v *variant.Resolved, dir string, sp spec) error {
	if err := writeFiles(p, v, dir); err != nil {
		return err
	}
	if err := writeAbout(p, dir, sp); err != nil {
		return err
	}
	if sp.gitInit {
		if err := gitInit(dir); err != nil {
			return err
		}
	}
	return checkGate(dir, p.Classifier)
}

func writeFiles(p *content.Problem, v *variant.Resolved, dir string) error {
	for _, name := range p.Scan.Candidate {
		data, err := fs.ReadFile(p.FS, name)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		info, err := fs.Stat(p.FS, name)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if variant.IsTemplated(name) {
			rendered, err := variant.Render(string(data), v)
			if err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			data = []byte(rendered)
		}
		perm := fs.FileMode(0o644)
		if info.Mode()&0o111 != 0 {
			perm = 0o755
		}
		dst := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, data, perm); err != nil {
			return err
		}
	}
	return nil
}

func writeAbout(p *content.Problem, dir string, sp spec) error {
	hasHarness := false
	for _, name := range p.Scan.Candidate {
		if strings.Contains(name, "harness/") {
			hasHarness = true
			break
		}
	}
	tmpl, err := template.New(AboutName).Parse(sp.about)
	if err != nil {
		return err
	}
	var out bytes.Buffer
	err = tmpl.Execute(&out, struct {
		Title      string
		Hours      string
		HasHarness bool
	}{
		Title:      p.Manifest.Title,
		Hours:      strconv.FormatFloat(p.Manifest.Time.SoftBudgetHours, 'f', -1, 64),
		HasHarness: hasHarness,
	})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, AboutName), out.Bytes(), 0o644)
}

// gitInit gives the candidate a working repository whose single commit
// carries no history and no real identity. The -c identity flags are scoped
// to this one commit in the bundle repo and never touch any other
// repository's configuration.
func gitInit(dir string) error {
	steps := [][]string{
		// Empty --template so git copies nothing from the operator's
		// template directory. The default one is whatever init.templateDir
		// or ~/.config/git/templates points at, and the gate does not look
		// inside .git, so anything living there rode out to the candidate.
		{"init", "-q", "-b", "main", "--template="},
		{"add", "-A"},
		{"-c", "user.name=candidate", "-c", "user.email=candidate@localhost",
			"-c", "commit.gpgsign=false", "-c", "core.hooksPath=", "-c", "core.excludesFile=", "commit", "-q", "-m", "initial drop"},
	}
	for _, args := range steps {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, bytes.TrimSpace(out))
		}
	}
	return nil
}

// ensureEmptyDir creates dir if missing and refuses to write into anything
// that already has contents.
func ensureEmptyDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return os.MkdirAll(dir, 0o755)
	}
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return fmt.Errorf("output directory %s is not empty", dir)
	}
	return nil
}
