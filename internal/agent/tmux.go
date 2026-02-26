package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"strings"

	"github.com/wolandomny/retinue/internal/session"
)

// TmuxRunner runs the claude CLI inside a named tmux session.
// Users can attach to the session with: tmux attach-session -t <name>
type TmuxRunner struct {
	Sessions session.Manager
}

// NewTmuxRunner returns a TmuxRunner backed by the given session Manager.
func NewTmuxRunner(mgr session.Manager) *TmuxRunner {
	return &TmuxRunner{Sessions: mgr}
}

// Run implements Runner by spawning claude inside a detached tmux session.
func (r *TmuxRunner) Run(ctx context.Context, opts RunOpts) (Result, error) {
	// 1. Determine session name.
	sessionName := opts.SessionName
	if sessionName == "" {
		sessionName = "retinue-" + randomSuffix(6)
	}

	// 2. Build the claude command arguments (same as ClaudeRunner).
	args := []string{
		"--print",
		"--verbose",
		"--output-format", "stream-json",
		"--dangerously-skip-permissions",
	}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	if opts.SystemPrompt != "" {
		args = append(args, "--system-prompt", opts.SystemPrompt)
	}
	args = append(args, opts.Prompt)

	// Unset CLAUDECODE so claude doesn't refuse to run inside a retinue session.
	claudeCmd := "env -u CLAUDECODE claude " + shellJoin(args)

	// 3. Wrap command to tee output and signal tmux on exit.
	waitCmd := "tmux"
	if opts.Socket != "" {
		waitCmd += " -L " + shellQuote(opts.Socket)
	}
	waitCmd += " wait-for -S " + sessionName

	var command string
	if opts.LogFile != "" {
		command = fmt.Sprintf("%s 2>&1 | tee %s; %s",
			claudeCmd, shellQuote(opts.LogFile), waitCmd)
	} else {
		command = fmt.Sprintf("%s; %s", claudeCmd, waitCmd)
	}

	// 4. Create the tmux session.
	workDir := opts.WorkDir
	if workDir == "" {
		workDir = "."
	}
	if err := r.Sessions.Create(ctx, sessionName, workDir, command); err != nil {
		return Result{}, fmt.Errorf("creating tmux session %q: %w", sessionName, err)
	}

	// 5. Wait for the session to signal completion.
	if err := r.Sessions.Wait(ctx, sessionName); err != nil {
		return Result{}, fmt.Errorf("waiting for tmux session %q: %w", sessionName, err)
	}

	// 6. Parse log file for result event.
	resultStr := ""
	if opts.LogFile != "" {
		data, err := os.ReadFile(opts.LogFile)
		if err == nil {
			scanner := bufio.NewScanner(strings.NewReader(string(data)))
			for scanner.Scan() {
				line := scanner.Text()
				var event claudeStreamEvent
				if err := json.Unmarshal([]byte(line), &event); err == nil && event.Type == "result" {
					resultStr = event.Result
				}
			}
		}
	}

	// 7. Return result.
	return Result{Output: resultStr, ExitCode: 0}, nil
}

// randomSuffix returns a random lowercase alphanumeric string of length n.
func randomSuffix(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}

// shellJoin joins arguments into a shell-safe command string.
func shellJoin(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = shellQuote(a)
	}
	return strings.Join(parts, " ")
}

// shellQuote wraps s in single quotes, escaping any single quotes within.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
