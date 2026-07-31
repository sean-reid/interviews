package variant

import (
	"fmt"
	"strings"
	"text/template"
)

// Render substitutes resolved parameters into candidate-facing text. A
// reference to a parameter the variant does not define is an error, never a
// silent blank: a bundle with a hole in it must not reach a candidate.
func Render(text string, r *Resolved) (string, error) {
	tmpl, err := template.New("").Option("missingkey=error").Parse(text)
	if err != nil {
		return "", fmt.Errorf("parsing template: %w", err)
	}
	var out strings.Builder
	if err := tmpl.Execute(&out, r.Params); err != nil {
		return "", fmt.Errorf("rendering: %w", err)
	}
	return out.String(), nil
}
