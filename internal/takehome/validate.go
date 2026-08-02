package takehome

import (
	"io/fs"
	"strings"

	"github.com/sean-reid/interviews/internal/content"
)

// ProbesPath is the live-review question bank every take-home must ship.
// The score comes from that review, not from the submitted artifact.
const ProbesPath = content.ProbesPath

// Validate checks the rules specific to take-home problems, on top of the
// generic content validation. The registry runs it at load time.
func Validate(p *content.Problem) []content.Issue {
	var issues []content.Issue

	info, err := fs.Stat(p.FS, ProbesPath)
	if err != nil || info.IsDir() {
		issues = append(issues, content.Issue{Path: ProbesPath,
			Msg: "take-home problems need the live-review question bank"})
	}

	// The brief must set up the stopping-point writeup; ABOUT.md repeats
	// the instruction but the brief is the task statement.
	if raw, err := fs.ReadFile(p.FS, content.BriefPath); err == nil {
		text := strings.ToLower(string(raw))
		if !strings.Contains(text, "stopping-point") && !strings.Contains(text, "stopping point") {
			issues = append(issues, content.Issue{Path: content.BriefPath,
				Msg: "brief must tell the candidate to write the stopping-point writeup"})
		}
	}
	return issues
}
