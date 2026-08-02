package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sean-reid/interviews/internal/interview"
)

// The reserved seed names live in the interview package and the keys that
// make them dangerous are built here. Change a prefix on this side without
// reserving it and a seed could point an interview at shared data again, so
// assert the keys really do start where the reservation says.
func TestBucketKeysStartWithTheReservedPrefixes(t *testing.T) {
	if got := StateKey("calm-bison-0801"); !strings.HasPrefix(got, interview.StatePrefix+"/") {
		t.Errorf("state key %q does not start with the reserved %q", got, interview.StatePrefix)
	}
	// Positive control: the prefix is not something every string starts with.
	if strings.HasPrefix("calm-bison-0801/terraform.tfstate", interview.StatePrefix+"/") {
		t.Fatal("the prefix check matches a key without it, so the assertion above proves nothing")
	}
	if err := interview.ValidSeed(interview.StatePrefix); err == nil {
		t.Errorf("%q builds the state key and is still an allowed seed", interview.StatePrefix)
	}
	if err := interview.ValidSeed(interview.TarballPrefix); err == nil {
		t.Errorf("%q holds the shared tarball and is still an allowed seed", interview.TarballPrefix)
	}
}

// stubTool puts a fake binary on PATH that logs its argv and prints out.
func stubTool(t *testing.T, name, out string) string {
	t.Helper()
	bin := t.TempDir()
	log := filepath.Join(bin, name+".calls")
	script := "#!/bin/sh\necho \"$@\" >> " + log + "\n"
	if out != "" {
		script += "printf '%s\\n' " + "'" + out + "'\n"
	}
	if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

// The tarball key never changes, so the S3 version id is the only thing
// that says which content a host ran. head-object reports the current one.
func TestTarballVersionAsksS3(t *testing.T) {
	log := stubTool(t, "aws", "v-0123456789abcdef")
	v, err := tarballVersion(nil, "s3://bucket/tarballs/interviews.tar.gz")
	if err != nil || v != "v-0123456789abcdef" {
		t.Fatalf("tarballVersion = %q, %v", v, err)
	}
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"s3api head-object", "--bucket bucket", "--key tarballs/interviews.tar.gz"} {
		if !strings.Contains(string(calls), want) {
			t.Errorf("aws call missing %q: %s", want, calls)
		}
	}

	if _, err := tarballVersion(nil, "https://bucket/key"); err == nil {
		t.Error("a non-s3 uri was accepted")
	}
}

// An unversioned bucket answers "None", which is nothing worth recording.
func TestTarballVersionOnAnUnversionedBucket(t *testing.T) {
	stubTool(t, "aws", "None")
	v, err := tarballVersion(nil, "s3://bucket/tarballs/interviews.tar.gz")
	if err != nil || v != "" {
		t.Fatalf("tarballVersion = %q, %v; want empty for None", v, err)
	}
}

// end leaves nothing per seed behind: each data dir carries its own full
// copy of the AWS provider (3.2 GB across five seeds), and init must not
// rewrite the module directory's tracked lockfile while it is at it.
func TestEndRemoteCleansTheDataDirAndLeavesTheLockfileAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv(interview.HomeEnv, home)
	log := stubTool(t, "terraform", "")
	if err := interview.SaveConfig(&interview.Config{AWS: &interview.AWSSetup{
		Region: "eu-west-1", Bucket: "bucket", TarballURI: "s3://bucket/tarballs/interviews.tar.gz",
	}}); err != nil {
		t.Fatal(err)
	}
	rec := &interview.Session{Seed: "calm-bison-0801", Problem: "relay", Mode: interview.AWS,
		CreatedAt: time.Now(), TerraformDir: t.TempDir()}
	if err := interview.Save(rec); err != nil {
		t.Fatal(err)
	}

	var out, errOut strings.Builder
	if code := endRemote(rec, false, &out, &errOut); code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, out.String(), errOut.String())
	}
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(calls), "-lockfile=readonly") {
		t.Errorf("init may rewrite the tracked lockfile:\n%s", calls)
	}
	if _, err := os.Stat(filepath.Join(home, "terraform", rec.Seed)); !os.IsNotExist(err) {
		t.Error("the per-seed terraform data dir survived the destroy")
	}
	if after, err := interview.Load(rec.Seed); err != nil || after.EndedAt.IsZero() {
		t.Errorf("session not marked ended: %+v, %v", after, err)
	}
}
