package cli

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sean-reid/interviews/internal/interview"
)

// TarballName is what the host downloads and unpacks.
const TarballName = "interviews.tar.gz"

func cmdSetup(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		return usageErr("setup", stderr)
	}
	if args[0] != "aws" {
		fmt.Fprintf(stderr, "interviews setup: no target %q\n", args[0])
		return usageErr("setup", stderr)
	}
	return setupAWS(args[1:], stdout, stderr)
}

// setupAWS does the one-time cloud preparation: the evidence bucket, and the
// bundle a host downloads. It replaces a page of terraform and aws
// invocations from the runbook, and it is the only place that knows both the
// platform tree and the content tree, which now live in separate
// repositories.
func setupAWS(args []string, stdout, stderr io.Writer) int {
	fs_, contentRoot := newFlagSet("setup aws", stderr)
	region := fs_.String("region", "", "AWS region to provision in")
	bucket := fs_.String("bucket", "", "name for the evidence bucket")
	profile := fs_.String("profile", "", "AWS named profile (default: the usual credential chain)")
	infra := fs_.String("infra", "", "path to the terraform modules (default: found from here)")
	skipBucket := fs_.Bool("skip-bucket", false, "leave the bucket alone and only refresh the tarball")
	pos, err := parsePermuted(fs_, args)
	if err != nil {
		return 2
	}
	if len(pos) != 0 || *region == "" || *bucket == "" {
		return usageErr("setup aws", stderr)
	}

	root, err := infraRoot(*infra)
	if err != nil {
		fmt.Fprintf(stderr, "interviews setup aws: %v\n", err)
		return 1
	}
	// Checked before anything is built or created: the bundle is useless
	// without content, and finding that out after a linux build and a bucket
	// is a worse way to learn the root is unset.
	if _, err := openRegistry(*contentRoot, false, io.Discard); err != nil {
		fmt.Fprintf(stderr, "interviews setup aws: %v\n", err)
		fmt.Fprintf(stderr, "  interviews config set content <path to the problems checkout>/content\n")
		return 1
	}
	// Interviewing against last month's problems starts here, not at start:
	// whatever is packed now is what every host runs until this is rerun.
	warnStale(*contentRoot, stderr)

	env := os.Environ()
	if *profile != "" {
		env = append(env, "AWS_PROFILE="+*profile)
	}
	who, err := callerIdentity(env)
	if err != nil {
		fmt.Fprintf(stderr, "interviews setup aws: %v\n", err)
		return 1
	}
	// Printed rather than assumed: a profile that shadows another one puts
	// this in the wrong account, and the bucket is the first thing created.
	fmt.Fprintf(stdout, "account: %s\nregion:  %s\nbucket:  %s\n\n", who, *region, *bucket)

	if !*skipBucket {
		acct := filepath.Join(root, "account")
		if err := runIn(stdout, stderr, env, acct, "terraform", "init", "-input=false"); err != nil {
			fmt.Fprintf(stderr, "interviews setup aws: %v\n", err)
			return 1
		}
		// Bounded, because the interesting failure here does not fail: if the
		// policy names a different bucket, CreateBucket succeeds and the
		// read-back is denied, and the provider retries that for a quarter of
		// an hour before giving up.
		if err := runBounded(stdout, stderr, env, acct, bucketTimeout,
			"terraform", "apply", "-auto-approve", "-input=false",
			"-var", "region="+*region, "-var", "bucket="+*bucket); err != nil {
			fmt.Fprintf(stderr, "interviews setup aws: %v\n", err)
			fmt.Fprintf(stderr, "\nif that stalled rather than failed, the policy on this identity probably names\n"+
				"a different bucket than --bucket %s. The bucket may exist now and be unmanageable:\n"+
				"  aws iam put-user-policy ...   with BUCKET set to %s\n"+
				"  then rerun with --skip-bucket if it was already created\n", *bucket, *bucket)
			return 1
		}
	}

	tarball := filepath.Join(os.TempDir(), TarballName)
	digest, err := packBundle(stdout, root, *contentRoot, tarball)
	if err != nil {
		fmt.Fprintf(stderr, "interviews setup aws: %v\n", err)
		return 1
	}
	uri := fmt.Sprintf("s3://%s/tarballs/%s", *bucket, TarballName)
	prev := interview.LoadConfig().AWS
	if prev != nil && prev.ContentDigest == digest && prev.TarballURI == uri {
		fmt.Fprintf(stdout, "bundle unchanged since %s, not re-uploading\n",
			prev.UploadedAt.Format(time.RFC1123))
	} else {
		if err := runIn(stdout, stderr, env, "", "aws", "s3", "cp", tarball, uri, "--region", *region); err != nil {
			fmt.Fprintf(stderr, "interviews setup aws: %v\n", err)
			return 1
		}
	}

	c := interview.LoadConfig()
	c.AWS = &interview.AWSSetup{
		Region: *region, Bucket: *bucket, TarballURI: uri, Profile: *profile,
		ContentDigest: digest, UploadedAt: time.Now(),
	}
	if err := interview.SaveConfig(c); err != nil {
		fmt.Fprintf(stderr, "interviews setup aws: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "\nready. interviews start <problem> --remote needs nothing else.\n")
	return 0
}

// infraRoot finds the terraform modules. They ship in the repository rather
// than in the binary, so this needs a checkout: walking up from here covers
// running the command from anywhere inside one.
func infraRoot(explicit string) (string, error) {
	if explicit != "" {
		if _, err := os.Stat(filepath.Join(explicit, "account")); err != nil {
			return "", fmt.Errorf("--infra %s: %w", explicit, err)
		}
		return explicit, nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(dir, "infra", "aws")
		if _, err := os.Stat(filepath.Join(candidate, "account")); err == nil {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no infra/aws above %s: run this from a checkout of the platform, or pass --infra", must(os.Getwd()))
		}
		dir = parent
	}
}

func must(s string, _ error) string { return s }

// callerIdentity reports who the AWS calls will be made as.
func callerIdentity(env []string) (string, error) {
	cmd := exec.Command("aws", "sts", "get-caller-identity", "--query", "Arn", "--output", "text")
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("aws sts get-caller-identity failed; check the profile and its credentials: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// bucketTimeout caps the bucket apply. Long enough for a slow region, short
// enough that a permission problem is reported rather than waited on.
const bucketTimeout = 90 * time.Second

// runBounded is runIn with a deadline, so a provider retry loop cannot hold
// the command open indefinitely.
func runBounded(stdout, stderr io.Writer, env []string, dir string, limit time.Duration, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = dir, env, stdout, stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return fmt.Errorf("%s %s gave up after %s", name, args[0], limit)
	}
	if err != nil {
		return fmt.Errorf("%s %s: %w", name, args[0], err)
	}
	return nil
}

func runIn(stdout, stderr io.Writer, env []string, dir string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = dir, env, stdout, stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, args[0], err)
	}
	return nil
}

// packBundle writes the tarball a host unpacks: the linux binary at the
// root, the session host scripts, and the content tree. Returns a digest of
// what went in, so an unchanged bundle is not uploaded twice.
func packBundle(out io.Writer, infra, contentRoot, dest string) (string, error) {
	platform := filepath.Dir(infra) // <repo>/infra/aws -> <repo>/infra
	platform = filepath.Dir(platform)
	fmt.Fprintf(out, "building the linux binary\n")
	bin := filepath.Join(os.TempDir(), "interviews-linux")
	build := exec.Command("go", "build", "-trimpath", "-o", bin, "./cmd/interviews")
	build.Dir = platform
	build.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
	build.Stdout, build.Stderr = out, out
	if err := build.Run(); err != nil {
		return "", fmt.Errorf("building for linux: %w", err)
	}

	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	sum := sha256.New()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	add := func(src, name string, mode int64) error {
		body, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		sum.Write([]byte(name))
		sum.Write(body)
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(body))}); err != nil {
			return err
		}
		_, err = tw.Write(body)
		return err
	}
	if err := add(bin, "interviews", 0o755); err != nil {
		return "", err
	}
	for _, tree := range []struct{ dir, prefix string }{
		{filepath.Join(platform, "session", "host"), "session/host"},
		{contentRoot, "content"},
	} {
		err := filepath.WalkDir(tree.dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, err := filepath.Rel(tree.dir, p)
			if err != nil {
				return err
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			mode := int64(0o644)
			if info.Mode()&0o111 != 0 {
				mode = 0o755
			}
			return add(p, filepath.ToSlash(filepath.Join(tree.prefix, rel)), mode)
		})
		if err != nil {
			return "", fmt.Errorf("packing %s: %w", tree.dir, err)
		}
	}
	for _, c := range []io.Closer{tw, gz, f} {
		if err := c.Close(); err != nil {
			return "", err
		}
	}
	info, err := os.Stat(dest)
	if err != nil {
		return "", err
	}
	digest := hex.EncodeToString(sum.Sum(nil))[:16]
	fmt.Fprintf(out, "packed %s (%d KiB, content digest %s)\n", dest, info.Size()/1024, digest)
	return digest, nil
}
