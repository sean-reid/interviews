package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sean-reid/interviews/internal/interview"
	"github.com/sean-reid/interviews/internal/provenance"
	"github.com/sean-reid/interviews/internal/version"
)

// A laptop's content is a checkout, and its commit is the only identity it
// has. A tree with uncommitted changes is not the commit it names, and two
// interviews run from it are not the same interview.
func TestContentVersionOfACheckout(t *testing.T) {
	_, root := contentClone(t)
	head, err := git(root, "rev-parse", "--short", "HEAD")
	if err != nil || head == "" {
		t.Fatalf("rev-parse: %q, %v", head, err)
	}
	if got := contentVersion(root); got != head {
		t.Errorf("contentVersion = %q, want the checkout's commit %q", got, head)
	}
	if err := os.WriteFile(filepath.Join(root, "scratch.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := contentVersion(root); got != head+"-dirty" {
		t.Errorf("contentVersion of a dirty tree = %q, want %q", got, head+"-dirty")
	}
}

// A provisioned host unpacks a tarball whose key never changes, so it cannot
// work out which content it has. Its own user-data tells it, and that beats
// anything a directory on the box might look like.
func TestContentVersionPrefersWhatTheHostWasTold(t *testing.T) {
	t.Setenv(provenance.ContentVersionEnv, "v-0123456789abcdef")
	if got := contentVersion(t.TempDir()); got != "v-0123456789abcdef" {
		t.Errorf("contentVersion = %q, want what the host was told", got)
	}
	// Told nothing, and no repository to ask: nothing to record, rather than
	// something that reads as a version.
	t.Setenv(provenance.ContentVersionEnv, "")
	if got := contentVersion(t.TempDir()); got != "" {
		t.Errorf("contentVersion of a plain directory = %q, want nothing", got)
	}
}

// Nothing on the box says it is not a laptop; the environment it was booted
// with does.
func TestOriginReadsTheMachineItIsOn(t *testing.T) {
	t.Setenv(provenance.ContentVersionEnv, "v-0123456789abcdef")
	t.Setenv(provenance.WhereEnv, string(provenance.Host))
	o := originFor(t.TempDir())
	if o.Where != provenance.Host || o.Content != "v-0123456789abcdef" {
		t.Errorf("origin on a host = %+v", o)
	}
	if o.Platform != version.Version {
		t.Errorf("platform = %q, want %q", o.Platform, version.Version)
	}
	t.Setenv(provenance.WhereEnv, "")
	if o := originFor(t.TempDir()); o.Where != provenance.Local {
		t.Errorf("origin with nothing set = %q, want %q", o.Where, provenance.Local)
	}
}

// repoFile resolves a path in the checkout this test runs from.
func repoFile(t *testing.T, parts ...string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(append([]string{"..", ".."}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// The chain that tells a host what it is: terraform takes the version the
// interviewer read, the template writes both facts into the session
// environment file, and the unit that builds the environment loads it. Every
// link is asserted because a rename on any one of them is silent: the
// evidence would simply stop saying where it came from.
func TestUserDataTellsTheHostWhatItIs(t *testing.T) {
	tpl := repoFile(t, "infra", "aws", "interview", "user-data.sh.tpl")
	for _, want := range []string{
		provenance.WhereEnv + "=" + string(provenance.Host),
		provenance.ContentVersionEnv + "=${content_version}",
	} {
		if !strings.Contains(tpl, want) {
			t.Errorf("user-data does not write %q:\n%s", want, tpl)
		}
	}
	if main := repoFile(t, "infra", "aws", "interview", "main.tf"); !strings.Contains(main, "content_version") {
		t.Error("the module never passes content_version to the template")
	}
	if vars := repoFile(t, "infra", "aws", "interview", "variables.tf"); !strings.Contains(vars, `variable "content_version"`) {
		t.Error("content_version is not a variable of the interview module")
	}
	unit := repoFile(t, "session", "host", "iv-session.service")
	if !strings.Contains(unit, "EnvironmentFile=/etc/interviews/session.env") {
		t.Error("the unit that builds the environment does not load the session environment")
	}
	if !strings.Contains(unit, "interviews env up") {
		t.Error("the unit no longer brings the environment up, so this chain proves nothing")
	}
}

// The version the interviewer read has to reach the host, since the host
// cannot read it back for itself. Stub binaries, so what is asserted is the
// command line startRemote really builds.
func TestStartRemoteHandsTheContentVersionToTheHost(t *testing.T) {
	t.Setenv(interview.HomeEnv, t.TempDir())
	stubTool(t, "aws", "v-0123456789abcdef")
	// Every terraform call answers with an empty object, which is a valid
	// but hostless set of outputs: startRemote then stops at the wait
	// instead of polling a box that does not exist.
	tf := stubTool(t, "terraform", "{}")
	if err := interview.SaveConfig(&interview.Config{AWS: &interview.AWSSetup{
		Region: "eu-west-1", Bucket: "bucket", TarballURI: "s3://bucket/tarballs/interviews.tar.gz",
	}}); err != nil {
		t.Fatal(err)
	}
	infra := t.TempDir()
	for _, dir := range []string{"account", "interview"} {
		if err := os.MkdirAll(filepath.Join(infra, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	var out, errOut strings.Builder
	code := startRemote("pipeline-meltdown", "calm-bison-0801", "",
		remoteOptions{TTLMinutes: 60, Infra: infra}, &out, &errOut)
	if code == 0 {
		t.Fatalf("a stubbed provision reported a live host:\n%s", out.String())
	}
	calls, err := os.ReadFile(tf)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(calls), "-var content_version=v-0123456789abcdef") {
		t.Errorf("the apply does not carry the content version:\n%s", calls)
	}
	rec, err := interview.Load("calm-bison-0801")
	if err != nil {
		t.Fatal(err)
	}
	if rec.ContentVersion != "v-0123456789abcdef" {
		t.Errorf("session records content version %q", rec.ContentVersion)
	}
}

// A take-home has no environment to record anything, and it is graded a week
// after it goes out. The session record is the only thing that can say what
// produced the drop.
func TestBundleRecordsWhatTheDropWasProducedOn(t *testing.T) {
	requireGit(t)
	t.Setenv(interview.HomeEnv, t.TempDir())
	t.Setenv(provenance.ContentVersionEnv, "")
	out := filepath.Join(t.TempDir(), "drop")
	code, _, stderr := run(t, "bundle", "--content", goodRoot, "slow-aligner",
		"--seed", "calm-bison-0801", "-o", out)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	rec, err := interview.Load("calm-bison-0801")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Provenance == nil {
		t.Fatal("the drop's session says nothing about what produced it")
	}
	if rec.Provenance.Platform != version.Version {
		t.Errorf("platform version = %q, want %q", rec.Provenance.Platform, version.Version)
	}
	if rec.Provenance.Where != provenance.Local {
		t.Errorf("produced on %q, want %q", rec.Provenance.Where, provenance.Local)
	}
	// No environment came up, so nothing may claim a node image or a
	// Kubernetes version for it.
	if rec.Provenance.NodeImage != "" || rec.Provenance.Kind != "" || rec.Provenance.Kubectl != "" {
		t.Errorf("an offline drop carries substrate versions: %+v", rec.Provenance)
	}
	// And sessions show has to print it, or the record is written for nobody.
	code, shown, stderr := run(t, "sessions", "show", "--seed", "calm-bison-0801")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	for _, want := range []string{"platform version", version.Version, "produced on", string(provenance.Local)} {
		if !strings.Contains(shown, want) {
			t.Errorf("sessions show does not report %q:\n%s", want, shown)
		}
	}
}
