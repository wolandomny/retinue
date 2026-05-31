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
	"time"

	"github.com/wolandomny/retinue/internal/session"
	"github.com/wolandomny/retinue/internal/shell"
)

// resultGracePolls and resultGraceInterval bound how long Run re-reads the
// tee'd log file looking for a {"type":"result"} event after the worker's
// tmux command has signaled completion. tmux wait-for can fire a hair before
// the final `tee` flush lands on disk, so a short grace recovers the result
// TEXT. This grace is ONLY about recovering result text; it never decides
// success/failure — that decision is made by the caller (dispatchOne), which
// uses commit presence as the authority for repo tasks.
const (
	resultGracePolls    = 10
	resultGraceInterval = 100 * time.Millisecond
)

// claudeCodeEnvVar is the environment variable name unset via env -u
// to allow nested Claude Code invocations.
const claudeCodeEnvVar = "CLAUDECODE"

// defaultSuffixLen is the length of the random suffix appended to
// auto-generated window names.
const defaultSuffixLen = 6

// TmuxRunner runs the claude CLI inside a named tmux window.
// Users can attach to the session with: tmux attach-session -t <session>
type TmuxRunner struct {
	Sessions session.Manager
}

// NewTmuxRunner returns a TmuxRunner backed by the given session Manager.
func NewTmuxRunner(mgr session.Manager) *TmuxRunner {
	return &TmuxRunner{Sessions: mgr}
}

// Run implements Runner by spawning claude inside a window of the apartment's
// tmux session.
func (r *TmuxRunner) Run(ctx context.Context, opts RunOpts) (Result, error) {
	// 1. Determine window name.
	windowName := opts.WindowName
	if windowName == "" {
		windowName = "retinue-" + randomSuffix(defaultSuffixLen)
	}

	// Determine session name.
	aptSession := opts.ApartmentSession
	if aptSession == "" {
		aptSession = "retinue"
	}

	// 2. Build the claude command arguments (unchanged).
	args := buildTmuxClaudeArgs(opts)

	// Unset CLAUDECODE so claude doesn't refuse to run inside a retinue session.
	// Also inject any extra environment variables (e.g. GH_TOKEN).
	envParts := []string{"env", "-u", claudeCodeEnvVar}
	for _, e := range opts.Env {
		envParts = append(envParts, shell.Quote(e))
	}
	claudeCmd := strings.Join(envParts, " ") + " claude " + shell.Join(args)

	// 3. Wrap command to tee output and signal tmux on exit.
	// Use windowName as the wait-for channel (unique per task).
	waitCmd := "tmux"
	if opts.Socket != "" {
		waitCmd += " -L " + shell.Quote(opts.Socket)
	}
	waitCmd += " wait-for -S " + windowName

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

	// 4. Create a window in the apartment session.
	workDir := opts.WorkDir
	if workDir == "" {
		workDir = "."
	}
	if err := r.Sessions.CreateWindow(ctx, aptSession, windowName, workDir, command); err != nil {
		return Result{}, fmt.Errorf("creating tmux window %q: %w", windowName, err)
	}

	// 5. Wait for the window's command to signal completion.
	if err := r.Sessions.Wait(ctx, windowName); err != nil {
		return Result{}, fmt.Errorf("waiting for tmux window %q: %w", windowName, err)
	}

	// 6. Parse the log file for a {"type":"result"} event to recover the
	// worker's final result TEXT.
	//
	// tmux wait-for can signal completion a moment before the final `tee` flush
	// reaches disk, so a missing result event on the first read is NOT proof of
	// failure — it may just be an unflushed line. We re-read the log a few times
	// over a short grace window to recover the text if it shows up.
	//
	// Crucially, this runner NO LONGER classifies success/failure. A missing
	// result event is non-authoritative: we return whatever text we have (often
	// empty) with ExitCode 0 and let the caller decide. For repo tasks the
	// caller (dispatchOne) uses COMMIT PRESENCE as the authority, which both
	// recovers real committed work whose result line never flushed (incident 1)
	// and rejects phantom/errored runs that committed nothing (incident 2).
	resultStr := ""
	resultFound := false
	if opts.LogFile != "" {
		for attempt := 0; attempt < resultGracePolls; attempt++ {
			resultStr, resultFound = parseResultFromLog(opts.LogFile)
			if resultFound {
				break
			}
			// Sleep between polls, but not after the final attempt.
			if attempt < resultGracePolls-1 {
				select {
				case <-ctx.Done():
					return Result{Output: resultStr, ExitCode: 0}, nil
				case <-time.After(resultGraceInterval):
				}
			}
		}
	}

	// 7. Return best-effort output. ExitCode 0 / nil err means "ran to a tmux
	// signal"; it does NOT assert the worker succeeded. The caller decides.
	return Result{Output: resultStr, ExitCode: 0}, nil
}

// parseResultFromLog reads the tee'd log file and returns the result text from
// the last {"type":"result"} event, plus whether such an event was found.
func parseResultFromLog(logFile string) (result string, found bool) {
	data, err := os.ReadFile(logFile)
	if err != nil {
		return "", false
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		var event claudeStreamEvent
		if err := json.Unmarshal([]byte(line), &event); err == nil && event.Type == "result" {
			result = event.Result
			found = true
		}
	}
	return result, found
}

// buildTmuxClaudeArgs constructs the claude CLI argument list for a tmux run.
// It is a pure function of opts so that argument construction (including
// effort handling) can be exercised in unit tests without a tmux server.
// The ordering mirrors the original inline construction to keep the produced
// command byte-for-byte identical.
func buildTmuxClaudeArgs(opts RunOpts) []string {
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
	args = applyEffort(args, opts.Effort)
	if opts.DisallowedTools != "" {
		args = append(args, "--disallowed-tools="+opts.DisallowedTools)
	}
	args = append(args, opts.Prompt)
	return args
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
