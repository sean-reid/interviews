package cli

import (
	"fmt"
	"io"
	"os/exec"
	"runtime"
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
		return 2
	} else if len(pos) > 0 {
		return usageErr("doctor", stderr)
	}

	fmt.Fprintf(stdout, "interviews %s on %s/%s\n\n", version.Version, runtime.GOOS, runtime.GOARCH)
	contentReport(stdout)
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
		return 0
	}
	fmt.Fprintln(stdout, "install what you need:")
	for _, t := range missing {
		if t.brew != "" && runtime.GOOS == "darwin" {
			fmt.Fprintf(stdout, "  brew install %s\n", t.brew)
			continue
		}
		fmt.Fprintf(stdout, "  %s: %s\n", t.bin, t.site)
	}
	if broken {
		fmt.Fprintln(stdout, "\nan interview on this machine needs the groups above that are not optional")
		return 1
	}
	return 0
}

// contentReport says where the problems are and whether the checkout is
// current. Nothing else fetches: doctor is the command you run before a
// candidate is waiting, so it can afford the round trip.
func contentReport(stdout io.Writer) {
	root, from := interview.ContentRoot()
	fmt.Fprintf(stdout, "content  %s  (%s)\n", root, from)
	if _, err := openRegistry(root, false, io.Discard); err != nil {
		fmt.Fprintf(stdout, "  no problems there: %v\n", err)
		fmt.Fprintf(stdout, "  interviews config set content <path to the problems checkout>/content\n\n")
		return
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
}
