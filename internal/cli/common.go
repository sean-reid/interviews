package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/sean-reid/interviews/internal/registry"
)

// DefaultContentRoot is where commands look for content unless --content is given.
const DefaultContentRoot = "./content"

func newFlagSet(name string, stderr io.Writer) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	content := fs.String("content", DefaultContentRoot, "content tree root")
	return fs, content
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
