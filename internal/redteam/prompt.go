package redteam

import (
	"fmt"
	"io/fs"
	"strings"

	"github.com/sean-reid/interviews/internal/content"
	"github.com/sean-reid/interviews/internal/variant"
)

// candidatePrompt assembles what the agent is told. It may contain only what
// a candidate gets: the rendered brief and the mechanics of the environment.
// Nothing interviewer-only may appear here, or the calibration measures the
// wrong thing and leaks the answer key into a transcript.
func candidatePrompt(p *content.Problem, v *variant.Resolved, env string) (string, error) {
	raw, err := fs.ReadFile(p.FS, content.BriefPath)
	if err != nil {
		return "", fmt.Errorf("reading candidate brief: %w", err)
	}
	brief, err := variant.Render(string(raw), v)
	if err != nil {
		return "", fmt.Errorf("rendering candidate brief: %w", err)
	}

	var b strings.Builder
	b.WriteString("You are a candidate in a technical interview. This is the brief you were given.\n\n")
	b.WriteString(strings.TrimSpace(brief))
	b.WriteString("\n\n")
	if env != "" {
		b.WriteString(strings.TrimSpace(env))
		b.WriteString("\n\n")
	}
	b.WriteString("Work the problem as far as you can within your time. ")
	b.WriteString("Nobody is available to answer questions, so decide and act on your own judgement.")
	return b.String(), nil
}

// debugEnvNotes tell the agent how to reach the broken environment. They
// describe access only, never what is wrong with it.
func debugEnvNotes(kubeconfig, namespace string) string {
	if kubeconfig == "" {
		return "The environment is running on this machine. Use the shell to inspect and change it."
	}
	return fmt.Sprintf(
		"You have kubectl and a kubeconfig at %s (already exported as KUBECONFIG). "+
			"Your access is scoped to the %s namespace.", kubeconfig, namespace)
}
