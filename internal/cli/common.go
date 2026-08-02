package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"strings"

	"github.com/sean-reid/interviews/internal/debug"
	"github.com/sean-reid/interviews/internal/interview"
	"github.com/sean-reid/interviews/internal/registry"
	"github.com/sean-reid/interviews/internal/variant"
)

// DefaultContentRoot is the last resort when nothing says where content is.
const DefaultContentRoot = interview.FallbackContentRoot

func newFlagSet(name string, stderr io.Writer) (*flag.FlagSet, *string) {
	fs := newBareFlagSet(name, stderr)
	// The default is the resolved root, so --content beats the config, the
	// config beats the environment, and every command gets that order without
	// a line of its own.
	root, _ := interview.ContentRoot()
	content := fs.String("content", root, "content tree root")
	return fs, content
}

// contentRootFor picks the tree a recorded session resolves against. The
// session's own root wins, because it is where the environment was built;
// the configured root describes this machine, not this session, and tearing
// down against the wrong tree strands what it was meant to remove.
func contentRootFor(recorded, flagRoot string, explicit bool) string {
	if recorded == "" || explicit {
		return flagRoot
	}
	return recorded
}

// passed reports whether a flag was actually given on the command line.
// Comparing a value to its default cannot answer that, because the default
// for --content is the resolved root and so it differs from the fallback on
// every machine that has configured one.
func passed(fs *flag.FlagSet, name string) bool {
	seen := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			seen = true
		}
	})
	return seen
}

// newBareFlagSet is for the commands that read the content root from the
// session record instead of a flag.
func newBareFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	// The default usage is "Usage of env:" and a flag list, which never
	// mentions that env takes up, verify, or down. The synopsis is the part
	// that says how to invoke the thing.
	fs.Usage = func() {
		fmt.Fprintf(stderr, "usage: %s\n", synopsis(name))
		if extra := verbsOf(name); extra != "" {
			fmt.Fprintln(stderr, extra)
		}
		fmt.Fprintln(stderr, "\nflags:")
		fs.PrintDefaults()
	}
	return fs
}

// synopses are the one-line invocations, keyed by command name. They head
// --help, answer help <command>, and are what a usage error prints, so
// there is one wording per command rather than three.
var synopses = map[string]string{
	"start":    `interviews start <problem-id> [--level senior] [--seed id] [--no-break] [--remote [--ttl 120] [--instance-type t]]`,
	"hint":     `interviews hint "what you told them" [--minute n] [--seed id]`,
	"end":      `interviews end [<seed>] [--purge]`,
	"sessions": `interviews sessions [--all] [--waiting] [--remote] | sessions show [<seed>] | sessions log [<seed>] [--follow]`,
	"list":     `interviews list [--type TYPE] [--level LEVEL] [--json]`,
	"describe": `interviews describe <problem-id> [--seed id] [--json]`,
	"validate": `interviews validate [--content dir]`,
	"grade":    `interviews grade sheet|score|hint <problem-id> [--seed id]`,
	"bundle":   `interviews bundle <problem-id> -o <dir|file.tar.gz> [--seed id] [--level LEVEL] [--due 120h]`,
	"env":      `interviews env up|verify|down <problem-id> [--seed id] [--set k=v] [--purge]`,
	"break":    `interviews break <problem-id> [--seed id] [--set k=v]`,
	"fault":    `interviews fault status|fix <problem-id> [fault-id] [--seed id]`,
	"prove":    `interviews prove <problem-id> [--pack name] [--set k=v] [--keep]`,
	"session":  `interviews session start|stop|evidence|timeline|kubeconfig <problem-id> [--seed id]`,
	"redteam":  `interviews redteam <problem-id> [--pack name] | interviews redteam ledger`,
	"config":   `interviews config | interviews config set content <path> | interviews config unset content`,
	"sent":     `interviews sent [--seed id]`,
	"returned": `interviews returned <path to the submission> [--seed id]`,
	"reviewed": `interviews reviewed [--seed id]`,
	"setup":    `interviews setup aws --region <region> --bucket <name> [--profile p] [--skip-bucket]`,
	"doctor":   `interviews doctor`,
	"version":  `interviews version`,

	"grade sheet":        `interviews grade sheet <problem-id> [--seed id] [-o file] [--level LEVEL] [--rubric file] [--hints dir]`,
	"grade score":        `interviews grade score <problem-id> [--seed id]`,
	"grade hint":         `interviews grade hint <problem-id> "the hint text" --minute <n> [--seed id] [--workdir dir]`,
	"sessions show":      `interviews sessions show [<seed>]`,
	"session start":      `interviews session start <problem-id> [--seed id] [--base-url URL]`,
	"session stop":       `interviews session stop <problem-id> [--seed id]`,
	"session evidence":   `interviews session evidence <problem-id> [--seed id] [--final] [--s3 s3://bucket/prefix]`,
	"session timeline":   `interviews session timeline <problem-id> [--seed id] --interval 30s --once|--for 70m`,
	"session kubeconfig": `interviews session kubeconfig <problem-id> [--seed id] [--out PATH]`,
	"session proxy":      `interviews session proxy --to <port> [--from 8003]`,
	"setup aws":          `interviews setup aws --region <region> --bucket <name> [--profile p] [--skip-bucket]`,
	"redteam ledger":     `interviews redteam ledger [--problem id] [--json]`,
}

// verbs a parent command dispatches on, so its help lists them instead of
// only appearing when the invocation is already wrong.
var verbs = map[string][]string{
	"setup":   {"aws: create the evidence bucket and upload the platform and content bundle"},
	"config":  {"set content <path>: where the problems are checked out", "unset content: fall back to $INTERVIEWS_CONTENT or ./content"},
	"env":     {"up: build the environment and wait for verify", "verify: run the health check once", "down: tear it down, keeping the session evidence"},
	"fault":   {"status: check every injected fault", "fix: apply the answer key for one fault or all of them"},
	"grade":   {"sheet: render the grading sheet", "score: fill the objective record from the live environment", "hint: log a hint against a session elsewhere"},
	"session": {"start: tmux, recorder, and both terminal endpoints", "stop: kill them and take the final evidence", "evidence: refresh the score and bundle the workdir", "timeline: sample fault state over the session", "kubeconfig: mint the candidate kubeconfig", "proxy: forward a loopback port, which start runs for a compose app"},
	"sessions": {"show: everything about one session, including the URLs",
		"log: what a provisioned host said as it booted, with --follow to watch"},
	"redteam": {"ledger: what past calibration runs found"},
}

func synopsis(name string) string {
	if line, ok := synopses[name]; ok {
		return line
	}
	return "interviews " + name
}

func verbsOf(name string) string {
	list, ok := verbs[name]
	if !ok {
		return ""
	}
	return "\nverbs:\n  " + strings.Join(list, "\n  ")
}

// usageErr prints how to invoke a command and returns the usage exit code.
func usageErr(name string, stderr io.Writer) int {
	fmt.Fprintf(stderr, "usage: %s\n", synopsis(name))
	if extra := verbsOf(name); extra != "" {
		fmt.Fprintln(stderr, extra)
	}
	return 2
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
// contentHint is what to do about a content root that is not there. The
// content lives in its own checkout, so a moved or unconfigured one is the
// ordinary first-run state rather than an exceptional failure.
const contentHint = "set it with: interviews config set content <path to the problems checkout>/content"

func openRegistry(root string, strict bool, stderr io.Writer) (*registry.Registry, error) {
	info, err := os.Stat(root)
	if err != nil {
		// Nine commands resolve content, and all of them used to end here on
		// a bare stat error. doctor, config and setup all know the answer;
		// start, the one running with a candidate waiting, did not. Saying it
		// here says it everywhere.
		return nil, fmt.Errorf("content root %s: %w\n%s", root, err, contentHint)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("content root %s is not a directory\n%s", root, contentHint)
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
// resolvedProblem is a problem, the variant to act on, and where that
// environment's state lives.
type resolvedProblem struct {
	entry   *registry.Entry
	variant *variant.Resolved
	workdir string
	seed    string
}

// resolveProblem turns a problem id and a seed into the variant to act on.
//
// The environment recorded what it was built with, and that beats re-deriving
// from the seed: a session started with --set resolves to different parameters
// without those same flags, so a command would act on values the candidate
// never saw. An explicit --set still wins over both.
//
// This existed twice, and only the grading copy honoured the recorded
// overrides, so the copy that touches the environment was the one acting on
// the wrong parameters.
func resolveProblem(contentRoot, problemID, seed, workdir string, sets []string, stderr io.Writer) (*resolvedProblem, error) {
	seed, workdir, err := resolveTarget(seed, workdir, stderr)
	if err != nil {
		return nil, err
	}
	overrides, err := parseOverrides(sets)
	if err != nil {
		return nil, err
	}
	reg, err := openRegistry(contentRoot, false, stderr)
	if err != nil {
		return nil, err
	}
	entry, ok := reg.Get(problemID)
	if !ok {
		return nil, fmt.Errorf("no problem %q (try interviews list)", problemID)
	}
	v, err := variant.Resolve(problemID, entry.Problem.Manifest.Params, seed, overrides)
	if err != nil {
		return nil, err
	}
	if workdir == "" {
		if workdir, err = debug.DefaultWorkdir(v); err != nil {
			return nil, err
		}
	}
	if st, serr := debug.LoadState(workdir); serr == nil && len(st.Overrides) > 0 {
		merged := maps.Clone(st.Overrides)
		maps.Copy(merged, overrides)
		if !maps.Equal(merged, overrides) {
			recorded, rerr := variant.Resolve(problemID, entry.Problem.Manifest.Params, seed, merged)
			if rerr != nil {
				fmt.Fprintf(stderr, "warning: ignoring the overrides recorded in %s: %v\n", debug.StateFile, rerr)
			} else {
				v = recorded
			}
		}
	}
	return &resolvedProblem{entry: entry, variant: v, workdir: workdir, seed: seed}, nil
}

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
