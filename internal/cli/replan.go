package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/wolandomny/retinue/internal/agent"
	"github.com/wolandomny/retinue/internal/task"
)

// replanResult holds the output of a re-planning attempt.
type replanResult struct {
	RevisedPrompt string
	Usage         agent.UsageSummary
}

// replanFailedTask uses a lightweight Claude call to analyze a
// failed task and produce a revised prompt that avoids the failure.
// Returns the revised prompt, or falls back to mechanical retry
// if the re-plan itself fails.
func replanFailedTask(ctx context.Context, t task.Task, model, logsPath string) (replanResult, error) {
	// Truncate error and result to avoid context bloat.
	errText := t.Error
	if len(errText) > 2000 {
		errText = errText[:2000] + "\n... (truncated)"
	}
	resultText := t.Result
	if len(resultText) > 3000 {
		resultText = resultText[:3000] + "\n... (truncated)"
	}

	prompt := fmt.Sprintf(
		"A worker agent failed to complete a task. Analyze the failure "+
			"and rewrite the task prompt to avoid the same issue.\n\n"+
			"## Original Task Prompt\n%s\n\n"+
			"## Error\n```\n%s\n```\n\n"+
			"## Worker Output (last portion)\n```\n%s\n```\n\n"+
			"## Instructions\n"+
			"Rewrite the ENTIRE task prompt incorporating lessons from the failure. "+
			"Be specific about what went wrong and what to do differently. "+
			"Do NOT just append an error message — restructure the prompt if needed. "+
			"Output ONLY the revised prompt text, nothing else.",
		t.Prompt, errText, resultText,
	)

	logFile := filepath.Join(logsPath, t.ID+"-replan.log")

	runner := agent.NewClaudeRunner()
	result, err := runner.Run(ctx, agent.RunOpts{
		Prompt: prompt,
		SystemPrompt: "You are a task planning assistant. You analyze failed tasks " +
			"and rewrite their prompts to avoid the same failures. " +
			"Output only the revised prompt — no preamble, no explanation.",
		Model:   model,
		LogFile: logFile,
	})
	if err != nil {
		return replanResult{}, fmt.Errorf("replan agent failed: %w", err)
	}

	usage, _ := agent.ParseUsageFromLog(logFile)

	revised := strings.TrimSpace(result.Output)
	if revised == "" {
		return replanResult{}, fmt.Errorf("replan returned empty prompt")
	}

	return replanResult{
		RevisedPrompt: revised,
		Usage:         usage,
	}, nil
}
