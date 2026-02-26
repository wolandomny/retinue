package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// claudeCodeEnvPrefix is the environment variable prefix that must be
// unset to allow nested Claude Code invocations.
const claudeCodeEnvPrefix = "CLAUDECODE="

// ClaudeRunner spawns claude CLI processes.
type ClaudeRunner struct{}

func NewClaudeRunner() *ClaudeRunner {
	return &ClaudeRunner{}
}

// claudeStreamEvent represents a single event in the Claude CLI's
// stream-json output format. The "result" event type carries the
// agent's final output in the Result field.
type claudeStreamEvent struct {
	Type   string `json:"type"`
	Result string `json:"result"`
}

func (r *ClaudeRunner) Run(ctx context.Context, opts RunOpts) (Result, error) {
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

	cmd := exec.CommandContext(ctx, "claude", args...)
	if opts.WorkDir != "" {
		cmd.Dir = opts.WorkDir
	}

	// Unset CLAUDECODE so the worker can spawn its own claude process.
	// Without this, claude refuses to run nested inside another Claude Code session.
	env := os.Environ()
	filtered := env[:0]
	for _, e := range env {
		if !strings.HasPrefix(e, claudeCodeEnvPrefix) {
			filtered = append(filtered, e)
		}
	}
	cmd.Env = filtered

	var output strings.Builder
	cmd.Stdout = &output
	cmd.Stderr = &output

	// Also tee to log file if specified.
	if opts.LogFile != "" {
		logFile, err := os.Create(opts.LogFile)
		if err != nil {
			return Result{}, fmt.Errorf("creating log file: %w", err)
		}
		defer logFile.Close()

		cmd.Stdout = &logWriter{writers: []stringWriter{&output, logFile}}
		cmd.Stderr = &logWriter{writers: []stringWriter{&output, logFile}}
	}

	if err := cmd.Run(); err != nil {
		exitCode := 1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
		return Result{Output: output.String(), ExitCode: exitCode}, fmt.Errorf("claude process failed: %w", err)
	}

	// Scan newline-delimited JSON events to find the result event.
	// If multiple "result" events are present (unlikely but possible),
	// the last one wins.
	raw := output.String()
	var finalResult string
	scanner := bufio.NewScanner(strings.NewReader(raw))
	for scanner.Scan() {
		line := scanner.Text()
		var event claudeStreamEvent
		if err := json.Unmarshal([]byte(line), &event); err == nil && event.Type == "result" {
			finalResult = event.Result
		}
	}
	if finalResult != "" {
		return Result{Output: finalResult, ExitCode: 0}, nil
	}

	return Result{Output: raw, ExitCode: 0}, nil
}

// stringWriter is a minimal write interface used by logWriter to
// fan out writes to multiple destinations.
type stringWriter interface {
	Write(p []byte) (n int, err error)
}

// logWriter multiplexes Write calls across multiple writers.
type logWriter struct {
	writers []stringWriter
}

func (w *logWriter) Write(p []byte) (n int, err error) {
	for _, wr := range w.writers {
		n, err = wr.Write(p)
		if err != nil {
			return n, err
		}
	}
	return len(p), nil
}
