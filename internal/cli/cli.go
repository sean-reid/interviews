// Package cli dispatches interviews subcommands. Commands register in the
// commands map; Run stays the single entry point so main stays trivial and
// everything is testable against injected writers.
package cli

import (
	"fmt"
	"io"

	"github.com/sean-reid/interviews/internal/interview"
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
  sent       mark an offline interview as handed to the candidate
  returned   record where a submission landed
  reviewed   mark the live review done
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
  session    start | stop | evidence | timeline | kubeconfig | proxy by hand

Everywhere:
  config     where the problems are on this machine
  setup      setup aws: evidence bucket and the bundle a host downloads
  doctor     check this machine has what the modes you use need
  version    print the version
  help       this menu, or help <command> for one command

A live interview is start, hint, end. The commands under authoring are the
pieces those are built from, for writing content and for CI.

Commands that read problems take --content <dir>. Without it, the root comes
from interviews config, then $INTERVIEWS_CONTENT, then ./content. --seed
<interview-id> selects one session; without it, commands use the current one.
`

type command func(args []string, stdout, stderr io.Writer) int

// commands is populated in init: cmdHelp reads it to answer help <command>,
// which a composite literal cannot express.
var commands map[string]command

func init() {
	commands = map[string]command{
		"config":    cmdConfig,
		"setup":     cmdSetup,
		"doctor":    cmdDoctor,
		"start":     cmdStart,
		"end":       cmdEnd,
		"hint":      cmdHint,
		"sent":      cmdStage(interview.Sent),
		"returned":  cmdStage(interview.Returned),
		"reviewed":  cmdStage(interview.Reviewed),
		"sessions":  cmdSessions,
		"list":      cmdList,
		"describe":  cmdDescribe,
		"validate":  cmdValidate,
		"env":       cmdEnv,
		"break":     cmdBreak,
		"fault":     cmdFault,
		"prove":     cmdProve,
		"session":   cmdSession,
		"grade":     cmdGrade,
		"bundle":    cmdBundle,
		"redteam":   cmdRedteam,
		"version":   cmdVersion,
		"--version": cmdVersion,
		"-v":        cmdVersion,
		"help":      cmdHelp,
		"-h":        cmdHelp,
		"--help":    cmdHelp,
	}
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

// cmdHelp prints the top-level menu, or one command's own help. It used to
// ignore its argument, so the obvious way to ask about a command answered
// with the menu you were already looking at.
func cmdHelp(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stdout, usage)
		return 0
	}
	name := args[0]
	cmd, ok := commands[name]
	if !ok || name == "help" || name == "-h" || name == "--help" {
		fmt.Fprintf(stderr, "interviews help: no command %q\n\n", name)
		fmt.Fprint(stderr, usage)
		return 2
	}
	// Ask the command itself, so the flag list is the real one rather than a
	// copy of it that goes stale. Its help goes to stdout: it was asked for.
	cmd([]string{"--help"}, stdout, stdout)
	return 0
}
