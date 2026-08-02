// Package variant resolves a problem's parameters for one interview,
// deterministically. The same problem and interview id always produce the
// same values, so a session can be reproduced exactly for regrading, and
// each parameter draws from its own derived seed, so adding a parameter to
// a problem never changes the values existing parameters resolve to.
package variant

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"maps"
	"math/rand/v2"
	"slices"
	"strconv"

	"github.com/sean-reid/interviews/internal/content"
)

// Resolved is one interview's fully resolved variant, with the provenance
// needed to reproduce it. It serializes into evidence bundles.
type Resolved struct {
	Problem     string            `json:"problem"`
	InterviewID string            `json:"interview_id"`
	Params      map[string]any    `json:"params"`
	Overrides   map[string]string `json:"overrides,omitempty"`
}

// Resolve computes the variant for one interview. Overrides win over
// everything and are validated against the spec; a parameter with a default
// is pinned to it; anything else draws from the interview seed.
func Resolve(problemID string, specs map[string]content.ParamSpec, interviewID string, overrides map[string]string) (*Resolved, error) {
	for name := range overrides {
		if _, ok := specs[name]; !ok {
			return nil, fmt.Errorf("override %q: no such parameter", name)
		}
	}

	r := &Resolved{
		Problem:     problemID,
		InterviewID: interviewID,
		Params:      make(map[string]any, len(specs)),
	}
	if len(overrides) > 0 {
		r.Overrides = overrides
	}

	for _, name := range slices.Sorted(maps.Keys(specs)) {
		v, err := resolveParam(problemID, interviewID, name, specs[name], overrides)
		if err != nil {
			return nil, err
		}
		r.Params[name] = v
	}
	return r, nil
}

func resolveParam(problemID, interviewID, name string, spec content.ParamSpec, overrides map[string]string) (any, error) {
	if raw, ok := overrides[name]; ok {
		return convertOverride(name, spec, raw)
	}
	if spec.Default != nil {
		return spec.Default, nil
	}
	rng := paramRNG(problemID, interviewID, name)
	switch spec.Type {
	case content.Choice:
		if len(spec.Of) == 0 {
			return nil, fmt.Errorf("param %q: empty choice list", name)
		}
		return spec.Of[rng.IntN(len(spec.Of))], nil
	case content.Int:
		if spec.Min == nil || spec.Max == nil || *spec.Min > *spec.Max {
			return nil, fmt.Errorf("param %q: invalid int bounds", name)
		}
		return *spec.Min + rng.IntN(*spec.Max-*spec.Min+1), nil
	case content.String:
		return nil, fmt.Errorf("param %q: string parameter has no default", name)
	default:
		return nil, fmt.Errorf("param %q: unknown type %q", name, spec.Type)
	}
}

func convertOverride(name string, spec content.ParamSpec, raw string) (any, error) {
	switch spec.Type {
	case content.Choice:
		if slices.Contains(spec.Of, raw) {
			return raw, nil
		}
		return nil, fmt.Errorf("override %q: %q is not one of %v", name, raw, spec.Of)
	case content.Int:
		n, err := strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("override %q: %q is not an integer", name, raw)
		}
		if spec.Min != nil && spec.Max != nil && (n < *spec.Min || n > *spec.Max) {
			return nil, fmt.Errorf("override %q: %d outside [%d, %d]", name, n, *spec.Min, *spec.Max)
		}
		return n, nil
	case content.String:
		return raw, nil
	default:
		return nil, fmt.Errorf("override %q: unknown type %q", name, spec.Type)
	}
}

// paramRNG derives an independent deterministic stream per parameter.
func paramRNG(problemID, interviewID, param string) *rand.Rand {
	sum := HashParts(problemID, interviewID, param)
	return rand.New(rand.NewPCG(
		binary.BigEndian.Uint64(sum[0:8]),
		binary.BigEndian.Uint64(sum[8:16]),
	))
}

// HashParts digests parts with each one length-prefixed, so no two part
// lists share a digest.
func HashParts(parts ...string) []byte {
	h := sha256.New()
	for _, s := range parts {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(s)))
		h.Write(n[:])
		h.Write([]byte(s))
	}
	return h.Sum(nil)
}
