// Package provenance records what an artifact was produced on. Comparing
// two candidates on one seeded problem only holds if both ran on the same
// thing, and an evidence bundle used to say nothing at all about the
// machine: two bundles from two different Kubernetes versions were
// indistinguishable.
package provenance

import (
	"fmt"
	"os"
	"time"

	"github.com/sean-reid/interviews/internal/version"
)

// Where an interview ran.
type Where string

// The two machines an interview happens on.
const (
	Local Where = "local"
	Host  Where = "host"
)

// What a provisioned host is told about itself. Its user-data writes both
// into /etc/interviews/session.env, which the session unit loads, so every
// command that builds the environment inherits them. Nothing sets either on
// a laptop, and that absence is what tells the two apart.
const (
	WhereEnv          = "IV_WHERE"
	ContentVersionEnv = "IV_CONTENT_VERSION"
)

// Record is what one artifact was produced on. Everything past the platform
// version is optional and absent when it could not be determined: an empty
// string in an evidence bundle reads as a version, and the wrong one.
type Record struct {
	Platform string `json:"platform_version,omitempty"`
	Where    Where  `json:"where,omitempty"`
	// Content identifies the problems: the version id of the tarball a host
	// unpacked, or the commit of the checkout a laptop read.
	Content string `json:"content_version,omitempty"`
	// The substrate, on the types that have one.
	NodeImage string `json:"node_image,omitempty"`
	Kind      string `json:"kind_version,omitempty"`
	Kubectl   string `json:"kubectl_version,omitempty"`
	// Undetermined names what this record could not read, and why. A record
	// is a precondition for nothing: a version call that failed leaves a line
	// here rather than stopping an environment coming up.
	Undetermined []string  `json:"undetermined,omitempty"`
	At           time.Time `json:"recorded_at,omitzero"`
}

// New starts a record for this binary, now, against the given content.
func New(where Where, content string) Record {
	return Record{Platform: version.Version, Where: where, Content: content, At: time.Now()}
}

// Running reports which machine this is.
func Running() Where {
	if Where(os.Getenv(WhereEnv)) == Host {
		return Host
	}
	return Local
}

// Missing records something that could not be determined, so the gap reads
// as a gap rather than as an absent feature.
func (r *Record) Missing(what string, err error) {
	r.Undetermined = append(r.Undetermined, fmt.Sprintf("%s: %v", what, err))
}
