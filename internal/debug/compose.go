package debug

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sean-reid/interviews/internal/provenance"
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

// provenance adds nothing. There is no node image and no cluster tooling
// here, and those fields stay absent rather than empty: an empty version in
// an evidence bundle reads as one nobody could determine.
func (p *composeProvider) provenance(context.Context, *provenance.Record) {}

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

// Down tears the project down by project name when the rendered compose
// file is gone. Engine.Down removes the workdir the file lives in, so a
// second teardown, or any workdir loss, would otherwise leave containers
// running with no route back through the tool.
func (p *composeProvider) Down(ctx context.Context) error {
	args := []string{"compose"}
	if _, err := os.Stat(p.file()); err == nil {
		args = append(args, "-f", p.file())
	}
	args = append(args, "-p", p.project(), "down", "-v", "--remove-orphans")
	return p.e.Runner.Command(ctx, "docker", args...)
}
