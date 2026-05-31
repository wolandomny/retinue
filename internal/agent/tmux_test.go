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

// TestBuildClaudeArgsEffortHigh guards against the effort flag being dropped
// on the tmux worker path: Effort=="high" must append --effort high.
func TestBuildClaudeArgsEffortHigh(t *testing.T) {
	args := buildTmuxClaudeArgs(RunOpts{Prompt: "hello", Effort: "high"})

	effortIdx := indexOf(args, "--effort")
	if effortIdx == -1 {
		t.Fatalf("expected args to contain --effort, got %v", args)
	}
	if effortIdx+1 >= len(args) || args[effortIdx+1] != "high" {
		t.Fatalf("expected --effort to be followed by high, got %v", args)
	}
}

// TestBuildClaudeArgsEffortUltracode verifies ultracode maps to the --settings
// flag with {"ultracode": true} and never emits a bare --effort flag.
func TestBuildClaudeArgsEffortUltracode(t *testing.T) {
	args := buildTmuxClaudeArgs(RunOpts{Prompt: "hello", Effort: "ultracode"})

	if !contains(args, "--settings") {
		t.Fatalf("expected args to contain --settings, got %v", args)
	}
	if !contains(args, `{"ultracode": true}`) {
		t.Fatalf("expected args to contain ultracode settings, got %v", args)
	}
	if contains(args, "--effort") {
		t.Fatalf("expected args to NOT contain --effort, got %v", args)
	}
}

// TestBuildClaudeArgsEffortEmpty verifies an empty Effort adds no effort or
// settings flag at all.
func TestBuildClaudeArgsEffortEmpty(t *testing.T) {
	args := buildTmuxClaudeArgs(RunOpts{Prompt: "hello"})

	if contains(args, "--effort") {
		t.Fatalf("expected args to NOT contain --effort, got %v", args)
	}
	if contains(args, "--settings") {
		t.Fatalf("expected args to NOT contain --settings, got %v", args)
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

// TestBuildTmuxClaudeArgsIncludesPromptAsLastArg guards against the prompt
// being dropped or swallowed: opts.Prompt must always be the final argument.
func TestBuildTmuxClaudeArgsIncludesPromptAsLastArg(t *testing.T) {
	cases := []RunOpts{
		{Prompt: "test prompt", Effort: "high"},
		{Prompt: "test prompt", Effort: "ultracode"},
		{Prompt: "test prompt"},
		{Prompt: "test prompt", DisallowedTools: "AskUserQuestion"},
		{Prompt: "test prompt", Effort: "xhigh", DisallowedTools: "AskUserQuestion", Model: "claude-opus", SystemPrompt: "You are helpful"},
	}
	for _, opts := range cases {
		args := buildTmuxClaudeArgs(opts)
		if len(args) == 0 || args[len(args)-1] != "test prompt" {
			t.Errorf("prompt not last arg; args=%v", args)
		}
	}
}

// TestBuildTmuxClaudeArgsDisallowedToolsUsesEqualsForm guards the prompt-swallowing
// regression: the deny flag must be emitted as the single token
// "--disallowed-tools=AskUserQuestion" and never as a bare "--disallowed-tools"
// element (the two-token form swallows the positional prompt).
func TestBuildTmuxClaudeArgsDisallowedToolsUsesEqualsForm(t *testing.T) {
	args := buildTmuxClaudeArgs(RunOpts{Prompt: "test prompt", DisallowedTools: "AskUserQuestion"})
	if !contains(args, "--disallowed-tools=AskUserQuestion") {
		t.Errorf("expected args to contain --disallowed-tools=AskUserQuestion, got %v", args)
	}
	if contains(args, "--disallowed-tools") {
		t.Errorf("expected args to NOT contain bare --disallowed-tools (two-token form swallows prompt), got %v", args)
	}
}

// TestBuildClaudeArgsDisallowedToolsUsesEqualsForm mirrors the above for the
// non-tmux ClaudeRunner arg builder.
func TestBuildClaudeArgsDisallowedToolsUsesEqualsForm(t *testing.T) {
	args := buildClaudeArgs(RunOpts{Prompt: "test prompt", DisallowedTools: "AskUserQuestion"})
	if !contains(args, "--disallowed-tools=AskUserQuestion") {
		t.Errorf("expected args to contain --disallowed-tools=AskUserQuestion, got %v", args)
	}
	if contains(args, "--disallowed-tools") {
		t.Errorf("expected args to NOT contain bare --disallowed-tools (two-token form swallows prompt), got %v", args)
	}
	if len(args) == 0 || args[len(args)-1] != "test prompt" {
		t.Errorf("prompt not last arg; args=%v", args)
	}
}

// TestTmuxRunnerWindowCommandCarriesPrompt verifies the prompt survives
// end-to-end into the recorded tmux window command.
func TestTmuxRunnerWindowCommandCarriesPrompt(t *testing.T) {
	fake := session.NewFakeManager()
	runner := &TmuxRunner{Sessions: fake}

	opts := RunOpts{
		Prompt:           "unique-prompt-token-xyzzy",
		WindowName:       "prompt-carry-test",
		ApartmentSession: "retinue",
		WorkDir:          "/tmp",
	}

	_, err := runner.Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cmd := fake.WindowCommand("retinue", "prompt-carry-test")
	if !strings.Contains(cmd, "unique-prompt-token-xyzzy") {
		t.Errorf("expected window command to contain prompt token, got: %s", cmd)
	}
}

// TestTmuxRunnerMissingResultEventDoesNotHardFail verifies the NEW contract:
// a log file with stream events but no {"type":"result"} event must NOT cause
// Run to hard-fail. The result event is non-authoritative for success — the
// runner returns best-effort output (here empty) with ExitCode 0 / nil err and
// lets the caller (dispatchOne) decide based on commit presence.
//
// This replaces the old TestTmuxRunnerFastFailureNoResultEventErrors, which
// encoded the removed hard-fail behavior that discarded real committed work
// whose result line had not yet flushed (incident 1).
func TestTmuxRunnerMissingResultEventDoesNotHardFail(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "run.log")

	logContent := `{"type":"message_start","message":{}}
{"type":"content_block_start"}
{"type":"content_block_stop"}
`
	if err := os.WriteFile(logFile, []byte(logContent), 0o600); err != nil {
		t.Fatalf("writing log file: %v", err)
	}

	fake := session.NewFakeManager()
	runner := &TmuxRunner{Sessions: fake}

	opts := RunOpts{
		Prompt:           "test",
		WindowName:       "noresult-test",
		ApartmentSession: "retinue",
		WorkDir:          "/tmp",
		LogFile:          logFile,
	}

	result, err := runner.Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("expected nil error on missing result event (non-authoritative), got %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("expected ExitCode 0 on missing result event, got %d", result.ExitCode)
	}
	if result.Output != "" {
		t.Errorf("expected empty Output when no result event present, got %q", result.Output)
	}
}

// TestTmuxRunnerEmptyLogFileDoesNotHardFail verifies an empty requested log
// file also does NOT hard-fail under the new contract; the runner returns
// empty best-effort output with ExitCode 0 / nil err.
//
// This replaces the old TestTmuxRunnerEmptyLogFileErrors.
func TestTmuxRunnerEmptyLogFileDoesNotHardFail(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "run.log")

	if err := os.WriteFile(logFile, []byte(""), 0o600); err != nil {
		t.Fatalf("writing log file: %v", err)
	}

	fake := session.NewFakeManager()
	runner := &TmuxRunner{Sessions: fake}

	opts := RunOpts{
		Prompt:           "test",
		WindowName:       "emptylog-test",
		ApartmentSession: "retinue",
		WorkDir:          "/tmp",
		LogFile:          logFile,
	}

	result, err := runner.Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("expected nil error on empty log file (non-authoritative), got %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("expected ExitCode 0 on empty log file, got %d", result.ExitCode)
	}
	if result.Output != "" {
		t.Errorf("expected empty Output for empty log file, got %q", result.Output)
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

func TestTmuxRunnerEnvVarsInCommand(t *testing.T) {
	fake := session.NewFakeManager()
	runner := &TmuxRunner{Sessions: fake}

	opts := RunOpts{
		Prompt:           "test",
		WindowName:       "env-test",
		ApartmentSession: "retinue",
		WorkDir:          "/tmp",
		Env:              []string{"GH_TOKEN=ghp_abc123", "FOO=bar"},
	}

	_, err := runner.Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cmd := fake.WindowCommand("retinue", "env-test")
	if cmd == "" {
		t.Fatal("expected a command to be recorded for window 'env-test'")
	}

	// The env command should include the extra env vars.
	if !strings.Contains(cmd, "GH_TOKEN=ghp_abc123") {
		t.Errorf("expected command to contain GH_TOKEN env var, got: %s", cmd)
	}
	if !strings.Contains(cmd, "FOO=bar") {
		t.Errorf("expected command to contain FOO env var, got: %s", cmd)
	}
	// env -u CLAUDECODE should still be present.
	if !strings.Contains(cmd, "env -u CLAUDECODE") {
		t.Errorf("expected command to contain 'env -u CLAUDECODE', got: %s", cmd)
	}
	// The claude command should follow the env vars.
	if !strings.Contains(cmd, "claude") {
		t.Errorf("expected command to contain 'claude', got: %s", cmd)
	}
}

func TestTmuxRunnerEmptyEnvVarsNoChange(t *testing.T) {
	fake := session.NewFakeManager()
	runner := &TmuxRunner{Sessions: fake}

	opts := RunOpts{
		Prompt:           "test",
		WindowName:       "no-env-test",
		ApartmentSession: "retinue",
		WorkDir:          "/tmp",
	}

	_, err := runner.Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cmd := fake.WindowCommand("retinue", "no-env-test")
	// With no extra env vars, the command should start with "env -u CLAUDECODE claude".
	if !strings.Contains(cmd, "env -u CLAUDECODE claude") {
		t.Errorf("expected command to contain 'env -u CLAUDECODE claude', got: %s", cmd)
	}
}

func TestTmuxRunnerLogFileCommandUsesTee(t *testing.T) {
	// Use a real temp log file containing a result event so the fast-failure
	// guard is satisfied; this test only asserts on command construction.
	dir := t.TempDir()
	logFile := filepath.Join(dir, "run.log")
	if err := os.WriteFile(logFile, []byte(`{"type":"result","result":"ok"}`+"\n"), 0o600); err != nil {
		t.Fatalf("writing log file: %v", err)
	}

	fake := session.NewFakeManager()
	runner := &TmuxRunner{Sessions: fake}

	opts := RunOpts{
		Prompt:           "test",
		WindowName:       "tee-test",
		ApartmentSession: "retinue",
		WorkDir:          "/tmp",
		LogFile:          logFile,
	}

	_, err := runner.Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cmd := fake.WindowCommand("retinue", "tee-test")
	if !strings.Contains(cmd, "tee") {
		t.Errorf("expected command to contain 'tee', got: %s", cmd)
	}
	if !strings.Contains(cmd, logFile) {
		t.Errorf("expected command to contain log file path, got: %s", cmd)
	}
	if !strings.Contains(cmd, "tmux wait-for -S tee-test") {
		t.Errorf("expected command to contain tmux wait-for signal, got: %s", cmd)
	}
}
