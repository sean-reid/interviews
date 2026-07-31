package leak

import (
	"fmt"
	"path"
	"strings"
)

// glob is a compiled slash-separated pattern. A "**" segment matches zero or
// more path segments; every other segment matches one segment with path.Match
// semantics. This is the whole pattern language: small enough to test
// exhaustively, no dependency, and fnmatch-familiar.
type glob struct {
	src  string
	segs []string
}

func compileGlob(pattern string) (glob, error) {
	if pattern == "" || pattern != path.Clean(pattern) || strings.HasPrefix(pattern, "/") {
		return glob{}, fmt.Errorf("glob %q: must be a clean relative path", pattern)
	}
	segs := strings.Split(pattern, "/")
	for _, s := range segs {
		if s == "**" {
			continue
		}
		if strings.Contains(s, "**") {
			return glob{}, fmt.Errorf("glob %q: ** must be a whole segment", pattern)
		}
		if _, err := path.Match(s, ""); err != nil {
			return glob{}, fmt.Errorf("glob %q: %w", pattern, err)
		}
	}
	return glob{src: pattern, segs: segs}, nil
}

func (g glob) match(name string) bool {
	return matchSegs(g.segs, strings.Split(name, "/"))
}

func matchSegs(pat, name []string) bool {
	if len(pat) == 0 {
		return len(name) == 0
	}
	if pat[0] == "**" {
		if matchSegs(pat[1:], name) {
			return true
		}
		return len(name) > 0 && matchSegs(pat, name[1:])
	}
	if len(name) == 0 {
		return false
	}
	ok, err := path.Match(pat[0], name[0])
	if err != nil || !ok {
		return false
	}
	return matchSegs(pat[1:], name[1:])
}
