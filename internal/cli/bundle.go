package cli

import (
	"fmt"
	"io"

	"github.com/sean-reid/interviews/internal/takehome"
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
	reg, err := openRegistry(*contentRoot, false, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "interviews bundle: %v\n", err)
		return 1
	}
	entry, ok := reg.Get(pos[0])
	if !ok {
		fmt.Fprintf(stderr, "interviews bundle: no problem %q (try interviews list)\n", pos[0])
		return 1
	}
	v, err := variant.Resolve(pos[0], entry.Problem.Manifest.Params, *seed, overrides)
	if err != nil {
		fmt.Fprintf(stderr, "interviews bundle: %v\n", err)
		return 1
	}
	if err := takehome.Bundle(entry.Problem, v, *outPath); err != nil {
		fmt.Fprintf(stderr, "interviews bundle: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "wrote bundle for %s (seed %s) to %s\n", pos[0], *seed, *outPath)
	return 0
}
