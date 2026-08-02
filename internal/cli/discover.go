package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/sean-reid/interviews/internal/interview"
)

// The tags the interview module puts on everything it creates. They are what
// makes the account itself an index of running interviews, so they have to
// match infra/aws/interview/versions.tf exactly; a test asserts that they do.
const (
	tagManagedBy = "ManagedBy"
	tagOwner     = "interviews"
	tagInterview = "Interview"
	tagProblem   = "Problem"
	tagTTL       = "TTLMinutes"
)

// describeTimeout bounds a discovery. A listing that hangs on the network is
// worse than one that says it could not ask.
const describeTimeout = 20 * time.Second

// remoteHost is a host the account knows about, which is not the same set as
// the sessions this machine started.
type remoteHost struct {
	Seed       string
	Problem    string
	State      string
	LaunchedAt time.Time
	TTLMinutes int
}

// status reads a discovered host the way sessionStatus reads a local one,
// except that the clock starts at the launch the API reports rather than at
// a record this machine may not have.
func (h remoteHost) status() (string, int) {
	switch h.State {
	case "pending":
		return "provisioning", rankLive
	case "stopping", "stopped":
		// Nothing stops an interview host: it terminates on ttl or on end. A
		// stopped one is a mistake that still bills for its disk.
		return "stopped, still holding its disk", rankStranded
	}
	if h.TTLMinutes > 0 {
		left := time.Until(h.LaunchedAt.Add(time.Duration(h.TTLMinutes) * time.Minute))
		if left <= 0 {
			return "past its ttl", rankStranded
		}
		return "up, " + age(left) + " of ttl left", rankLive
	}
	return "up", rankLive
}

func (h remoteHost) session() *interview.Session {
	return &interview.Session{
		Seed: h.Seed, Problem: h.Problem, Mode: interview.AWS, CreatedAt: h.LaunchedAt,
	}
}

// reconcile settles the local listing against what the account reports. Both
// directions matter: a record whose host is gone, and a host no record here
// claims. The second is the one that bills for hours unnoticed.
func reconcile(rows []sessionRow, known map[string]*interview.Session, hosts []remoteHost) []sessionRow {
	byseed := make(map[string]remoteHost, len(hosts))
	for _, h := range hosts {
		byseed[h.Seed] = h
	}
	shown := make(map[string]bool, len(rows))
	for i := range rows {
		r := &rows[i]
		shown[r.s.Seed] = true
		if r.s.Mode != interview.AWS {
			continue
		}
		h, live := byseed[r.s.Seed]
		switch {
		case !r.s.EndedAt.IsZero():
			if live {
				r.state, r.rank = "still up after end", rankStranded
			}
		case live:
			r.state, r.rank = h.status()
		case r.s.Host != "":
			// The record has an address and the account has no instance, so
			// something destroyed the host without ending the session. The
			// elastic IP usually outlives it.
			r.state, r.rank = "host gone", rankStranded
		}
		// A session with no address yet is left alone: its apply may still be
		// running, and calling that gone would cry wolf on every provision.
	}
	for _, h := range hosts {
		if shown[h.Seed] {
			continue
		}
		state, rank := h.status()
		row := sessionRow{s: h.session(), state: state, rank: rank}
		if rec, ok := known[h.Seed]; ok {
			// Started here and already ended, so the destroy did not finish.
			row.s, row.state, row.rank = rec, "still up after end", rankStranded
		} else {
			row.elsewhere = true
		}
		rows = append(rows, row)
	}
	return rows
}

// describeHosts asks EC2 which interview instances exist. Terminated ones are
// left out: they cost nothing and there is no action to take on them.
func describeHosts(ctx context.Context, cfg *interview.AWSSetup) ([]remoteHost, error) {
	raw, err := awsJSON(ctx, cfg, "ec2", "describe-instances",
		"--filters",
		"Name=tag:"+tagManagedBy+",Values="+tagOwner,
		"Name=instance-state-name,Values=pending,running,stopping,stopped")
	if err != nil {
		return nil, err
	}
	return parseHosts(raw)
}

func parseHosts(raw []byte) ([]remoteHost, error) {
	var out struct {
		Reservations []struct {
			Instances []struct {
				LaunchTime time.Time `json:"LaunchTime"`
				State      struct {
					Name string `json:"Name"`
				} `json:"State"`
				Tags []struct {
					Key   string `json:"Key"`
					Value string `json:"Value"`
				} `json:"Tags"`
			} `json:"Instances"`
		} `json:"Reservations"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("reading what ec2 said: %w", err)
	}
	var hosts []remoteHost
	for _, r := range out.Reservations {
		for _, i := range r.Instances {
			h := remoteHost{State: i.State.Name, LaunchedAt: i.LaunchTime}
			for _, t := range i.Tags {
				switch t.Key {
				case tagInterview:
					h.Seed = t.Value
				case tagProblem:
					h.Problem = t.Value
				case tagTTL:
					h.TTLMinutes, _ = strconv.Atoi(t.Value)
				}
			}
			// An instance with the owner tag but no seed is not something this
			// tool can name, let alone act on. Skipping it silently would hide
			// a cost, so it is listed under a seed that says what it is.
			if h.Seed == "" {
				h.Seed = "(untagged)"
			}
			hosts = append(hosts, h)
		}
	}
	return hosts, nil
}

// strandedAddresses counts elastic IPs the module allocated that are attached
// to nothing. They bill by the hour and no instance listing can see them,
// which is exactly the leftover a half-finished destroy produces.
func strandedAddresses(ctx context.Context, cfg *interview.AWSSetup) (int, error) {
	raw, err := awsJSON(ctx, cfg, "ec2", "describe-addresses",
		"--filters", "Name=tag:"+tagManagedBy+",Values="+tagOwner)
	if err != nil {
		return 0, err
	}
	var out struct {
		Addresses []struct {
			AssociationID string `json:"AssociationId"`
		} `json:"Addresses"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return 0, fmt.Errorf("reading what ec2 said: %w", err)
	}
	n := 0
	for _, a := range out.Addresses {
		if a.AssociationID == "" {
			n++
		}
	}
	return n, nil
}

func awsJSON(ctx context.Context, cfg *interview.AWSSetup, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, describeTimeout)
	defer cancel()
	full := append(append([]string{}, args...), "--region", cfg.Region, "--output", "json")
	cmd := exec.CommandContext(ctx, "aws", full...)
	cmd.Env = os.Environ()
	if cfg.Profile != "" {
		cmd.Env = append(cmd.Env, "AWS_PROFILE="+cfg.Profile)
	}
	var errb strings.Builder
	cmd.Stderr = &errb
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("aws %s gave up after %s", args[1], describeTimeout)
	}
	if err != nil {
		return nil, fmt.Errorf("aws %s: %w: %s", args[1], err, strings.TrimSpace(errb.String()))
	}
	return out, nil
}
