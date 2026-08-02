package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/sean-reid/interviews/internal/provenance"
)

// ContentStatus is what the content checkout looks like right now. The
// problems live in their own repository, so an interview can be run against
// last month's version of them with nothing to say so.
type ContentStatus struct {
	// Repo is the work tree holding the content, empty when it is not in one.
	Repo string
	// Branch is the checked-out branch, empty on a detached head.
	Branch string
	// Dirty means uncommitted changes under the content root.
	Dirty bool
	// Behind counts commits the upstream has that this checkout does not.
	// Meaningful only with Upstream set.
	Behind   int
	Upstream string
}

// contentStatus inspects the checkout holding root. Every failure is
// reported as "not a repository" rather than as an error: a content tree
// that is a plain directory is a perfectly good content tree.
func contentStatus(root string, fetch bool) ContentStatus {
	var s ContentStatus
	top, err := git(root, "rev-parse", "--show-toplevel")
	if err != nil || top == "" {
		return s
	}
	s.Repo = top
	if fetch {
		// Only doctor does this: it costs a network round trip, and a command
		// about to start an interview must not wait on one.
		_, _ = git(root, "fetch", "--quiet")
	}
	if branch, err := git(root, "rev-parse", "--abbrev-ref", "HEAD"); err == nil && branch != "HEAD" {
		s.Branch = branch
	}
	// Scope the dirty check to the content root, since the rest of the
	// repository is none of our business. The pathspec is "." because git
	// resolves pathspecs after -C has moved into root: passing root again
	// errors for any relative root, including the ./content default.
	if out, err := git(root, "status", "--porcelain", "--", "."); err == nil && out != "" {
		s.Dirty = true
	}
	upstream, err := git(root, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	if err != nil || upstream == "" {
		return s
	}
	s.Upstream = upstream
	if out, err := git(root, "rev-list", "--count", "HEAD..@{upstream}"); err == nil {
		if n, err := strconv.Atoi(out); err == nil {
			s.Behind = n
		}
	}
	return s
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// contentVersion identifies the problems a run is using, for the provenance
// it records. A provisioned host is told by its own user-data which tarball
// version it unpacked; a checkout is its commit, which is the only identity
// a content tree on a laptop has. Two cheap local git calls, and empty when
// the content is neither.
func contentVersion(root string) string {
	if v := os.Getenv(provenance.ContentVersionEnv); v != "" {
		return v
	}
	commit, err := git(root, "rev-parse", "--short", "HEAD")
	if err != nil || commit == "" {
		return ""
	}
	// A dirty tree is not the commit it names, and two interviews run from it
	// are not the same interview.
	if out, err := git(root, "status", "--porcelain", "--", "."); err == nil && out != "" {
		return commit + "-dirty"
	}
	return commit
}

// warnStale says so when the content checkout is behind or dirty, and
// never blocks: this runs as an interview is starting.
func warnStale(root string, stderr io.Writer) {
	s := contentStatus(root, false)
	if s.Repo == "" {
		return
	}
	if s.Behind > 0 {
		fmt.Fprintf(stderr, "warning: content is %d commit(s) behind %s\n  git -C %s pull\n",
			s.Behind, s.Upstream, s.Repo)
	}
	if s.Dirty {
		fmt.Fprintf(stderr, "warning: uncommitted changes under %s\n", root)
	}
}
