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
	"strings"
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
	Infra        string
}

// terraformDataDir is where one interview's terraform working data lives.
// end removes it after a successful destroy: it holds that seed's own full
// copy of the AWS provider, which nothing ever reads again.
func terraformDataDir(seed string) (string, error) {
	home, err := interview.Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "terraform", seed), nil
}

// terraformEnv gives one interview its own terraform data directory while
// every session shares the module source. Without this, two sessions started
// from the same checkout both reconfigure .terraform in place and fight over
// which backend key it points at, which is not hypothetical: two concurrent
// provisions did exactly that, and one came back with no host at all.
func terraformEnv(profile, seed string) ([]string, error) {
	data, err := terraformDataDir(seed)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(data, 0o700); err != nil {
		return nil, err
	}
	env := append(os.Environ(), "TF_DATA_DIR="+data)
	if profile != "" {
		env = append(env, "AWS_PROFILE="+profile)
	}
	return env, nil
}

// StateKey is where one interview's terraform state lives in the evidence
// bucket. A key per interview, in the bucket rather than on a laptop, so any
// interviewer can destroy any host and losing a checkout strands nothing.
// The prefix is separate from the evidence prefix because the host's own role
// may write evidence and must not reach state.
func StateKey(seed string) string {
	return interview.StatePrefix + "/" + seed + "/terraform.tfstate"
}

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
	env, err := terraformEnv(a.Profile, seed)
	if err != nil {
		fmt.Fprintf(stderr, "interviews start: %v\n", err)
		return 1
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
	// Which content this host runs is otherwise unrecoverable: the tarball
	// key never changes and the host does not report what it unpacked.
	if v, verr := tarballVersion(env, a.TarballURI); verr == nil {
		rec.ContentVersion = v
	} else {
		fmt.Fprintf(stderr, "warning: could not read the content version: %v\n", verr)
	}
	if err := interview.Save(rec); err != nil {
		fmt.Fprintf(stderr, "interviews start: %v\n", err)
		return 1
	}
	if err := interview.SetCurrent(seed); err != nil {
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
		// Handed to the host so its own evidence says which content it ran.
		// The key never changes, so the box cannot work this out for itself.
		"-var", "content_version=" + rec.ContentVersion,
		"-var", "ttl_minutes=" + strconv.Itoa(opts.TTLMinutes)}
	if opts.InstanceType != "" {
		args = append(args, "-var", "instance_type="+opts.InstanceType)
	}
	if err := runBounded(stdout, stderr, env, dir, provisionTimeout, "terraform", args...); err != nil {
		fmt.Fprintf(stderr, "interviews start: %v\n", err)
		fmt.Fprintf(stderr, "\nwhatever came up is recorded in s3://%s/%s. interviews end tears it down,\nfrom this machine or any other.\n", a.Bucket, StateKey(seed))
		return 1
	}

	rec.ProvisionedAt = time.Now()

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
	tail := &logTail{env: env, evidence: rec.Evidence}
	if err := waitForHost(stdout, rec.Host, rec.CandidateURL, tail); err != nil {
		fmt.Fprintf(stderr, "\ninterviews start: %v\n", err)
		fmt.Fprintf(stderr, "the URLs are recorded either way: interviews sessions show --seed %s\n"+
			"the host uploads why it failed, which needs no access to the box:\n"+
			"  aws s3 cp %sprovision.log -\n", seed, rec.Evidence)
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
func endRemote(rec *interview.Session, purge bool, stdout, stderr io.Writer) int {
	cfg := interview.LoadConfig()
	profile := ""
	if cfg.AWS != nil {
		profile = cfg.AWS.Profile
	}
	env, err := terraformEnv(profile, rec.Seed)
	if err != nil {
		fmt.Fprintf(stderr, "interviews end: %v\n", err)
		return 1
	}
	dir := rec.TerraformDir
	if dir == "" {
		fmt.Fprintf(stderr, "interviews end: session %s records no terraform directory\n", rec.Seed)
		return 1
	}

	if purge && rec.Evidence != "" {
		// Purging means the evidence is not wanted, so pulling it first and
		// deleting the bucket copy after would be the worst of both. This
		// used to be dropped silently: end --purge on a provisioned session
		// reported success while the bucket kept everything.
		fmt.Fprintf(stdout, "deleting the evidence at %s\n", rec.Evidence)
		if err := runBounded(stdout, stderr, env, "", syncTimeout,
			"aws", "s3", "rm", rec.Evidence, "--recursive"); err != nil {
			fmt.Fprintf(stderr, "interviews end: could not delete the evidence: %v\n", err)
		} else {
			rec.Evidence = ""
		}
	}
	if !purge && rec.Evidence != "" {
		local := filepath.Join(os.TempDir(), "interviews-evidence-"+rec.Seed)
		if err := os.MkdirAll(local, 0o700); err != nil {
			fmt.Fprintf(stderr, "interviews end: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "pulling evidence from %s\n", rec.Evidence)
		if err := runBounded(stdout, stderr, env, "", syncTimeout, "aws", "s3", "sync", rec.Evidence, local); err != nil {
			// Reported, not fatal: the host is still costing money and the
			// bucket keeps whatever synced, so teardown carries on.
			fmt.Fprintf(stderr, "interviews end: could not pull the evidence: %v\n", err)
		} else {
			// The record keeps pointing at the bucket: the local copy is a
			// convenience in a temp directory the OS reaps, and sessions log
			// still needs the URI.
			fmt.Fprintf(stdout, "local copy: %s\n", local)
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
		// A lock outliving the process that took it is the one failure here
		// that retrying cannot clear, and nothing else says how to get out.
		fmt.Fprintf(stderr, "\nIf it says the state is locked and no other end is running, take the ID\nfrom that message and clear it:\n  terraform -chdir=%s force-unlock <id>\n", rec.TerraformDir)
		return 1
	}
	// The data dir holds this seed's own copy of the AWS provider and its
	// backend config, hundreds of megabytes nothing reads after the destroy;
	// five interviews once left 3.2 GB of it behind.
	if data, derr := terraformDataDir(rec.Seed); derr == nil {
		if rerr := os.RemoveAll(data); rerr != nil {
			fmt.Fprintf(stderr, "interviews end: could not remove %s: %v\n", data, rerr)
		}
	}
	rec.EndedAt = time.Now()
	if err := interview.Save(rec); err != nil {
		fmt.Fprintf(stderr, "interviews end: %v\n", err)
		return 1
	}
	if err := interview.ClearCurrent(rec.Seed); err != nil {
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
// The lockfile is readonly for the same reason: TF_DATA_DIR moves the rest
// of init's writes per seed, but .terraform.lock.hcl lands in the module
// directory, where the checked-in copy is the pin and two concurrent starts
// were both rewriting it.
func initBackend(stdout, stderr io.Writer, env []string, dir string, a *interview.AWSSetup, seed string) error {
	return runBounded(stdout, stderr, env, dir, initTimeout, "terraform", "init", "-input=false", "-reconfigure",
		"-lockfile=readonly",
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

// waitForHost waits for the host to be reachable and then for the interview
// to actually be served. Two stages on purpose: the proxy answers /healthz
// long before the session stack exists, so treating that as ready hands over
// URLs that return 502. The candidate route is the honest signal.
func waitForHost(out io.Writer, ip, candidate string, tail *logTail) error {
	if ip == "" {
		return fmt.Errorf("no public ip recorded")
	}
	if err := poll(out, fmt.Sprintf("https://%s.sslip.io/healthz", ip), "host reachable", tail); err != nil {
		return err
	}
	if candidate == "" {
		return nil
	}
	return poll(out, candidate, "interview ready", tail)
}

// poll waits for one url to answer 200. While waiting it prints whatever the
// host has said about itself, so a slow provision reads as progress instead
// of being indistinguishable from a stuck one. That distinction cost ten
// blind minutes once.
func poll(out io.Writer, url, done string, tail *logTail) error {
	fmt.Fprintf(out, "waiting for %s\n", url)
	client := &http.Client{Timeout: 10 * time.Second}
	deadline := time.Now().Add(bootTimeout)
	for {
		res, err := client.Get(url)
		if err == nil {
			_ = res.Body.Close()
			if res.StatusCode == http.StatusOK {
				tail.print(out)
				fmt.Fprintf(out, "%s\n", done)
				return nil
			}
		}
		if time.Now().After(deadline) {
			tail.print(out)
			return fmt.Errorf("%s did not answer within %s", url, bootTimeout)
		}
		if !tail.print(out) {
			fmt.Fprint(out, ".")
		}
		time.Sleep(bootPoll)
	}
}

// logTail reports what is new in the host's provisioning log each time it is
// asked. The host uploads that log at milestones, so this is the closest
// thing to watching a boot on a box with no way in.
type logTail struct {
	env      []string
	evidence string
	seen     int
}

// print writes any lines the log has gained, and reports whether it wrote
// anything.
func (t *logTail) print(out io.Writer) bool {
	if t == nil || t.evidence == "" {
		return false
	}
	body, err := fetchProvisionLog(t.env, t.evidence)
	if err != nil || len(body) <= t.seen {
		return false
	}
	fresh := body[t.seen:]
	t.seen = len(body)
	// Only the milestone lines: the rest is apt and download chatter, and the
	// point here is to see how far it has got.
	wrote := false
	for _, line := range strings.Split(fresh, "\n") {
		if strings.HasPrefix(line, "===") || strings.HasPrefix(line, "provisioning ") {
			fmt.Fprintf(out, "\n%s", line)
			wrote = true
		}
	}
	if wrote {
		fmt.Fprintln(out)
	}
	return wrote
}

// tarballVersion asks S3 for the current version id of the content tarball.
// The key is fixed and the bucket versioned, so this id is the only thing
// that says which content a host booted from.
func tarballVersion(env []string, uri string) (string, error) {
	rest, ok := strings.CutPrefix(uri, "s3://")
	if !ok {
		return "", fmt.Errorf("tarball uri %q is not an s3:// uri", uri)
	}
	bucket, key, ok := strings.Cut(rest, "/")
	if !ok || key == "" {
		return "", fmt.Errorf("tarball uri %q names no key", uri)
	}
	cmd := exec.Command("aws", "s3api", "head-object",
		"--bucket", bucket, "--key", key, "--query", "VersionId", "--output", "text")
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("aws s3api head-object: %w", err)
	}
	v := strings.TrimSpace(string(out))
	if v == "None" {
		// What S3 reports on a bucket without versioning: nothing to record.
		v = ""
	}
	return v, nil
}

// fetchProvisionLog streams the log out of the bucket. Absent is not an
// error: the host has not uploaded anything yet.
func fetchProvisionLog(env []string, evidence string) (string, error) {
	cmd := exec.Command("aws", "s3", "cp", strings.TrimRight(evidence, "/")+"/provision.log", "-")
	cmd.Env = env
	out, err := cmd.Output()
	return string(out), err
}
