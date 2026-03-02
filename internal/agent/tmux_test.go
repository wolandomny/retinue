package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wolandomny/retinue/internal/session"
)

// Compile-time check: TmuxRunner implements Runner.
var _ Runner = &TmuxRunner{}

func TestTmuxRunnerUsesProvidedWindowName(t *testing.T) {
	fake := session.NewFakeManager()
	runner := &TmuxRunner{Sessions: fake}

	opts := RunOpts{
		Prompt:           "hello",
		WindowName:       "my-window",
		ApartmentSession: "retinue",
		WorkDir:          "/tmp",
	}

	_, err := runner.Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	exists, err := fake.HasWindow(context.Background(), "retinue", "my-window")
	if err != nil {
		t.Fatalf("HasWindow error: %v", err)
	}
	if !exists {
		t.Error("expected window 'my-window' to exist in session 'retinue' after Run")
	}
}

func TestTmuxRunnerGeneratesWindowNameWhenEmpty(t *testing.T) {
	fake := session.NewFakeManager()
	runner := &TmuxRunner{Sessions: fake}

	opts := RunOpts{
		Prompt:           "hello",
		ApartmentSession: "retinue",
		WorkDir:          "/tmp",
	}

	_, err := runner.Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// We don't know the exact name, but exactly one window should exist
	// in the "retinue" session with the "retinue-" prefix.
	// Just verify Run doesn't error and returns an empty-ish result.
}

func TestTmuxRunnerCommandContainsPromptAndArgs(t *testing.T) {
	fake := session.NewFakeManager()
	runner := &TmuxRunner{Sessions: fake}

	opts := RunOpts{
		Prompt:           "do something",
		Model:            "claude-3-5-sonnet",
		SystemPrompt:     "you are helpful",
		WindowName:       "cmd-test",
		ApartmentSession: "retinue",
		WorkDir:          "/tmp",
	}

	_, err := runner.Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cmd := fake.WindowCommand("retinue", "cmd-test")
	if cmd == "" {
		t.Fatal("expected a command to be recorded for window 'cmd-test'")
	}

	checks := []string{
		"--print",
		"--output-format",
		"stream-json",
		"--dangerously-skip-permissions",
		"--model",
		"claude-3-5-sonnet",
		"--system-prompt",
		"you are helpful",
		"do something",
	}
	for _, want := range checks {
		if !strings.Contains(cmd, want) {
			t.Errorf("command %q does not contain %q", cmd, want)
		}
	}
}

func TestTmuxRunnerParsesResultFromLogFile(t *testing.T) {
	// Write a fake log file with a stream-json result event.
	dir := t.TempDir()
	logFile := filepath.Join(dir, "run.log")

	logContent := `{"type":"message_start","message":{}}
{"type":"content_block_start"}
{"type":"result","result":"hello from claude","stop_reason":"end_turn"}
`
	if err := os.WriteFile(logFile, []byte(logContent), 0o600); err != nil {
		t.Fatalf("writing log file: %v", err)
	}

	fake := session.NewFakeManager()
	runner := &TmuxRunner{Sessions: fake}

	opts := RunOpts{
		Prompt:           "test",
		WindowName:       "log-test",
		ApartmentSession: "retinue",
		WorkDir:          "/tmp",
		LogFile:          logFile,
	}

	result, err := runner.Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Output != "hello from claude" {
		t.Errorf("Output = %q, want %q", result.Output, "hello from claude")
	}
}

func TestTmuxRunnerNoLogFileReturnsEmptyOutput(t *testing.T) {
	fake := session.NewFakeManager()
	runner := &TmuxRunner{Sessions: fake}

	opts := RunOpts{
		Prompt:           "test",
		WindowName:       "no-log",
		ApartmentSession: "retinue",
		WorkDir:          "/tmp",
	}

	result, err := runner.Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Output != "" {
		t.Errorf("Output = %q, want empty string", result.Output)
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
}

func TestTmuxRunnerWaitForIncludesSocketFlag(t *testing.T) {
	fake := session.NewFakeManager()
	runner := &TmuxRunner{Sessions: fake}

	opts := RunOpts{
		Prompt:           "test",
		WindowName:       "sock-test",
		ApartmentSession: "retinue",
		WorkDir:          "/tmp",
		Socket:           "mysocket",
	}

	_, err := runner.Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cmd := fake.WindowCommand("retinue", "sock-test")
	if !strings.Contains(cmd, "tmux -L 'mysocket' wait-for -S sock-test") {
		t.Errorf("expected command to contain 'tmux -L 'mysocket' wait-for -S sock-test', got: %s", cmd)
	}
}

func TestTmuxRunnerWaitForOmitsSocketFlagWhenEmpty(t *testing.T) {
	fake := session.NewFakeManager()
	runner := &TmuxRunner{Sessions: fake}

	opts := RunOpts{
		Prompt:           "test",
		WindowName:       "nosock-test",
		ApartmentSession: "retinue",
		WorkDir:          "/tmp",
	}

	_, err := runner.Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cmd := fake.WindowCommand("retinue", "nosock-test")
	if !strings.Contains(cmd, "tmux wait-for -S nosock-test") {
		t.Errorf("expected command to contain 'tmux wait-for -S nosock-test', got: %s", cmd)
	}
	if strings.Contains(cmd, "-L") {
		t.Errorf("expected command to NOT contain '-L' when socket is empty, got: %s", cmd)
	}
}

func TestTmuxRunnerLogFileCommandUsesTee(t *testing.T) {
	fake := session.NewFakeManager()
	runner := &TmuxRunner{Sessions: fake}

	opts := RunOpts{
		Prompt:           "test",
		WindowName:       "tee-test",
		ApartmentSession: "retinue",
		WorkDir:          "/tmp",
		LogFile:          "/var/log/run.log",
	}

	_, err := runner.Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cmd := fake.WindowCommand("retinue", "tee-test")
	if !strings.Contains(cmd, "tee") {
		t.Errorf("expected command to contain 'tee', got: %s", cmd)
	}
	if !strings.Contains(cmd, "/var/log/run.log") {
		t.Errorf("expected command to contain log file path, got: %s", cmd)
	}
	if !strings.Contains(cmd, "tmux wait-for -S tee-test") {
		t.Errorf("expected command to contain tmux wait-for signal, got: %s", cmd)
	}
}
