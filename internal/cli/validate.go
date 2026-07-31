package cli

import (
	"fmt"
	"io"

	"github.com/sean-reid/interviews/internal/registry"
)

type validateOut struct {
	Problems int      `json:"problems"`
	Errors   []string `json:"errors"`
	Warnings []string `json:"warnings"`
}

func cmdValidate(args []string, stdout, stderr io.Writer) int {
	fs, contentRoot := newFlagSet("validate", stderr)
	asJSON := fs.Bool("json", false, "machine-readable output")
	positional, perr := parsePermuted(fs, args)
	if perr != nil {
		return 2
	}
	if len(positional) > 0 {
		fmt.Fprintf(stderr, "interviews validate: unexpected argument %q\n", positional[0])
		return 2
	}
	reg, err := openRegistry(*contentRoot, true, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "interviews validate: %v\n", err)
		return 1
	}

	out := validateOut{Problems: len(reg.Problems()), Errors: []string{}, Warnings: []string{}}
	for _, f := range reg.Findings() {
		if f.Warning {
			out.Warnings = append(out.Warnings, f.String())
		} else {
			out.Errors = append(out.Errors, f.String())
		}
	}

	if *asJSON {
		code := writeJSON(stdout, stderr, out)
		if code != 0 {
			return code
		}
	} else {
		for _, e := range out.Errors {
			fmt.Fprintln(stdout, e)
		}
		for _, w := range out.Warnings {
			fmt.Fprintln(stdout, w)
		}
		fmt.Fprintf(stdout, "%d problems, %d errors, %d warnings\n",
			out.Problems, len(out.Errors), len(out.Warnings))
	}
	if registry.Errors(reg.Findings()) {
		return 1
	}
	return 0
}
