package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/sean-reid/interviews/internal/interview"
	"github.com/sean-reid/interviews/internal/taxonomy"
)

// Bounds on the cloud steps. A provision that has not answered by then is a
// problem to look at, not something to keep waiting on.
const (
	provisionTimeout = 10 * time.Minute
	destroyTimeout   = 10 * time.Minute
	bootTimeout      = 12 * time.Minute
	bootPoll         = 15 * time.Second
)

// remoteOptions are what a provisioned session needs beyond the problem.
type remoteOptions struct {
	TTLMinutes   int
	InstanceType string
	NoWait       bool
	Infra        string
}

// StateKey is where one interview's terraform state lives in the evidence
// bucket. A key per interview, in the bucket rather than on a laptop, so any
// interviewer can destroy any host and losing a checkout strands nothing.
// The prefix is separate from the evidence prefix because the host's own role
// may write evidence and must not reach state.
func StateKey(seed string) string { return "state/" + seed + "/terraform.tfstate" }

// startRemote provisions a host and records the session.
func startRemote(problem, seed string, level taxonomy.Level, opts remoteOptions, stdout, stderr io.Writer) int {
	cfg := interview.LoadConfig()
	if cfg.AWS == nil {
		fmt.Fprintf(stderr, "interviews start: this machine has no cloud setup yet\n"+
			"  interviews setup aws --region <region> --bucket <name>\n")
		return 1
	}
	a := cfg.AWS
	root, err := infraRoot(opts.Infra)
	if err != nil {
		fmt.Fprintf(stderr, "interviews start: %v\n", err)
		return 1
	}
	dir := filepath.Join(root, "interview")
	env := os.Environ()
	if a.Profile != "" {
		env = append(env, "AWS_PROFILE="+a.Profile)
	}
	who, err := callerIdentity(env)
	if err != nil {
		fmt.Fprintf(stderr, "interviews start: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "session %s: %s\naccount: %s\nregion:  %s\n\n", seed, problem, who, a.Region)

	// Recorded before anything is built, so a provision that dies partway
	// leaves a record naming the workspace that holds the orphans.
	rec := &interview.Session{
		Seed: seed, Problem: problem, Level: level, Type: taxonomy.Debugging,
		Mode: interview.AWS, CreatedAt: time.Now(),
		TerraformDir: dir, TTLMinutes: opts.TTLMinutes,
		Evidence: fmt.Sprintf("s3://%s/%s/", a.Bucket, seed),
	}
	if err := interview.Save(rec); err != nil {
		fmt.Fprintf(stderr, "interviews start: %v\n", err)
		return 1
	}

	if err := initBackend(stdout, stderr, env, dir, a, seed); err != nil {
		fmt.Fprintf(stderr, "interviews start: %v\n", err)
		return 1
	}
	args := []string{"apply", "-auto-approve", "-input=false",
		"-var", "problem=" + problem, "-var", "seed=" + seed,
		"-var", "region=" + a.Region,
		"-var", "evidence_bucket=" + a.Bucket,
		"-var", "repo_tarball_s3_uri=" + a.TarballURI,
		"-var", "ttl_minutes=" + strconv.Itoa(opts.TTLMinutes)}
	if opts.InstanceType != "" {
		args = append(args, "-var", "instance_type="+opts.InstanceType)
	}
	if err := runBounded(stdout, stderr, env, dir, provisionTimeout, "terraform", args...); err != nil {
		fmt.Fprintf(stderr, "interviews start: %v\n", err)
		fmt.Fprintf(stderr, "\nwhatever came up is recorded in s3://%s/%s. interviews end tears it down,\nfrom this machine or any other.\n", a.Bucket, StateKey(seed))
		return 1
	}

	out, err := outputs(env, dir)
	if err != nil {
		fmt.Fprintf(stderr, "interviews start: reading terraform outputs: %v\n", err)
		return 1
	}
	rec.CandidateURL, rec.ObserverURL = out["candidate_url"], out["observer_url"]
	rec.AppURL, rec.Host = out["app_url"], out["public_ip"]
	if p := out["evidence_path"]; p != "" {
		rec.Evidence = p
	}
	if err := interview.Save(rec); err != nil {
		fmt.Fprintf(stderr, "interviews start: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "\nhost %s, self-destructs in %d minutes\n", rec.Host, opts.TTLMinutes)
	if opts.NoWait {
		fmt.Fprintf(stdout, "not waiting for boot. interviews sessions show prints the URLs.\n")
		return 0
	}
	if err := waitForHost(stdout, rec.Host); err != nil {
		fmt.Fprintf(stderr, "\ninterviews start: %v\n", err)
		fmt.Fprintf(stderr, "the host is up but not serving yet. The URLs are recorded either way:\n"+
			"  interviews sessions show --seed %s\n", seed)
		return 1
	}
	fmt.Fprintf(stdout, "\ncandidate: %s\nobserver:  %s\n", rec.CandidateURL, rec.ObserverURL)
	if rec.AppURL != "" {
		fmt.Fprintf(stdout, "app:       %s (down while the faults are in)\n", rec.AppURL)
	}
	fmt.Fprintf(stdout, "\nlog hints with:  interviews hint \"what you said\"\nend with:        interviews end\n")
	return 0
}

// endRemote pulls the evidence before destroying anything, then tears the
// host down. Order matters: the bucket outlives the host, but a destroy that
// runs first removes the only reason to have provisioned it.
func endRemote(rec *interview.Session, stdout, stderr io.Writer) int {
	cfg := interview.LoadConfig()
	env := os.Environ()
	if cfg.AWS != nil && cfg.AWS.Profile != "" {
		env = append(env, "AWS_PROFILE="+cfg.AWS.Profile)
	}
	dir := rec.TerraformDir
	if dir == "" {
		fmt.Fprintf(stderr, "interviews end: session %s records no terraform directory\n", rec.Seed)
		return 1
	}

	if rec.Evidence != "" {
		local := filepath.Join(os.TempDir(), "interviews-evidence-"+rec.Seed)
		if err := os.MkdirAll(local, 0o700); err != nil {
			fmt.Fprintf(stderr, "interviews end: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "pulling evidence from %s\n", rec.Evidence)
		if err := runIn(stdout, stderr, env, "", "aws", "s3", "sync", rec.Evidence, local); err != nil {
			// Reported, not fatal: the host is still costing money and the
			// bucket keeps whatever synced, so teardown carries on.
			fmt.Fprintf(stderr, "interviews end: could not pull the evidence: %v\n", err)
		} else {
			fmt.Fprintf(stdout, "evidence: %s\n", local)
			rec.Evidence = local
		}
	}

	if cfg.AWS == nil {
		fmt.Fprintf(stderr, "interviews end: this machine has no cloud setup, so it cannot reach the state\n")
		return 1
	}
	if err := initBackend(stdout, stderr, env, dir, cfg.AWS, rec.Seed); err != nil {
		fmt.Fprintf(stderr, "interviews end: %v\n", err)
		return 1
	}
	if err := runBounded(stdout, stderr, env, dir, destroyTimeout,
		"terraform", "destroy", "-auto-approve", "-input=false",
		"-var", "problem="+rec.Problem, "-var", "seed="+rec.Seed,
		"-var", "region="+regionOf(cfg), "-var", "evidence_bucket="+bucketOf(cfg),
		"-var", "repo_tarball_s3_uri="+tarballOf(cfg)); err != nil {
		fmt.Fprintf(stderr, "interviews end: %v\n", err)
		fmt.Fprintf(stderr, "\nthe host may still be running and still costing money. Its state is at\ns3://%s/%s, so this is retryable from anywhere.\n", bucketOf(cfg), StateKey(rec.Seed))
		return 1
	}
	rec.EndedAt = time.Now()
	if err := interview.Save(rec); err != nil {
		fmt.Fprintf(stderr, "interviews end: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "host destroyed. The evidence bucket keeps %s/%s.\n", bucketOf(cfg), rec.Seed)
	return 0
}

func regionOf(c *interview.Config) string {
	if c.AWS == nil {
		return ""
	}
	return c.AWS.Region
}

func bucketOf(c *interview.Config) string {
	if c.AWS == nil {
		return ""
	}
	return c.AWS.Bucket
}

func tarballOf(c *interview.Config) string {
	if c.AWS == nil {
		return ""
	}
	return c.AWS.TarballURI
}

// initBackend points the module at this interview's state key. Reconfigure
// rather than migrate: the module directory is shared between sessions, and
// each init is switching to a different interview's state, not moving one.
func initBackend(stdout, stderr io.Writer, env []string, dir string, a *interview.AWSSetup, seed string) error {
	return runIn(stdout, stderr, env, dir, "terraform", "init", "-input=false", "-reconfigure",
		"-backend-config=bucket="+a.Bucket,
		"-backend-config=key="+StateKey(seed),
		"-backend-config=region="+a.Region,
		"-backend-config=use_lockfile=true")
}

// outputs reads every terraform output, including the sensitive ones, which
// is where the URLs live.
func outputs(env []string, dir string) (map[string]string, error) {
	cmd := exec.Command("terraform", "output", "-json")
	cmd.Dir, cmd.Env = dir, env
	raw, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var decoded map[string]struct {
		Value any `json:"value"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(decoded))
	for k, v := range decoded {
		if s, ok := v.Value.(string); ok {
			out[k] = s
		}
	}
	return out, nil
}

// waitForHost polls the health endpoint until the host serves it. Caddy
// answers well before the environment finishes building, so this says the
// host is reachable, not that the interview is ready.
func waitForHost(out io.Writer, ip string) error {
	if ip == "" {
		return fmt.Errorf("no public ip recorded")
	}
	url := fmt.Sprintf("https://%s.sslip.io/healthz", ip)
	fmt.Fprintf(out, "waiting for %s\n", url)
	client := &http.Client{Timeout: 10 * time.Second}
	deadline := time.Now().Add(bootTimeout)
	for {
		res, err := client.Get(url)
		if err == nil {
			_ = res.Body.Close()
			if res.StatusCode == http.StatusOK {
				fmt.Fprintf(out, "host answering\n")
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not answer within %s", url, bootTimeout)
		}
		fmt.Fprint(out, ".")
		time.Sleep(bootPoll)
	}
}
