package cli

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"slices"
	"text/tabwriter"

	"github.com/sean-reid/interviews/internal/interview"
	"github.com/sean-reid/interviews/internal/version"
)

// tool is one external program the platform shells out to, and what to say
// when it is missing. Nothing lists these anywhere else, so the first time
// an interviewer learns ttyd is needed is otherwise at session start with a
// candidate waiting.
type tool struct {
	bin  string
	what string
	brew string // homebrew formula, empty when there is none
	site string
}

// The groups are capabilities, not packages: what is missing matters less
// than which part of the job it stops.
var toolGroups = []struct {
	name     string
	why      string
	required bool
	tools    []tool
}{
	{
		name: "debugging environments", why: "interviews start, on any debugging problem", required: true,
		tools: []tool{
			{bin: "docker", what: "container runtime, and the compose provider", brew: "docker", site: "https://docs.docker.com/get-docker/"},
			{bin: "kind", what: "kubernetes-flavor problems", brew: "kind", site: "https://kind.sigs.k8s.io/"},
			{bin: "kubectl", what: "every kind scenario script", brew: "kubectl", site: "https://kubernetes.io/docs/tasks/tools/"},
		},
	},
	{
		name: "live sessions", why: "the shared recorded terminal a candidate connects to", required: true,
		tools: []tool{
			{bin: "tmux", what: "the session the candidate and observer share", brew: "tmux", site: "https://github.com/tmux/tmux"},
			{bin: "ttyd", what: "serves that session over http", brew: "ttyd", site: "https://github.com/tsl0922/ttyd"},
			{bin: "asciinema", what: "records the session; the recording is the evidence", brew: "asciinema", site: "https://asciinema.org/"},
		},
	},
	{
		name: "provisioned hosts", why: "running an interview on a disposable EC2 host instead of this machine",
		tools: []tool{
			{bin: "terraform", what: "provisions and destroys the host", brew: "terraform", site: "https://developer.hashicorp.com/terraform/downloads"},
			{bin: "aws", what: "the evidence bucket and the content tarball", brew: "awscli", site: "https://aws.amazon.com/cli/"},
		},
	},
	{
		name: "calibration", why: "interviews redteam, which measures whether a problem is still hard enough",
		tools: []tool{
			{bin: "claude", what: "drives the red-team run; the API driver needs no binary", site: "https://claude.com/claude-code"},
		},
	},
}

// cmdDoctor reports what this machine can and cannot do. It exits non-zero
// only when the local interview path is broken: someone who runs interviews
// on a provisioned host does not need terraform to be a failure.
func cmdDoctor(args []string, stdout, stderr io.Writer) int {
	fs := newBareFlagSet("doctor", stderr)
	if pos, err := parsePermuted(fs, args); err != nil {
		return parseExit(err)
	} else if len(pos) > 0 {
		return usageErr("doctor", stderr)
	}

	fmt.Fprintf(stdout, "interviews %s on %s/%s\n\n", version.Version, runtime.GOOS, runtime.GOARCH)
	contentOK := contentReport(stdout, stderr)
	var missing []tool
	broken := false
	for _, g := range toolGroups {
		// One writer per group: a shared one lines every group's columns up
		// with every other group's, which spreads them across the terminal.
		w := tabwriter.NewWriter(stdout, 2, 8, 2, ' ', 0)
		label := g.name
		if !g.required {
			label += " (optional)"
		}
		fmt.Fprintf(w, "%s\t\t%s\n", label, g.why)
		for _, t := range g.tools {
			path, err := exec.LookPath(t.bin)
			if err != nil {
				fmt.Fprintf(w, "  %s\tmissing\t%s\n", t.bin, t.what)
				missing = append(missing, t)
				if g.required {
					broken = true
				}
				continue
			}
			fmt.Fprintf(w, "  %s\tok\t%s\n", t.bin, path)
		}
		if err := w.Flush(); err != nil {
			return 1
		}
		fmt.Fprintln(stdout)
	}
	if len(missing) == 0 {
		fmt.Fprintln(stdout, "everything the platform shells out to is here")
	} else {
		fmt.Fprintln(stdout, "install what you need:")
		for _, t := range missing {
			if t.brew != "" && runtime.GOOS == "darwin" {
				fmt.Fprintf(stdout, "  brew install %s\n", t.brew)
				continue
			}
			fmt.Fprintf(stdout, "  %s: %s\n", t.bin, t.site)
		}
	}
	if broken {
		fmt.Fprintln(stdout, "\nan interview on this machine needs the groups above that are not optional")
	}
	strayReport(stdout)
	if broken || !contentOK {
		return 1
	}
	return 0
}

// strayReport asks the account what is running and says what nothing local
// accounts for: hosts no record here knows about, hosts still up after end
// reported them destroyed, and elastic ips attached to nothing. sessions
// --remote finds the same things, but only when someone remembers to ask,
// and a forgotten host is exactly the one nobody asks about. Findings never
// fail doctor: a stray host bills the account, it does not stop this
// machine running an interview.
func strayReport(stdout io.Writer) {
	cfg := interview.LoadConfig().AWS
	if cfg == nil {
		// No cloud setup, so there is no account to check.
		return
	}
	ctx := context.Background()
	strays, herr := strayHosts(ctx, cfg)
	stranded, aerr := strandedAddresses(ctx, cfg)
	fmt.Fprintln(stdout)
	if herr == nil && aerr == nil && len(strays) == 0 && stranded == 0 {
		fmt.Fprintln(stdout, "aws  nothing stray in the account: no unaccounted hosts, no idle elastic ips")
		return
	}
	// A check that could not run is reported as exactly that: silence here
	// would read as a clean account.
	if herr != nil {
		fmt.Fprintf(stdout, "aws  could not check the account for stray hosts: %v\n", herr)
	}
	if aerr != nil {
		fmt.Fprintf(stdout, "aws  could not check the account for stranded elastic ips: %v\n", aerr)
	}
	if len(strays) > 0 {
		fmt.Fprintln(stdout, "aws  the account disagrees with the records on this machine:")
		w := tabwriter.NewWriter(stdout, 2, 8, 2, ' ', 0)
		for _, r := range strays {
			state := r.state
			if r.elsewhere {
				state += ", no record on this machine"
			}
			fmt.Fprintf(w, "  %s\t%s\t%s\n", r.s.Seed, r.s.Problem, state)
		}
		if err := w.Flush(); err != nil {
			return
		}
	}
	switch {
	case aerr != nil:
	case stranded == 1:
		fmt.Fprintln(stdout, "aws  1 elastic ip attached to nothing and billing")
	case stranded > 1:
		fmt.Fprintf(stdout, "aws  %d elastic ips attached to nothing and billing\n", stranded)
	}
	if len(strays) > 0 || stranded > 0 {
		fmt.Fprintln(stdout, "  these cost money: interviews sessions --remote has the detail, interviews end <seed> tears one down")
	}
}

// strayHosts is the reconciliation sessions --remote does, reduced to the
// rows that demand action: every disagreement between the account and the
// registry, plus hosts no record here claims.
func strayHosts(ctx context.Context, cfg *interview.AWSSetup) ([]sessionRow, error) {
	hosts, err := describeHosts(ctx, cfg)
	if err != nil {
		return nil, err
	}
	rows, known, err := localRows(false)
	if err != nil {
		return nil, err
	}
	rows = reconcile(rows, known, hosts)
	return slices.DeleteFunc(rows, func(r sessionRow) bool {
		return r.rank != rankStranded && !r.elsewhere
	}), nil
}

// contentReport says where the problems are and whether the checkout is
// current, and reports whether they load at all. Nothing else fetches:
// doctor is the command you run before a candidate is waiting, so it can
// afford the round trip.
func contentReport(stdout, stderr io.Writer) bool {
	root, from := interview.ContentRoot()
	fmt.Fprintf(stdout, "content  %s  (%s)\n", root, from)
	if _, err := openRegistry(root, false, io.Discard); err != nil {
		// A machine that cannot load its problems cannot run an interview,
		// so this is a failure, not a remark in the report.
		fmt.Fprintf(stderr, "  no problems there: %v\n", err)
		fmt.Fprintf(stderr, "  interviews config set content <path to the problems checkout>/content\n\n")
		return false
	}
	s := contentStatus(root, true)
	switch {
	case s.Repo == "":
		fmt.Fprintf(stdout, "  not a git checkout, so nothing can say whether it is current\n")
	case s.Behind > 0:
		fmt.Fprintf(stdout, "  %d commit(s) behind %s: git -C %s pull\n", s.Behind, s.Upstream, s.Repo)
	case s.Upstream == "":
		fmt.Fprintf(stdout, "  %s has no upstream, so nothing can say whether it is current\n", s.Repo)
	default:
		fmt.Fprintf(stdout, "  up to date with %s\n", s.Upstream)
	}
	if s.Dirty {
		fmt.Fprintf(stdout, "  uncommitted changes under %s\n", root)
	}
	fmt.Fprintln(stdout)
	return true
}
