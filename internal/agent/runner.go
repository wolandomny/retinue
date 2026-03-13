package agent

import "context"

// RunOpts configures a single agent run.
type RunOpts struct {
	Prompt           string
	SystemPrompt     string
	WorkDir          string
	Model            string
	LogFile          string
	WindowName       string // tmux window name within the apartment session
	ApartmentSession string // tmux session name (e.g. "retinue")
	Socket           string // tmux socket name (-L flag); if empty, uses default socket
	Env              []string // extra env vars to inject (e.g. "GH_TOKEN=xxx")
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
