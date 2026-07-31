package redteam

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

// apiDriver runs an agent loop against the Messages API with a single bash
// tool executed in the scratch directory. It is the fallback for anyone
// without the claude CLI, and needs ANTHROPIC_API_KEY.
type apiDriver struct {
	model string
}

// defaultAPIModel is the model calibration runs against unless overridden.
// Calibration should use the strongest generally available model: the
// question this harness answers is what a candidate with the best tools can
// do unassisted.
const defaultAPIModel = "claude-opus-5"

// maxTurns bounds one attempt so a looping agent cannot run forever inside
// the wall-clock budget.
const maxTurns = 200

func (d *apiDriver) Name() string { return "api" }

func (d *apiDriver) Available(_ context.Context) error {
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		return errors.New("ANTHROPIC_API_KEY is not set: export a key or use --driver claude")
	}
	return nil
}

func (d *apiDriver) Run(ctx context.Context, t Task) (*Attempt, error) {
	ctx, cancel := context.WithTimeout(ctx, t.Budget)
	defer cancel()

	model := d.model
	if model == "" {
		model = defaultAPIModel
	}
	transcript, err := os.Create(filepath.Join(t.Dir, "transcript.jsonl"))
	if err != nil {
		return nil, err
	}
	defer func() { _ = transcript.Close() }()

	client := anthropic.NewClient()
	messages := []anthropic.MessageParam{
		anthropic.NewUserMessage(anthropic.NewTextBlock(t.Prompt)),
	}
	tools := []anthropic.ToolUnionParam{
		{OfBashTool20250124: &anthropic.ToolBash20250124Param{}},
	}

	a := &Attempt{Driver: d.Name(), Model: model, Transcript: transcript.Name()}
	start := time.Now()
	defer func() { a.Duration = time.Since(start) }()

	for turn := 0; turn < maxTurns; turn++ {
		resp, err := client.Messages.New(ctx, anthropic.MessageNewParams{
			Model:     anthropic.Model(model),
			MaxTokens: 16000,
			Messages:  messages,
			Tools:     tools,
		})
		if err != nil {
			if ctx.Err() != nil {
				a.TimedOut = true
				return a, nil // the budget ending the run is the expected outcome
			}
			return a, fmt.Errorf("turn %d: %w", turn, err)
		}
		a.Turns++
		logEvent(transcript, "assistant", resp)
		messages = append(messages, resp.ToParam())

		if resp.StopReason == anthropic.StopReasonRefusal {
			logEvent(transcript, "refusal", resp.StopReason)
			return a, fmt.Errorf("model declined the task; calibrate with --driver claude or another model")
		}
		if resp.StopReason != anthropic.StopReasonToolUse {
			return a, nil
		}

		var results []anthropic.ContentBlockParamUnion
		for _, block := range resp.Content {
			use, ok := block.AsAny().(anthropic.ToolUseBlock)
			if !ok {
				continue
			}
			out, isErr := runBash(ctx, use, t.Dir)
			logEvent(transcript, "bash", map[string]any{"input": use.Input, "output": out, "error": isErr})
			results = append(results, anthropic.NewToolResultBlock(use.ID, out, isErr))
		}
		if len(results) == 0 {
			return a, nil
		}
		messages = append(messages, anthropic.NewUserMessage(results...))
	}
	return a, nil
}

// runBash executes one bash tool call inside the scratch directory. The
// environment is disposable, which is the whole point: an unassisted agent
// gets the same shell a candidate would.
func runBash(ctx context.Context, use anthropic.ToolUseBlock, dir string) (string, bool) {
	var in struct {
		Command string `json:"command"`
		Restart bool   `json:"restart"`
	}
	if err := json.Unmarshal([]byte(use.JSON.Input.Raw()), &in); err != nil {
		return fmt.Sprintf("cannot parse tool input: %v", err), true
	}
	if in.Restart {
		return "shell restarted", false
	}
	cmd := exec.CommandContext(ctx, "bash", "-c", in.Command)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	text := string(out)
	if text == "" {
		text = "(no output)"
	}
	if err != nil {
		return strings.TrimSpace(text + "\n" + err.Error()), true
	}
	return text, false
}

func logEvent(w *os.File, kind string, payload any) {
	line, err := json.Marshal(map[string]any{"type": kind, "payload": payload})
	if err != nil {
		return
	}
	fmt.Fprintln(w, string(line))
}
