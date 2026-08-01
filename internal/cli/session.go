package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/sean-reid/interviews/internal/session"
)

func cmdSession(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usageErr("session", stderr)
	}
	switch args[0] {
	case "start":
		return sessionStart(args[1:], stdout, stderr)
	case "stop":
		return sessionStop(args[1:], stdout, stderr)
	case "evidence":
		return sessionEvidence(args[1:], stdout, stderr)
	case "timeline":
		return sessionTimeline(args[1:], stdout, stderr)
	case "kubeconfig":
		return sessionKubeconfig(args[1:], stdout, stderr)
	case "proxy":
		return sessionProxy(args[1:], stdout, stderr)
	default:
		return usageErr("session", stderr)
	}
}

// managerFor builds the session manager the same way every debugging
// command builds its engine.
func managerFor(contentRoot, problemID, seed, workdir string, sets []string, stdout, stderr io.Writer) (*session.Manager, error) {
	e, err := engineFor(contentRoot, problemID, seed, workdir, sets, stdout, stderr)
	if err != nil {
		return nil, err
	}
	return session.NewManager(e, stdout), nil
}

func sessionStart(args []string, stdout, stderr io.Writer) int {
	fs, contentRoot := newFlagSet("session start", stderr)
	seed := fs.String("seed", "", "interview id selecting the variant")
	workdir := fs.String("workdir", "", "session state directory (default: per-variant cache dir)")
	var sets repeatedFlag
	fs.Var(&sets, "set", "override a parameter (name=value, repeatable)")
	baseURL := fs.String("base-url", "", "public base URL fronting the ttyd ports")
	candTok := fs.String("candidate-token", "", "candidate URL token (default: random)")
	obsTok := fs.String("observer-token", "", "observer URL token (default: random)")
	appTok := fs.String("app-token", "", "app URL token, for a problem that declares an app (default: random)")
	candUser := fs.String("candidate-user", "",
		"account owning the tmux server, so the candidate's panes are its shells (default: this account)")
	socket := fs.String("tmux-socket", "", "tmux server socket path, required with --candidate-user")
	candKube := fs.String("candidate-kubeconfig", "", "kubeconfig to export in the session environment")
	pos, err := parsePermuted(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) != 1 {
		return usageErr("session start", stderr)
	}
	m, err := managerFor(*contentRoot, pos[0], *seed, *workdir, sets, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "interviews session start: %v\n", err)
		return 1
	}
	opts := session.StartOptions{
		BaseURL: *baseURL, CandidateToken: *candTok, ObserverToken: *obsTok, AppToken: *appTok,
		CandidateUser: *candUser, TmuxSocket: *socket, CandidateKubeconfig: *candKube,
	}
	if _, err := m.Start(context.Background(), opts); err != nil {
		fmt.Fprintf(stderr, "interviews session start: %v\n", err)
		return 1
	}
	return 0
}

func sessionStop(args []string, stdout, stderr io.Writer) int {
	contentRoot, seed, workdir, sets, pos, ok := debugFlags("session stop", args, stderr)
	if !ok {
		return 2
	}
	if len(pos) != 1 {
		return usageErr("session stop", stderr)
	}
	m, err := managerFor(contentRoot, pos[0], seed, workdir, sets, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "interviews session stop: %v\n", err)
		return 1
	}
	if err := m.Stop(context.Background()); err != nil {
		fmt.Fprintf(stderr, "interviews session stop: %v\n", err)
		return 1
	}
	return 0
}

func sessionEvidence(args []string, stdout, stderr io.Writer) int {
	fs, contentRoot := newFlagSet("session evidence", stderr)
	seed := fs.String("seed", "", "interview id selecting the variant")
	workdir := fs.String("workdir", "", "session state directory (default: per-variant cache dir)")
	var sets repeatedFlag
	fs.Var(&sets, "set", "override a parameter (name=value, repeatable)")
	final := fs.Bool("final", false, "final pass: record check errors instead of failing")
	s3 := fs.String("s3", "", "upload the bundle under this s3://bucket/prefix")
	pos, err := parsePermuted(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) != 1 {
		return usageErr("session evidence", stderr)
	}
	m, err := managerFor(*contentRoot, pos[0], *seed, *workdir, sets, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "interviews session evidence: %v\n", err)
		return 1
	}
	if err := m.Evidence(context.Background(), session.EvidenceOptions{Final: *final, S3: *s3}); err != nil {
		fmt.Fprintf(stderr, "interviews session evidence: %v\n", err)
		return 1
	}
	return 0
}

func sessionTimeline(args []string, stdout, stderr io.Writer) int {
	fs, contentRoot := newFlagSet("session timeline", stderr)
	seed := fs.String("seed", "", "interview id selecting the variant")
	workdir := fs.String("workdir", "", "session state directory (default: per-variant cache dir)")
	var sets repeatedFlag
	fs.Var(&sets, "set", "override a parameter (name=value, repeatable)")
	interval := fs.Duration("interval", 30*time.Second, "sampling interval")
	once := fs.Bool("once", false, "take a single sample and exit")
	forDur := fs.Duration("for", 0, "keep sampling for this long")
	pos, err := parsePermuted(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) != 1 || *once == (*forDur > 0) {
		return usageErr("session timeline", stderr)
	}
	m, err := managerFor(*contentRoot, pos[0], *seed, *workdir, sets, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "interviews session timeline: %v\n", err)
		return 1
	}
	ctx := context.Background()
	if *once {
		err = m.TimelineTick(ctx)
	} else {
		err = m.TimelineRun(ctx, *interval, *forDur)
	}
	if err != nil {
		fmt.Fprintf(stderr, "interviews session timeline: %v\n", err)
		return 1
	}
	return 0
}

func sessionKubeconfig(args []string, stdout, stderr io.Writer) int {
	fs, contentRoot := newFlagSet("session kubeconfig", stderr)
	seed := fs.String("seed", "", "interview id selecting the variant")
	workdir := fs.String("workdir", "", "session state directory (default: per-variant cache dir)")
	var sets repeatedFlag
	fs.Var(&sets, "set", "override a parameter (name=value, repeatable)")
	out := fs.String("out", "", "also write the kubeconfig here for the candidate's account to read")
	pos, err := parsePermuted(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) != 1 {
		return usageErr("session kubeconfig", stderr)
	}
	m, err := managerFor(*contentRoot, pos[0], *seed, *workdir, sets, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "interviews session kubeconfig: %v\n", err)
		return 1
	}
	// A second account reads the copy through the group it shares with this
	// one, which is the only reason to widen the mode past the owner.
	opts := session.KubeconfigOptions{Out: *out}
	if *out != "" {
		opts.Mode = 0o640
	}
	path, err := m.Kubeconfig(context.Background(), opts)
	if err != nil {
		fmt.Fprintf(stderr, "interviews session kubeconfig: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, path)
	return 0
}

// sessionProxy forwards one loopback port to another and stays running.
// Session start launches it for a compose app, whose published port is
// whatever its variant drew, so the candidate's URL can name a fixed one.
// It is a session process like the rest: stop kills it.
func sessionProxy(args []string, stdout, stderr io.Writer) int {
	fs := newBareFlagSet("session proxy", stderr)
	from := fs.Int("from", session.AppPort, "loopback port to listen on")
	to := fs.Int("to", 0, "loopback port to forward to")
	pos, err := parsePermuted(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) != 0 || *to == 0 {
		return usageErr("session proxy", stderr)
	}
	if err := session.Proxy(context.Background(), *from, *to, stdout); err != nil {
		fmt.Fprintf(stderr, "interviews session proxy: %v\n", err)
		return 1
	}
	return 0
}
