package session

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
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
	InfoFile, CastFile, RawLogFile, KubeconfigFile, ScoreErrorFile,
}

// Evidence refreshes score.json from the live environment, bundles the
// workdir evidence set into evidence.tar.gz, and optionally uploads it.
// The bundle always ships; a refresh error surfaces afterwards unless
// this is the final pass, which only records it as score-error.txt.
func (m *Manager) Evidence(ctx context.Context, opts EvidenceOptions) error {
	var refreshErr error
	if st, err := debug.LoadState(m.Engine.Workdir); err == nil && len(st.Injected) > 0 {
		if _, err := RefreshScore(ctx, m.Engine); err != nil {
			refreshErr = err
			if werr := os.WriteFile(filepath.Join(m.Engine.Workdir, ScoreErrorFile),
				[]byte(err.Error()+"\n"), 0o644); werr != nil {
				return werr
			}
		}
	}

	tarPath := filepath.Join(m.Engine.Workdir, EvidenceFile)
	if err := bundle(m.Engine.Workdir, tarPath); err != nil {
		return err
	}
	if opts.S3 != "" {
		dest := strings.TrimRight(opts.S3, "/") + "/" + EvidenceFile
		if err := m.Engine.Runner.Command(ctx, "aws", "s3", "cp", tarPath, dest); err != nil {
			return err
		}
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
			ID: s.ID, Title: s.Title, Tier: string(s.Tier), Fixed: s.Fixed,
		})
		score.Total++
		if s.Fixed {
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
	// The archive carries the session tokens and the candidate service
	// account token, so it must not be readable beyond its owner.
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
	return nil
}

func addFile(tw *tar.Writer, workdir, name string) (err error) {
	path := filepath.Join(workdir, name)
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	hdr, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	hdr.Name = name
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()
	if _, err := io.Copy(tw, f); err != nil {
		return fmt.Errorf("bundle %s: %w", name, err)
	}
	return nil
}
