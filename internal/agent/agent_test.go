package agent

import (
	"context"
	"testing"
)

// FakeRunner is a test double for Runner.
type FakeRunner struct {
	RunFunc func(ctx context.Context, opts RunOpts) (Result, error)
	Calls   []RunOpts
}

func (f *FakeRunner) Run(ctx context.Context, opts RunOpts) (Result, error) {
	f.Calls = append(f.Calls, opts)
	if f.RunFunc != nil {
		return f.RunFunc(ctx, opts)
	}
	return Result{Output: "fake output", ExitCode: 0}, nil
}

func TestFakeRunnerRecordsCalls(t *testing.T) {
	fake := &FakeRunner{}

	opts := RunOpts{
		Prompt:  "test prompt",
		Model:   "test-model",
		WorkDir: "/tmp",
	}

	result, err := fake.Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "fake output" {
		t.Errorf("output = %q, want %q", result.Output, "fake output")
	}
	if len(fake.Calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(fake.Calls))
	}
	if fake.Calls[0].Prompt != "test prompt" {
		t.Errorf("call prompt = %q, want %q", fake.Calls[0].Prompt, "test prompt")
	}
}

func TestFakeRunnerRecordsEnv(t *testing.T) {
	fake := &FakeRunner{}

	opts := RunOpts{
		Prompt: "test prompt",
		Env:    []string{"GH_TOKEN=ghp_abc123", "FOO=bar"},
	}

	_, err := fake.Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.Calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(fake.Calls))
	}
	if len(fake.Calls[0].Env) != 2 {
		t.Fatalf("expected 2 env vars, got %d", len(fake.Calls[0].Env))
	}
	if fake.Calls[0].Env[0] != "GH_TOKEN=ghp_abc123" {
		t.Errorf("env[0] = %q, want %q", fake.Calls[0].Env[0], "GH_TOKEN=ghp_abc123")
	}
	if fake.Calls[0].Env[1] != "FOO=bar" {
		t.Errorf("env[1] = %q, want %q", fake.Calls[0].Env[1], "FOO=bar")
	}
}

func TestClaudeRunnerBuildsArgs(t *testing.T) {
	// We can't run the actual claude CLI in tests, but we can verify
	// the runner struct exists and implements the interface.
	var _ Runner = &ClaudeRunner{}
}
