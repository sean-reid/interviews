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

Running an interview:
  list       list problems in the content tree
  start      build an environment, break it, and open a recorded terminal
  hint       log a hint against the running session
  end        stop the session, keep the evidence, tear the environment down
  sessions   what is running, what ended, and the URLs for each
  grade      sheet | score | hint: rubric-first grading artifacts
  bundle     write a take-home or system design bundle (dir or .tar.gz)

Authoring and CI:
  describe   show one problem, optionally with a resolved variant
  validate   check the content tree; exits 1 on any error
  prove      re-prove every fault: inject breaks, documented fix works
  redteam    drive a frontier agent at a problem; ledger records the verdict
  env        up | verify | down a debugging environment by hand
  break      inject the variant's fault pack
  fault      status | fix injected faults (interviewer only)
  session    start | stop | evidence | timeline | kubeconfig by hand
  version    print the version

A live interview is start, hint, end. The commands under authoring are the
pieces those are built from, for writing content and for CI.

Every command takes --content <dir> (default ./content). --seed <interview-id>
selects one session; without it, commands use the current one.
`

type command func(args []string, stdout, stderr io.Writer) int

var commands = map[string]command{
	"start":    cmdStart,
	"end":      cmdEnd,
	"hint":     cmdHint,
	"sessions": cmdSessions,
	"list":     cmdList,
	"describe": cmdDescribe,
	"validate": cmdValidate,
	"env":      cmdEnv,
	"break":    cmdBreak,
	"fault":    cmdFault,
	"prove":    cmdProve,
	"session":  cmdSession,
	"grade":    cmdGrade,
	"bundle":   cmdBundle,
	"redteam":  cmdRedteam,
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
