package agent

import "context"

// RunOpts configures a single agent run.
type RunOpts struct {
	Prompt       string
	SystemPrompt string
	WorkDir      string
	Model        string
	LogFile      string
}

// Result holds the output of an agent run.
type Result struct {
	Output   string
	ExitCode int
}

// Runner abstracts the execution of a Claude Code agent.
type Runner interface {
	Run(ctx context.Context, opts RunOpts) (Result, error)
}
