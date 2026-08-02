package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sean-reid/interviews/internal/interview"
)

// Nothing else lists what the platform shells out to, so the first time an
// interviewer learns ttyd is needed is otherwise at session start.
func TestDoctorNamesEveryToolAndItsPurpose(t *testing.T) {
	code, stdout, stderr := run(t, "doctor")
	if code != 0 && code != 1 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	for _, want := range []string{
		"docker", "kind", "kubectl", "tmux", "ttyd", "asciinema", "terraform", "aws",
		"debugging environments", "live sessions", "provisioned hosts (optional)",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("doctor says nothing about %q:\n%s", want, stdout)
		}
	}
	// Missing tools have to come with the way to get them.
	if strings.Contains(stdout, "missing") && !strings.Contains(stdout, "install") {
		t.Errorf("doctor reports something missing without saying how to install it:\n%s", stdout)
	}
}

func TestDoctorRejectsArguments(t *testing.T) {
	if code, _, stderr := run(t, "doctor", "everything"); code != 2 ||
		!strings.Contains(stderr, "usage: interviews doctor") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
}

// doctor is the pre-interview sanity check, so a machine that cannot load
// its problems has to fail it, and say so where failures go.
func TestDoctorFailsOnABrokenContentRoot(t *testing.T) {
	t.Setenv(interview.ContentEnv, filepath.Join(t.TempDir(), "nowhere"))
	code, _, stderr := run(t, "doctor")
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if !strings.Contains(stderr, "no problems there") {
		t.Errorf("stderr says nothing about the content root: %q", stderr)
	}
}

// stubAWS puts a fake aws CLI first on PATH, so everything below the exec
// boundary runs for real.
func stubAWS(t *testing.T, script string) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "aws"), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// awsMachine gives the test a registry of its own with an AWS setup in it,
// which is what makes doctor check the account.
func awsMachine(t *testing.T) {
	t.Helper()
	t.Setenv(interview.HomeEnv, t.TempDir())
	err := interview.SaveConfig(&interview.Config{AWS: &interview.AWSSetup{
		Region: "eu-west-1", Bucket: "evidence", TarballURI: "s3://evidence/tarball.tgz",
	}})
	if err != nil {
		t.Fatal(err)
	}
}

// A machine with no cloud setup has no account to check, so doctor says
// nothing about one and never runs aws.
func TestDoctorWithoutAWSSetupSaysNothingAndAsksNothing(t *testing.T) {
	t.Setenv(interview.HomeEnv, t.TempDir())
	marker := filepath.Join(t.TempDir(), "aws-was-run")
	t.Setenv("AWS_STUB_MARKER", marker)
	stubAWS(t, `touch "$AWS_STUB_MARKER"; echo '{}'`)
	code, stdout, stderr := run(t, "doctor")
	if code != 0 && code != 1 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	for _, word := range []string{"stray", "account"} {
		if strings.Contains(stdout, word) {
			t.Errorf("doctor mentions %q with no aws setup:\n%s", word, stdout)
		}
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("doctor ran aws on a machine with no aws setup")
	}
}

// A stray host costs money until someone acts, so it is reported loudly with
// the commands that deal with it. It does not stop this machine running an
// interview, so it must not change doctor's exit code.
func TestDoctorReportsAHostNoRecordKnows(t *testing.T) {
	t.Setenv(interview.HomeEnv, t.TempDir())
	stubAWS(t, `case "$*" in
*describe-instances*) cat testdata/describe-instances.json ;;
*describe-addresses*) echo '{"Addresses":[{"PublicIp":"203.0.113.9"}]}' ;;
esac`)
	baseline, _, _ := run(t, "doctor")

	awsMachine(t)
	code, stdout, stderr := run(t, "doctor")
	if code != baseline {
		t.Errorf("exit %d with a stray host, %d without: a stray must not change the exit code (stderr %q)", code, baseline, stderr)
	}
	for _, want := range []string{
		"quiet-badger-0801", "no record on this machine",
		"1 elastic ip attached to nothing",
		"sessions --remote", "interviews end",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("doctor says nothing about %q:\n%s", want, stdout)
		}
	}
}

// end reported this host destroyed and the account still runs it, which is
// the disagreement that bills unnoticed.
func TestDoctorReportsAHostStillUpAfterEnd(t *testing.T) {
	t.Setenv(interview.HomeEnv, t.TempDir())
	record(t, &interview.Session{Seed: "quiet-badger-0801", Problem: "relay",
		Mode: interview.AWS, EndedAt: time.Now().Add(-time.Hour)})
	if err := interview.SaveConfig(&interview.Config{AWS: &interview.AWSSetup{
		Region: "eu-west-1", Bucket: "evidence", TarballURI: "s3://evidence/tarball.tgz",
	}}); err != nil {
		t.Fatal(err)
	}
	stubAWS(t, `case "$*" in
*describe-instances*) cat testdata/describe-instances.json ;;
*) echo '{}' ;;
esac`)
	_, stdout, _ := run(t, "doctor")
	if !strings.Contains(stdout, "still up after end") {
		t.Errorf("doctor does not say the ended host is still up:\n%s", stdout)
	}
}

// A clean account says so in one line, so a clean result is distinguishable
// from the check not running.
func TestDoctorSaysSoWhenNothingIsStray(t *testing.T) {
	awsMachine(t)
	stubAWS(t, `echo '{}'`)
	_, stdout, _ := run(t, "doctor")
	if !strings.Contains(stdout, "nothing stray") {
		t.Errorf("doctor does not say the account is clean:\n%s", stdout)
	}
}

// An account that cannot be asked is reported as exactly that, and the rest
// of the report still prints: doctor stays useful with no network.
func TestDoctorStillReportsWhenTheAccountCannotBeAsked(t *testing.T) {
	awsMachine(t)
	stubAWS(t, `echo "ExpiredToken" >&2; exit 255`)
	code, stdout, stderr := run(t, "doctor")
	if code != 0 && code != 1 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	for _, want := range []string{
		"could not check the account for stray hosts",
		"could not check the account for stranded elastic ips",
		"ExpiredToken",
		"docker", // the rest of the report still printed
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("doctor says nothing about %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "nothing stray") {
		t.Errorf("doctor calls the account clean when it could not ask:\n%s", stdout)
	}
}
