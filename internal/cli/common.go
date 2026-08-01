package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/sean-reid/interviews/internal/interview"
	"github.com/sean-reid/interviews/internal/registry"
)

// DefaultContentRoot is where commands look for content unless --content is given.
const DefaultContentRoot = "./content"

func newFlagSet(name string, stderr io.Writer) (*flag.FlagSet, *string) {
	fs := newBareFlagSet(name, stderr)
	content := fs.String("content", DefaultContentRoot, "content tree root")
	return fs, content
}

// newBareFlagSet is for the commands that read the content root from the
// session record instead of a flag.
func newBareFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

// parsePermuted parses flags that appear before, between, or after the
// positional arguments. The flag package stops at the first non-flag, but
// "describe <id> --json" is the natural way to type it.
func parsePermuted(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			break
		}
		positional = append(positional, args[0])
		args = args[1:]
	}
	return positional, nil
}

// openRegistry loads the content root from disk. Commands that browse pass
// strict=false and keep going past content errors; validate reports them.
func openRegistry(root string, strict bool, stderr io.Writer) (*registry.Registry, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("content root %s: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("content root %s is not a directory", root)
	}
	r, err := registry.Load(os.DirFS(root))
	if err != nil {
		return nil, err
	}
	if !strict && registry.Errors(r.Findings()) {
		fmt.Fprintf(stderr, "warning: content has validation errors; run interviews validate\n")
	}
	return r, nil
}

// repeatedFlag collects a flag given multiple times (--set a=b --set c=d).
type repeatedFlag []string

func (r *repeatedFlag) String() string { return strings.Join(*r, ",") }

func (r *repeatedFlag) Set(v string) error {
	*r = append(*r, v)
	return nil
}

func parseOverrides(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(pairs))
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("--set %q: want name=value", p)
		}
		out[k] = v
	}
	return out, nil
}

// resolveTarget fills in what the registry already knows: the session a
// command means when no --seed is given, and where that session's state
// lives. It never fails on a seed the registry has never heard of, because
// a lost or absent record has to degrade to the old behavior rather than
// break a session in progress.
func resolveTarget(seed, workdir string, stderr io.Writer) (string, string, error) {
	if seed == "" {
		s, err := interview.Current()
		if err != nil {
			if errors.Is(err, interview.ErrNoCurrent) {
				return "", "", errors.New("no session yet: run interviews start <problem>, or pass --seed for one this machine did not start")
			}
			return "", "", err
		}
		// Acting on an assumed session without saying which one is how a hint
		// ends up in the wrong interview.
		fmt.Fprintf(stderr, "session %s (%s)\n", s.Seed, s.Problem)
		seed = s.Seed
		if workdir == "" {
			workdir = s.Workdir
		}
		return seed, workdir, nil
	}
	if workdir == "" {
		// The recorded workdir beats deriving one: a session started with --set
		// resolves to a different directory without those same flags, and the
		// derived path would be an empty state directory rather than an error.
		if s, err := interview.Load(seed); err == nil {
			workdir = s.Workdir
		}
	}
	return seed, workdir, nil
}
