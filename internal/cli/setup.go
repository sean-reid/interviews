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
	if len(args) == 0 {
		return usageErr("setup", stderr)
	}
	if args[0] == "--help" || args[0] == "-h" {
		printUsage("setup", stderr)
		return 0
	}
	switch args[0] {
	case "aws":
		return setupAWS(args[1:], stdout, stderr)
	case "pack":
		return setupPack(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "interviews setup: no target %q\n", args[0])
		return usageErr("setup", stderr)
	}
}

// setupPack builds the host bundle to a file and stops before AWS. It is
// how CI provisions its container from the same tarball a real host
// unpacks: a hand-rolled test tarball proves a layout nothing ships.
func setupPack(args []string, stdout, stderr io.Writer) int {
	fs_, contentRoot := newFlagSet("setup pack", stderr)
	dest := fs_.String("o", "", "where to write the tarball")
	infra := fs_.String("infra", "", "path to the terraform modules (default: found from the working directory)")
	pos, err := parsePermuted(fs_, args)
	if err != nil {
		return parseExit(err)
	}
	if len(pos) != 0 || *dest == "" {
		return usageErr("setup pack", stderr)
	}
	root, err := infraRoot(*infra)
	if err != nil {
		fmt.Fprintf(stderr, "interviews setup pack: %v\n", err)
		return 1
	}
	if _, err := packBundle(stdout, root, *contentRoot, *dest); err != nil {
		fmt.Fprintf(stderr, "interviews setup pack: %v\n", err)
		return 1
	}
	return 0
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
		return parseExit(err)
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
		if err := runBounded(stdout, stderr, env, acct, initTimeout, "terraform", "init", "-input=false"); err != nil {
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
	uri := fmt.Sprintf("s3://%s/%s/%s", *bucket, interview.TarballPrefix, TarballName)
	prev := interview.LoadConfig().AWS
	if prev != nil && prev.ContentDigest == digest && prev.TarballURI == uri {
		fmt.Fprintf(stdout, "bundle unchanged since %s, not re-uploading\n",
			prev.UploadedAt.Format(time.RFC1123))
	} else {
		if err := runBounded(stdout, stderr, env, "", syncTimeout, "aws", "s3", "cp", tarball, uri, "--region", *region); err != nil {
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
	start := dir
	for {
		candidate := filepath.Join(dir, "infra", "aws")
		if _, err := os.Stat(filepath.Join(candidate, "account")); err == nil {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no infra/aws above %s: run this from a checkout of the platform, or pass --infra", start)
		}
		dir = parent
	}
}

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

// unlockGrace is how long a bounded command gets after its interrupt to shut
// down cleanly. Terraform uses it to release the state lock; anything still
// running when it elapses is killed by the standard library.
const unlockGrace = 30 * time.Second

// syncTimeout caps pulling an interview's evidence out of the bucket. It runs
// before the destroy in end, so hanging here leaves the host up.
const syncTimeout = 5 * time.Minute

// initTimeout caps a terraform init. It reaches the network for providers and
// the backend, and start --remote runs it with a candidate waiting.
const initTimeout = 3 * time.Minute

// runBounded runs a command with a deadline, so a provider retry loop or an
// unreachable endpoint cannot hold it open indefinitely. Everything here
// reaches the network, so nothing runs unbounded.
// boundedCmd builds the command runBounded runs.
//
// Ask, then kill. Terraform releases its state lock on an interrupt and not
// on a kill, and a kill is what exec.CommandContext does by default: a
// destroy that ran past its bound left a lock in the bucket that blocked
// every later end, from any machine, while the host kept billing. WaitDelay
// is the backstop for a process that ignores the signal, so the bound still
// means something.
func boundedCmd(ctx context.Context, stdout, stderr io.Writer, env []string, dir, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = dir, env, stdout, stderr
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = unlockGrace
	return cmd
}

func runBounded(stdout, stderr io.Writer, env []string, dir string, limit time.Duration, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	cmd := boundedCmd(ctx, stdout, stderr, env, dir, name, args...)
	err := cmd.Run()
	// args can be empty, so name the command without indexing into it.
	what := strings.TrimSpace(name + " " + firstArg(args))
	if ctx.Err() != nil {
		return fmt.Errorf("%s gave up after %s", what, limit)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	return nil
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
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
	pack := func() error {
		if err := add(bin, "interviews", 0o755); err != nil {
			return err
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
				return fmt.Errorf("packing %s: %w", tree.dir, err)
			}
		}
		return nil
	}
	// The chain closes on every path: a pack failure used to return with
	// the tar, gzip and file writers all still open.
	err = pack()
	for _, c := range []io.Closer{tw, gz, f} {
		if cerr := c.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}
	if err != nil {
		return "", err
	}
	info, err := os.Stat(dest)
	if err != nil {
		return "", err
	}
	digest := hex.EncodeToString(sum.Sum(nil))[:16]
	fmt.Fprintf(out, "packed %s (%d KiB, content digest %s)\n", dest, info.Size()/1024, digest)
	return digest, nil
}
