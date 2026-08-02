package debug

import (
	"context"
	"fmt"

	"github.com/sean-reid/interviews/internal/provenance"
)

// Provider owns the environment substrate lifecycle. Everything it does
// goes through the engine's Runner.
type Provider interface {
	Name() string
	// Detect fails fast when required tooling is missing.
	Detect(ctx context.Context) error
	// Up creates the substrate and deploys the healthy app.
	Up(ctx context.Context) error
	Down(ctx context.Context) error
	// env contributes provider-specific variables to script environments.
	env(map[string]string)
	// provenance adds what the substrate knows about itself: the image it
	// built from, and the versions the tools that built it report. It must
	// never fail: Up records what it could not read and carries on.
	provenance(ctx context.Context, r *provenance.Record)
}

func (e *Engine) newProvider() (Provider, error) {
	switch e.Scenario.Env.Provider {
	case "kind":
		return &kindProvider{e: e}, nil
	case "compose":
		return &composeProvider{e: e}, nil
	default:
		return nil, fmt.Errorf("no provider %q", e.Scenario.Env.Provider)
	}
}
