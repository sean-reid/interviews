// Package leak decides what a candidate may ever see. Classification is
// fail-closed: a file is interviewer-only unless a manifest glob explicitly
// makes it candidate-visible, and nothing in a protected directory
// (interviewer/, faults/) can be made visible at all. Later stages call
// Leaks over rendered bundles and session filesystems to prove that
// property end to end.
package leak

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"syscall"
	"unicode/utf8"
)

// Class is the visibility of one file within a problem directory.
type Class int

const (
	// InterviewerOnly files never leave the interviewer's side. The default.
	InterviewerOnly Class = iota
	// CandidateVisible files may appear in bundles and candidate sessions.
	CandidateVisible
)

// ProtectedDirs name directories whose contents can never be made
// candidate-visible, whatever the manifest says: interviewer/ holds rubrics
// and references, faults/ holds a debugging scenario's answer-key scripts.
//
// A directory with one of these names is protected at any depth, not just at
// the problem root. Nesting one deeper is a plausible way to organise notes,
// and it must not become a way to publish them.
var ProtectedDirs = []string{"interviewer", "faults"}

// ProtectedFiles name files at the problem root that describe the exercise
// itself: the fault and environment specs, and the live-review material for a
// design problem. None of them is answer-free.
var ProtectedFiles = []string{"env.yaml", "review.yaml", "problem.yaml"}

// isProtectedName reports whether one path segment names something protected.
// Comparison folds case because the classifier and the filesystem must agree:
// on a case-insensitive filesystem, Interviewer/ and interviewer/ are the same
// directory, and a classifier that only knows the lowercase spelling would
// call the other one publishable.
//
// A segment that is not plain ASCII is protected whatever it spells. EqualFold
// is simple case folding, so it does not fold a dotted capital I, a Cyrillic e,
// a fullwidth i, or a name with a zero-width space in it onto the ASCII
// spelling: each of those is a directory that looks exactly like interviewer/
// to a human and publishes like an ordinary candidate file. Deciding those are
// protected costs an author a rename and cannot leak an answer key.
func isProtectedName(seg string, names []string) bool {
	if !isASCII(seg) {
		return true
	}
	for _, n := range names {
		if strings.EqualFold(seg, n) {
			return true
		}
	}
	return false
}

func isASCII(s string) bool {
	for i := range len(s) {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// Classifier classifies paths relative to a problem root.
type Classifier struct {
	candidate []glob
}

// NewClassifier compiles the manifest's candidate globs. It rejects globs
// that could only ever target interviewer/ so a manifest cannot even express
// the intent to leak.
func NewClassifier(candidateGlobs []string) (*Classifier, error) {
	c := &Classifier{}
	for _, p := range candidateGlobs {
		g, err := compileGlob(p)
		if err != nil {
			return nil, err
		}
		for _, seg := range g.segs {
			if isProtectedName(seg, ProtectedDirs) {
				return nil, fmt.Errorf("glob %q: %s/ can never be candidate-visible", p, seg)
			}
		}
		c.candidate = append(c.candidate, g)
	}
	return c, nil
}

// Classify returns the visibility of one slash-separated relative path.
func (c *Classifier) Classify(name string) Class {
	name = path.Clean(name)
	if Protected(name) {
		return InterviewerOnly
	}
	segs := strings.Split(name, "/")
	for _, g := range c.candidate {
		if matchSegs(g.segs, segs) {
			return CandidateVisible
		}
	}
	return InterviewerOnly
}

// Protected reports whether a path is protected regardless of the manifest:
// it names, or sits under, a protected directory at any depth, or it is one of
// the root files that describe the exercise.
func Protected(name string) bool {
	segs := strings.Split(path.Clean(name), "/")
	for _, seg := range segs[:len(segs)-1] {
		if isProtectedName(seg, ProtectedDirs) {
			return true
		}
	}
	last := segs[len(segs)-1]
	if isProtectedName(last, ProtectedDirs) {
		return true
	}
	return len(segs) == 1 && isProtectedName(last, ProtectedFiles)
}

// Globs returns the source form of the compiled candidate globs.
func (c *Classifier) Globs() []string {
	out := make([]string, len(c.candidate))
	for i, g := range c.candidate {
		out[i] = g.src
	}
	return out
}

// Scan is the result of classifying a whole tree in one walk.
type Scan struct {
	Candidate   []string // candidate-visible files, sorted
	Interviewer []string // interviewer-only files, sorted
	// UnmatchedGlobs are candidate globs that matched no file: a sign the
	// manifest and the tree have drifted apart.
	UnmatchedGlobs []string
	// Irregular are entries this package will not vouch for by path alone.
	// A symlink can sit at a candidate path and point at an answer key, and a
	// hardlink is a second name for the same bytes with nothing in the path to
	// show it. Both are counted interviewer-only and reported for the caller
	// to reject.
	Irregular []string
}

// Scan walks fsys once and classifies every file against the compiled globs.
func (c *Classifier) Scan(fsys fs.FS) (*Scan, error) {
	s := &Scan{Candidate: []string{}, Interviewer: []string{}}
	matched := make([]bool, len(c.candidate))
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() || multiplyLinked(d) || !isASCII(p) {
			// Non-ASCII is here rather than only in isProtectedName so the
			// author is told. Treating the path as protected keeps the answer
			// key in, but silently withholding a candidate file the author
			// meant to ship is its own failure; this makes it a hard error.
			s.Irregular = append(s.Irregular, p)
			s.Interviewer = append(s.Interviewer, p)
			return nil
		}
		if Protected(p) {
			s.Interviewer = append(s.Interviewer, p)
			return nil
		}
		segs := strings.Split(path.Clean(p), "/")
		hit := false
		for i, g := range c.candidate {
			if matchSegs(g.segs, segs) {
				hit = true
				matched[i] = true
			}
		}
		if hit {
			s.Candidate = append(s.Candidate, p)
		} else {
			s.Interviewer = append(s.Interviewer, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(s.Candidate)
	sort.Strings(s.Interviewer)
	for i, g := range c.candidate {
		if !matched[i] {
			s.UnmatchedGlobs = append(s.UnmatchedGlobs, g.src)
		}
	}
	return s, nil
}

// Leaks walks fsys and returns every file that is not candidate-visible.
// Run it over a tree that is about to be handed to a candidate: anything it
// returns would be a leak, so the caller must treat non-empty as fatal.
func Leaks(fsys fs.FS, c *Classifier) ([]string, error) {
	s, err := c.Scan(fsys)
	if err != nil {
		return nil, err
	}
	if len(s.Interviewer) == 0 {
		return nil, nil
	}
	return s.Interviewer, nil
}

// multiplyLinked reports whether a regular file has more than one name on
// disk. Path classification describes one name; a second name elsewhere in
// the tree can reach the same bytes, so a file with extra links is not
// something this package can vouch for. Filesystems that do not report link
// counts (an in-memory test FS, for instance) answer false, which leaves
// path classification as the only claim being made.
func multiplyLinked(d fs.DirEntry) bool {
	info, err := d.Info()
	if err != nil {
		return true // cannot tell, so do not vouch for it
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Nlink > 1
}
