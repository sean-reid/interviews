package debug

import (
	"context"
	"fmt"
	"path/filepath"
)

// composeProvider hosts compose-linux-flavor scenarios with docker compose.
type composeProvider struct {
	e *Engine
}

func (p *composeProvider) Name() string { return "compose" }

func (p *composeProvider) project() string { return envName(p.e.Variant) }

func (p *composeProvider) file() string {
	return filepath.Join(p.e.Workdir, "rendered", p.e.Scenario.Env.Compose.File)
}

func (p *composeProvider) env(env map[string]string) {
	env["IV_COMPOSE_FILE"] = p.file()
	env["IV_PROJECT"] = p.project()
}

func (p *composeProvider) Detect(ctx context.Context) error {
	if _, err := p.e.Runner.Output(ctx, "docker", "compose", "version"); err != nil {
		return fmt.Errorf("docker compose not usable: %w", err)
	}
	return nil
}

func (p *composeProvider) Up(ctx context.Context) error {
	spec := p.e.Scenario.Env.Compose
	if _, err := p.e.render(spec.File); err != nil {
		return err
	}
	if spec.Configs != "" {
		if _, err := p.e.render(spec.Configs); err != nil {
			return err
		}
	}
	return p.e.Runner.Command(ctx, "docker", "compose",
		"-f", p.file(), "-p", p.project(), "up", "-d", "--build")
}

func (p *composeProvider) Down(ctx context.Context) error {
	return p.e.Runner.Command(ctx, "docker", "compose",
		"-f", p.file(), "-p", p.project(), "down", "-v", "--remove-orphans")
}
