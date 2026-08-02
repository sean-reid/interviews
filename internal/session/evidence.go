package session

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sean-reid/interviews/internal/debug"
	"github.com/sean-reid/interviews/internal/grading"
)

// EvidenceOptions tune one evidence pass. Final marks the last bundle of
// a session: it must never abort because a check errored, so refresh
// failures are recorded in the bundle instead of returned.
type EvidenceOptions struct {
	Final bool
	S3    string
}

// evidenceFiles is the workdir evidence set: bundling includes what
// exists and skips what does not.
var evidenceFiles = []string{
	debug.StateFile, grading.ScoreFile, grading.HintsFile, TimelineFile,
	InfoFile, CastFile, RawLogFile, ScoreErrorFile,
}

// candidateFileLimit caps one file from the candidate's directory. The
// bundle is rebuilt every two minutes for the whole interview, so one
// redirected log would be re-read and re-gzipped thirty times. Anything over
// the cap is named in skipped.txt instead of being dropped quietly, because
// a bundle that looks complete and is not is worse than one that says what
// it left out.
const candidateFileLimit = 4 << 20

// addCandidateWork puts the candidate's own directory in the bundle. It is
// the work being assessed, and a flat list of engine files could never
// include it: teardown reported it as evidence worth keeping while the
// artifact that leaves the machine did not carry it.
func addCandidateWork(tw *tar.Writer, workdir string) error {
	root := filepath.Join(workdir, CandidateDir)
	if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	var skipped []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(workdir, path)
		if err != nil {
			return err
		}
		// Only regular files: a symlink here could name anything on the host,
		// including the answer keys the candidate cannot otherwise read.
		if !info.Mode().IsRegular() {
			skipped = append(skipped, fmt.Sprintf("%s (not a regular file)", rel))
			return nil
		}
		if info.Size() > candidateFileLimit {
			skipped = append(skipped, fmt.Sprintf("%s (%d bytes, over the %d cap)", rel, info.Size(), candidateFileLimit))
			return nil
		}
		return addFile(tw, workdir, rel)
	})
	if err != nil {
		return err
	}
	if len(skipped) == 0 {
		return nil
	}
	return addBytes(tw, "skipped.txt", []byte(strings.Join(skipped, "\n")+"\n"))
}

// addBytes writes content the workdir does not hold as a file.
func addBytes(tw *tar.Writer, name string, raw []byte) error {
	hdr := &tar.Header{Name: name, Mode: 0o600, Size: int64(len(raw)), ModTime: time.Now()}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err := tw.Write(raw)
	return err
}

// redactedInfo returns what session.json should look like inside the
// bundle. The bundle is the artifact that leaves the machine and sits in
// a bucket; nothing grading needs is a credential.
func redactedInfo(raw []byte) ([]byte, error) {
	var info Info
	if err := json.Unmarshal(raw, &info); err != nil {
		return nil, err
	}
	// The tokens are the whole of the URL authentication, and the URLs
	// contain them. All three, the app route included: this bundle syncs to
	// the bucket every two minutes while the interview is still running, so
	// a token inside it is live rather than historical.
	info.CandidateToken, info.ObserverToken, info.AppToken = "", "", ""
	info.CandidateURL, info.ObserverURL, info.AppURL = "", "", ""
	return json.MarshalIndent(info, "", "  ")
}

// Evidence refreshes score.json from the live environment, bundles the
// workdir evidence set into evidence.tar.gz, and optionally uploads it.
// The bundle always ships; a refresh error surfaces afterwards unless
// this is the final pass, which only records it as score-error.txt.
func (m *Manager) Evidence(ctx context.Context, opts EvidenceOptions) error {
	var refreshErr error
	if st, err := debug.LoadState(m.Engine.Workdir); err == nil && st.Live() && len(st.Injected) > 0 {
		// The checks run real commands against the environment and print as
		// they go, so say what that output belongs to. Unlabelled, a check
		// that timed out reads as this command failing.
		fmt.Fprintf(m.Out, "reading the checks to refresh %s:\n", grading.ScoreFile)
		score, err := RefreshScore(ctx, m.Engine)
		switch {
		case err != nil:
			refreshErr = err
			fmt.Fprintf(m.Out, "the score could not be refreshed, so the bundle carries the last one: %v\n", err)
			if werr := os.WriteFile(filepath.Join(m.Engine.Workdir, ScoreErrorFile),
				[]byte(err.Error()+"\n"), 0o644); werr != nil {
				return werr
			}
		default:
			verified := "failed"
			if score.Verified {
				verified = "passed"
			}
			fmt.Fprintf(m.Out, "score: %d/%d faults fixed, end-to-end verify %s\n",
				score.Fixed, score.Total, verified)
		}
	}

	tarPath := filepath.Join(m.Engine.Workdir, EvidenceFile)
	if err := bundle(m.Engine.Workdir, tarPath); err != nil {
		return err
	}
	// Nothing else says where the evidence is, and a bundle nobody can find
	// is a bundle nobody copies off the machine before teardown.
	fmt.Fprintf(m.Out, "evidence: %s\n", tarPath)
	if opts.S3 != "" {
		dest := strings.TrimRight(opts.S3, "/") + "/" + EvidenceFile
		if err := m.Engine.Runner.Command(ctx, "aws", "s3", "cp", tarPath, dest); err != nil {
			return err
		}
		fmt.Fprintf(m.Out, "uploaded: %s\n", dest)
	}
	if opts.Final {
		return nil
	}
	return refreshErr
}

// RefreshScore recomputes the objective score from the live environment
// and writes it into the workdir; grade score and evidence bundling
// share this.
func RefreshScore(ctx context.Context, e *debug.Engine) (*grading.Score, error) {
	statuses, err := e.Status(ctx)
	if err != nil {
		return nil, err
	}
	pack, err := e.Pack()
	if err != nil {
		return nil, err
	}
	score := &grading.Score{
		Problem: e.Variant.Problem, InterviewID: e.Variant.InterviewID,
		Pack: pack, Verified: e.Verify(ctx) == nil, At: time.Now(),
	}
	for _, s := range statuses {
		score.Faults = append(score.Faults, grading.FaultResult{
			ID: s.ID, Title: s.Title, Tier: string(s.Tier), Fixed: s.Fixed(),
			CheckFailed: s.State == debug.CheckCannotRun,
		})
		score.Total++
		if s.Fixed() {
			score.Fixed++
		}
	}
	if err := grading.WriteScore(e.Workdir, score); err != nil {
		return nil, err
	}
	return score, nil
}

// bundle writes the existing evidence files into a tar.gz at dest.
func bundle(workdir, dest string) (err error) {
	// The archive carries a recording of the candidate's terminal, so it
	// must not be readable beyond its owner.
	// Build beside the target and swap it in, so a pass that fails partway
	// leaves the last good bundle intact. The sync timer reruns every two
	// minutes, and a truncated archive is worse than a stale one.
	tmp := dest + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	defer func() {
		for _, c := range []io.Closer{tw, gz, f} {
			if cerr := c.Close(); err == nil {
				err = cerr
			}
		}
		if err == nil {
			err = os.Rename(tmp, dest)
		}
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()
	for _, name := range evidenceFiles {
		if aerr := addFile(tw, workdir, name); aerr != nil {
			return aerr
		}
	}
	return addCandidateWork(tw, workdir)
}

// addFile streams one workdir file into the archive. session.json is the
// one exception read whole: its redacted copy has a different length than
// the header would claim, and it is a few hundred bytes. The cast and the
// raw log are not small: they grow for the length of the interview, and
// reading them whole once per sync pass is how a chatty terminal could
// OOM the host mid-interview.
func addFile(tw *tar.Writer, workdir, name string) error {
	path := filepath.Join(workdir, name)
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	// Opened read-only, so the close cannot lose writes.
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	hdr, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	hdr.Name = name
	if name == InfoFile {
		raw, err := io.ReadAll(f)
		if err != nil {
			return err
		}
		if raw, err = redactedInfo(raw); err != nil {
			return fmt.Errorf("bundle %s: %w", name, err)
		}
		hdr.Size = int64(len(raw))
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := tw.Write(raw); err != nil {
			return fmt.Errorf("bundle %s: %w", name, err)
		}
		return nil
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	// The session writes these by appending, so the size at open time is a
	// floor and the limit keeps the archive valid when a file grows
	// mid-copy. A file that shrank instead comes up short, which fails the
	// pass and leaves the last good bundle in place.
	if _, err := io.Copy(tw, io.LimitReader(f, hdr.Size)); err != nil {
		return fmt.Errorf("bundle %s: %w", name, err)
	}
	return nil
}
