// Package cli dispatches interviews subcommands. Commands register here as
// the platform grows; Run stays the single entry point so main stays trivial
// and everything is testable against injected writers.
package cli

import (
	"fmt"
	"io"

	"github.com/sean-reid/interviews/internal/version"
)

const usage = `interviews - run technical interviews that measure resourcefulness

Usage:
  interviews <command> [args]

Commands:
  version   print the version
  help      show this help
`

// Run executes the command line and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stdout, usage)
		return 0
	}
	switch args[0] {
	case "version":
		fmt.Fprintln(stdout, version.Version)
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "interviews: unknown command %q\n\n", args[0])
		fmt.Fprint(stderr, usage)
		return 2
	}
}
