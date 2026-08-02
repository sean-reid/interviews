// Package interview keeps the per-machine record of interview sessions:
// what exists, what it was, and which one the commands mean by default.
//
// The registry is an index, never a source of truth. A live environment is
// described by its workdir, and cloud resources by their terraform state,
// so a registry entry that is lost or stale degrades to typing --seed
// rather than breaking a session in progress.
package interview

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/sean-reid/interviews/internal/fileio"
	"github.com/sean-reid/interviews/internal/taxonomy"
)

// Mode is where a session runs.
type Mode string

// Where a session runs. Offline is a take-home or a design exercise: there
// is no environment, so the record is the only thing that knows the session
// exists.
const (
	Local   Mode = "local"
	AWS     Mode = "aws"
	Offline Mode = "offline"
)

// Stage is how far an offline interview has got. A debugging session's
// state is derived from its workdir, but whether a take-home was sent, came
// back, or has been reviewed is known only to the interviewer, so for these
// the record is the source of truth rather than an index.
type Stage string

// The stages an offline interview moves through.
const (
	Created  Stage = "created"
	Sent     Stage = "sent"
	Returned Stage = "returned"
	Reviewed Stage = "reviewed"
)

// Session is one interview. Everything an interviewer might have to ask
// for later lives here, the URLs above all: they are printed once, at
// start, and there is nowhere else to read them from.
type Session struct {
	Seed    string        `json:"seed"`
	Problem string        `json:"problem"`
	Type    taxonomy.Type `json:"type"`
	// Level is who the session is calibrated for; the sheet prints its band.
	Level taxonomy.Level `json:"level,omitempty"`
	Mode  Mode           `json:"mode"`

	CreatedAt time.Time `json:"created_at"`
	// EndedAt is when the interviewer ran end. Zero means it never happened,
	// which is not the same as still running: status is derived, not stored.
	EndedAt time.Time `json:"ended_at,omitzero"`
	// TTLMinutes is the host's self-destruct clock, 0 when there is none.
	TTLMinutes int `json:"ttl_minutes,omitempty"`

	Workdir      string `json:"workdir"`
	ContentRoot  string `json:"content_root,omitempty"`
	TerraformDir string `json:"terraform_dir,omitempty"`
	// Evidence is where the bundle lands: a directory locally, an s3 URI
	// for a provisioned host.
	Evidence string `json:"evidence,omitempty"`

	// Stage, and the paths either side of it, are the offline types: the
	// bundle that went out and the submission that came back.
	Stage          Stage     `json:"stage,omitempty"`
	BundlePath     string    `json:"bundle_path,omitempty"`
	SubmissionPath string    `json:"submission_path,omitempty"`
	DueAt          time.Time `json:"due_at,omitzero"`
	ReviewedAt     time.Time `json:"reviewed_at,omitzero"`

	CandidateURL string `json:"candidate_url,omitempty"`
	ObserverURL  string `json:"observer_url,omitempty"`
	AppURL       string `json:"app_url,omitempty"`
	Host         string `json:"host,omitempty"`
}

// HomeEnv overrides the registry location, for tests and for running two
// unrelated sets of sessions from one account.
const HomeEnv = "INTERVIEWS_HOME"

// currentFile names the session the commands mean when nobody says.
const currentFile = "current"

// Home is the registry root.
func Home() (string, error) {
	if dir := os.Getenv(HomeEnv); dir != "" {
		return dir, nil
	}
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfg, "interviews"), nil
}

func sessionsDir() (string, error) {
	home, err := Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "sessions"), nil
}

func path(seed string) (string, error) {
	dir, err := sessionsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, seed+".json"), nil
}

// Save writes a session record and makes it current.
func Save(s *Session) error {
	if err := ValidSeed(s.Seed); err != nil {
		return err
	}
	p, err := path(s.Seed)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	// The record carries both URL tokens, which are the only thing standing
	// between the internet and a shell.
	if err := fileio.WriteAtomic(p, raw, 0o600); err != nil {
		return err
	}
	return SetCurrent(s.Seed)
}

// Load reads one session record.
func Load(seed string) (*Session, error) {
	p, err := path(seed)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no session %q on this machine (try interviews sessions)", seed)
		}
		return nil, err
	}
	var s Session
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	return &s, nil
}

// List returns every recorded session, newest first.
func List() ([]*Session, error) {
	dir, err := sessionsDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []*Session
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		s, err := Load(strings.TrimSuffix(name, ".json"))
		if err != nil {
			// One unreadable record must not hide the rest, which is the whole
			// point of a file per session.
			continue
		}
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b *Session) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return out, nil
}

// Remove forgets a session, and the current pointer with it if it pointed
// there. The workdir and any cloud resources are not this package's to
// delete.
func Remove(seed string) error {
	p, err := path(seed)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	if cur, err := currentSeed(); err == nil && cur == seed {
		home, err := Home()
		if err != nil {
			return err
		}
		if err := os.Remove(filepath.Join(home, currentFile)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// SetCurrent points the current marker at a seed.
func SetCurrent(seed string) error {
	home, err := Home()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	return fileio.WriteAtomic(filepath.Join(home, currentFile), []byte(seed+"\n"), 0o600)
}

func currentSeed() (string, error) {
	home, err := Home()
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(filepath.Join(home, currentFile))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}

// ErrNoCurrent means nothing has been started on this machine, so there is
// no session for a command to assume.
var ErrNoCurrent = errors.New("no current session")

// Current is the session commands mean when no seed is given: the last one
// started, or the only unended one if the marker is gone. Two unended
// sessions and no marker is ambiguous on purpose.
func Current() (*Session, error) {
	if seed, err := currentSeed(); err == nil && seed != "" {
		s, err := Load(seed)
		if err == nil {
			return s, nil
		}
	}
	all, err := List()
	if err != nil {
		return nil, err
	}
	var live []*Session
	for _, s := range all {
		if s.EndedAt.IsZero() {
			live = append(live, s)
		}
	}
	switch len(live) {
	case 0:
		return nil, ErrNoCurrent
	case 1:
		return live[0], nil
	default:
		seeds := make([]string, 0, len(live))
		for _, s := range live {
			seeds = append(seeds, s.Seed)
		}
		return nil, fmt.Errorf("%d sessions are open (%s); say which with --seed",
			len(live), strings.Join(seeds, ", "))
	}
}

// Prefixes the evidence bucket uses for everything that is not one
// interview. A seed is a key under the same bucket and the host's role is
// scoped to <bucket>/<seed>/*, so a seed spelling one of these would hand
// that host write access to shared data: "tarballs" is the bundle every
// future host downloads and executes, "state" is every interview's
// terraform state. The CLI builds its keys from these, and a test pins that.
const (
	StatePrefix   = "state"
	TarballPrefix = "tarballs"
)

var reservedSeeds = []string{StatePrefix, TarballPrefix}

// ValidSeed rejects anything that would not be safe as a file name, a
// cluster name, or a bucket key. Seeds reach all three, and one with a
// slash in it would write the registry somewhere else entirely.
func ValidSeed(seed string) error {
	if seed == "" {
		return errors.New("empty seed")
	}
	if slices.Contains(reservedSeeds, seed) {
		return fmt.Errorf("seed %q is reserved: the evidence bucket already keeps %s/ for every interview", seed, seed)
	}
	if len(seed) > 64 {
		return fmt.Errorf("seed %q is longer than 64 characters", seed)
	}
	for _, r := range seed {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
		default:
			return fmt.Errorf("seed %q: use lower-case letters, digits, and dashes", seed)
		}
	}
	return nil
}

// Seed words. Short, neutral, and easy to read aloud over a call, because
// a seed gets spoken as often as it gets typed.
var (
	adjectives = []string{
		"amber", "brisk", "calm", "clear", "curious", "eager", "early", "fair",
		"gentle", "glad", "keen", "kind", "level", "lucid", "mellow", "merry",
		"noble", "patient", "plain", "prompt", "quiet", "rapid", "ready", "solid",
		"steady", "stern", "sunny", "swift", "tidy", "true", "warm", "wise",
	}
	nouns = []string{
		"badger", "bison", "cedar", "comet", "crane", "delta", "eagle", "ember",
		"falcon", "ferry", "harbor", "heron", "ibex", "jetty", "kestrel", "lagoon",
		"lantern", "marlin", "meadow", "onyx", "osprey", "otter", "quarry", "raven",
		"ridge", "sable", "shale", "sparrow", "summit", "tundra", "walrus", "willow",
	}
)

// NewSeed invents an unused seed for a session starting at now. The date
// suffix is what makes a directory of them readable a week later.
func NewSeed(now time.Time) (string, error) {
	stamp := now.Format("0102")
	for range 200 {
		seed := fmt.Sprintf("%s-%s-%s",
			adjectives[rand.IntN(len(adjectives))], nouns[rand.IntN(len(nouns))], stamp)
		p, err := path(seed)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(p); os.IsNotExist(err) {
			return seed, nil
		}
	}
	return "", errors.New("could not find an unused seed; interviews sessions clean removes old ones")
}
