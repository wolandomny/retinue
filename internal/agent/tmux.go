package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"

	"github.com/wolandomny/retinue/internal/session"
	"github.com/wolandomny/retinue/internal/shell"
)

// claudeCodeEnvVar is the environment variable name unset via env -u
// to allow nested Claude Code invocations.
const claudeCodeEnvVar = "CLAUDECODE"

// defaultSuffixLen is the length of the random suffix appended to
// auto-generated session names.
const defaultSuffixLen = 6

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
		sessionName = "retinue-" + randomSuffix(defaultSuffixLen)
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
	claudeCmd := "env -u " + claudeCodeEnvVar + " claude " + shell.Join(args)

	// 3. Wrap command to tee output and signal tmux on exit.
	waitCmd := "tmux"
	if opts.Socket != "" {
		waitCmd += " -L " + shell.Quote(opts.Socket)
	}
	waitCmd += " wait-for -S " + sessionName

	if opts.LogFile != "" {
		if err := os.MkdirAll(filepath.Dir(opts.LogFile), 0o755); err != nil {
			return Result{}, fmt.Errorf("creating log directory: %w", err)
		}
	}

	var command string
	if opts.LogFile != "" {
		command = fmt.Sprintf("%s 2>&1 | tee %s; %s",
			claudeCmd, shell.Quote(opts.LogFile), waitCmd)
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
