// Package leak decides what a candidate may ever see. Classification is
// fail-closed: a file is interviewer-only unless a manifest glob explicitly
// makes it candidate-visible, and nothing under interviewer/ can be made
// visible at all. Later stages call Leaks over rendered bundles and session
// filesystems to prove that property end to end.
package leak

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// Class is the visibility of one file within a problem directory.
type Class int

const (
	// InterviewerOnly files never leave the interviewer's side. The default.
	InterviewerOnly Class = iota
	// CandidateVisible files may appear in bundles and candidate sessions.
	CandidateVisible
)

// InterviewerDir is the directory whose contents can never be made
// candidate-visible, whatever the manifest says.
const InterviewerDir = "interviewer"

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
		if g.segs[0] == InterviewerDir {
			return nil, fmt.Errorf("glob %q: interviewer/ can never be candidate-visible", p)
		}
		c.candidate = append(c.candidate, g)
	}
	return c, nil
}

// Classify returns the visibility of one slash-separated relative path.
func (c *Classifier) Classify(name string) Class {
	name = path.Clean(name)
	if underInterviewerDir(name) {
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

func underInterviewerDir(name string) bool {
	name = path.Clean(name)
	return name == InterviewerDir || strings.HasPrefix(name, InterviewerDir+"/")
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
	// Irregular are entries that are neither directories nor regular files.
	// A symlink can name a candidate-visible path while pointing at an answer
	// key, so classification by path alone cannot vouch for them: they are
	// counted interviewer-only and reported for the caller to reject.
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
		if !d.Type().IsRegular() {
			s.Irregular = append(s.Irregular, p)
			s.Interviewer = append(s.Interviewer, p)
			return nil
		}
		if underInterviewerDir(p) {
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
