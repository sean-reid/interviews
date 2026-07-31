// Package takehome turns a take-home problem into a candidate bundle: the
// candidate-visible files rendered for one variant, an ABOUT.md, and a
// fresh git history. The bundle is the only artifact that ever leaves the
// content tree, so after writing one the engine re-classifies every output
// file and greps for interviewer-only markers; any hit removes the whole
// bundle (see gate.go).
package takehome

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
	"github.com/sean-reid/interviews/internal/taxonomy"
	"github.com/sean-reid/interviews/internal/variant"
)

// AboutName is the orientation file written at the bundle root.
const AboutName = "ABOUT.md"

// aboutText orients the candidate without exposing anything about how the
// problem is parameterized or graded.
const aboutText = `# {{.Title}}

Start with candidate/brief.md; it states the task.

Spend about {{.Hours}} hours. The problem is deliberately larger than that
budget, so nobody is expected to finish; use the time well and stop when it
runs out. When you stop, write STOPPING-POINT.md at the root of this
repository: what works, what does not, and what you would do next and why.
{{- if .HasHarness}}

The harness/ directory holds the tooling for exercising your solution; its
files describe how to run it.
{{- end}}

You may use any resource you like, including AI tools.

When you are done, send the repository back as a zip archive or a git
bundle, for example: git bundle create takehome.bundle --all.
`

// Bundle renders p's candidate-visible files for v into outPath: a
// directory, or a gzipped tarball when the path ends in .tar.gz or .tgz.
// The output is a git repository with a single history-free commit.
func Bundle(p *content.Problem, v *variant.Resolved, outPath string) error {
	// The one type gate for bundling. SysDesign bundles hook in here when
	// that engine lands.
	if p.Manifest.Type != taxonomy.TakeHome {
		return fmt.Errorf("%s is a %s problem; only take-home problems bundle",
			p.Manifest.ID, p.Manifest.Type)
	}

	if strings.HasSuffix(outPath, ".tar.gz") || strings.HasSuffix(outPath, ".tgz") {
		if _, err := os.Stat(outPath); err == nil {
			return fmt.Errorf("output %s already exists", outPath)
		}
		tmp, err := os.MkdirTemp("", "takehome-bundle-")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		dir := filepath.Join(tmp, p.Manifest.ID)
		if err := writeBundle(p, v, dir); err != nil {
			return err
		}
		return writeTarball(dir, p.Manifest.ID, outPath)
	}

	if err := ensureEmptyDir(outPath); err != nil {
		return err
	}
	if err := writeBundle(p, v, outPath); err != nil {
		_ = os.RemoveAll(outPath) // never leave a partial bundle behind
		return err
	}
	return nil
}

// writeBundle assembles the bundle in dir and runs the leak gate over the
// finished tree. The caller removes dir on error.
func writeBundle(p *content.Problem, v *variant.Resolved, dir string) error {
	if err := writeFiles(p, v, dir); err != nil {
		return err
	}
	if err := writeAbout(p, dir); err != nil {
		return err
	}
	if err := gitInit(dir); err != nil {
		return err
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

func writeAbout(p *content.Problem, dir string) error {
	hasHarness := false
	for _, name := range p.Scan.Candidate {
		if strings.HasPrefix(name, "harness/") {
			hasHarness = true
			break
		}
	}
	tmpl, err := template.New(AboutName).Parse(aboutText)
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
		{"init", "-q", "-b", "main"},
		{"add", "-A"},
		{"-c", "user.name=candidate", "-c", "user.email=candidate@localhost",
			"-c", "commit.gpgsign=false", "commit", "-q", "-m", "initial drop"},
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
