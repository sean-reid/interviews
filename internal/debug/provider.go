package debug

import (
	"context"
	"fmt"
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
