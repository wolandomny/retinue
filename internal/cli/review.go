package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/wolandomny/retinue/internal/agent"
	"github.com/wolandomny/retinue/internal/task"
)

// reviewVerdict represents the outcome of a pre-merge review.
type reviewVerdict struct {
	Approved bool
	Feedback string
	Usage    agent.UsageSummary
}

// reviewDiff runs a lightweight Claude review of the task's diff
// against its original prompt. Returns a verdict.
func reviewDiff(ctx context.Context, worktreePath string, t task.Task, model, logsPath string) (reviewVerdict, error) {
	// Get the diff against the base branch.
	diff, err := runGit(ctx, worktreePath, "diff", baseBranch+"...HEAD")
	if err != nil {
		return reviewVerdict{}, fmt.Errorf("getting diff: %w", err)
	}

	if strings.TrimSpace(diff) == "" {
		return reviewVerdict{Approved: true, Feedback: "no changes"}, nil
	}

	// Truncate very large diffs to avoid blowing context.
	const maxDiffLen = 50000
	if len(diff) > maxDiffLen {
		diff = diff[:maxDiffLen] + "\n... (truncated)"
	}

	prompt := fmt.Sprintf(
		"Review this diff for task %q.\n\n"+
			"## Task prompt\n%s\n\n"+
			"## Diff\n```\n%s\n```\n\n"+
			"## Instructions\n"+
			"1. Does the diff fulfill the task prompt? Check that all requested changes are present.\n"+
			"2. Are there obvious bugs, missing error handling, or logic errors?\n"+
			"3. Are there files modified that seem outside the task's scope?\n\n"+
			"Respond with EXACTLY one of these on the FIRST LINE:\n"+
			"APPROVED - if the diff looks good\n"+
			"REJECTED - if there are issues\n\n"+
			"Then provide a brief explanation (2-3 sentences max).",
		t.ID, t.Prompt, diff,
	)

	logFile := filepath.Join(logsPath, t.ID+"-review.log")

	runner := agent.NewClaudeRunner()
	result, err := runner.Run(ctx, agent.RunOpts{
		Prompt: prompt,
		SystemPrompt: "You are a code reviewer. Be concise. " +
			"Approve work that substantially fulfills the task, even if imperfect. " +
			"Reject only if there are clear omissions, bugs, or scope violations.",
		WorkDir: worktreePath,
		Model:   model,
		LogFile: logFile,
	})
	if err != nil {
		return reviewVerdict{}, fmt.Errorf("review agent failed: %w", err)
	}

	// Parse usage.
	usage, _ := agent.ParseUsageFromLog(logFile)

	// Parse verdict from first line of output.
	output := strings.TrimSpace(result.Output)
	approved := strings.HasPrefix(strings.ToUpper(output), "APPROVED")

	return reviewVerdict{
		Approved: approved,
		Feedback: output,
		Usage:    usage,
	}, nil
}
