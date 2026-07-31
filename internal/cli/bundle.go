package cli

import (
	"fmt"
	"io"

	"github.com/sean-reid/interviews/internal/bundle"
	"github.com/sean-reid/interviews/internal/registry"
	"github.com/sean-reid/interviews/internal/variant"
)

func cmdBundle(args []string, stdout, stderr io.Writer) int {
	fs, contentRoot := newFlagSet("bundle", stderr)
	seed := fs.String("seed", "", "interview id selecting the variant")
	outPath := fs.String("o", "", "output directory, or a .tar.gz/.tgz path")
	var sets repeatedFlag
	fs.Var(&sets, "set", "override a parameter (name=value, repeatable)")
	pos, err := parsePermuted(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) != 1 || *seed == "" || *outPath == "" {
		fmt.Fprintln(stderr, "usage: interviews bundle <problem-id> --seed <id> [--set k=v] -o <dir|out.tar.gz>")
		return 2
	}
	overrides, err := parseOverrides(sets)
	if err != nil {
		fmt.Fprintf(stderr, "interviews bundle: %v\n", err)
		return 2
	}
	reg, err := openRegistry(*contentRoot, true, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "interviews bundle: %v\n", err)
		return 1
	}
	entry, ok := reg.Get(pos[0])
	if !ok {
		fmt.Fprintf(stderr, "interviews bundle: no problem %q (try interviews list)\n", pos[0])
		return 1
	}
	// A problem that does not validate is not safe to hand out. A visibility
	// glob the classifier rejected, for instance, leaves the problem with no
	// candidate files at all, and the bundle would be a front page and
	// nothing else.
	if findings := reg.FindingsFor(entry.Dir); registry.Errors(findings) {
		fmt.Fprintf(stderr, "interviews bundle: %s does not validate:\n", pos[0])
		for _, f := range findings {
			if !f.Warning {
				fmt.Fprintf(stderr, "  %s\n", f)
			}
		}
		return 1
	}
	v, err := variant.Resolve(pos[0], entry.Problem.Manifest.Params, *seed, overrides)
	if err != nil {
		fmt.Fprintf(stderr, "interviews bundle: %v\n", err)
		return 1
	}
	if err := bundle.Write(entry.Problem, v, *outPath); err != nil {
		fmt.Fprintf(stderr, "interviews bundle: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "wrote bundle for %s (seed %s) to %s\n", pos[0], *seed, *outPath)
	return 0
}
