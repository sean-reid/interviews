// Package cli dispatches interviews subcommands. Commands register in the
// commands map; Run stays the single entry point so main stays trivial and
// everything is testable against injected writers.
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
  list       list problems in the content tree
  describe   show one problem, optionally with a resolved variant
  validate   check the content tree; exits 1 on any error
  env        up | verify | down a debugging environment
  break      inject the variant's fault pack
  fault      status | fix injected faults (interviewer only)
  prove      re-prove every fault: inject breaks, documented fix works
  grade      sheet | score | hint: rubric-first grading artifacts
  version    print the version
  help       show this help

Every command takes --content <dir> (default ./content). Debugging commands
take --seed <interview-id>, which selects the variant deterministically.
`

type command func(args []string, stdout, stderr io.Writer) int

var commands = map[string]command{
	"list":     cmdList,
	"describe": cmdDescribe,
	"validate": cmdValidate,
	"env":      cmdEnv,
	"break":    cmdBreak,
	"fault":    cmdFault,
	"prove":    cmdProve,
	"grade":    cmdGrade,
	"version":  cmdVersion,
	"help":     cmdHelp,
	"-h":       cmdHelp,
	"--help":   cmdHelp,
}

// Run executes the command line and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return cmdHelp(nil, stdout, stderr)
	}
	cmd, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "interviews: unknown command %q\n\n", args[0])
		fmt.Fprint(stderr, usage)
		return 2
	}
	return cmd(args[1:], stdout, stderr)
}

func cmdVersion(_ []string, stdout, _ io.Writer) int {
	fmt.Fprintln(stdout, version.Version)
	return 0
}

func cmdHelp(_ []string, stdout, _ io.Writer) int {
	fmt.Fprint(stdout, usage)
	return 0
}
