package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ClaudeRunner spawns claude CLI processes.
type ClaudeRunner struct{}

func NewClaudeRunner() *ClaudeRunner {
	return &ClaudeRunner{}
}

type claudeJSONOutput struct {
	Result string `json:"result"`
}

func (r *ClaudeRunner) Run(ctx context.Context, opts RunOpts) (Result, error) {
	args := []string{
		"--print",
		"--output-format", "json",
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

	// Try to extract result from JSON output.
	raw := output.String()
	var parsed claudeJSONOutput
	if err := json.Unmarshal([]byte(raw), &parsed); err == nil && parsed.Result != "" {
		return Result{Output: parsed.Result, ExitCode: 0}, nil
	}

	return Result{Output: raw, ExitCode: 0}, nil
}

type stringWriter interface {
	Write(p []byte) (n int, err error)
}

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
