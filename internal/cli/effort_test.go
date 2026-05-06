package cli

import (
	"strings"
	"testing"

	"github.com/wolandomny/retinue/internal/standing"
	"github.com/wolandomny/retinue/internal/task"
	"github.com/wolandomny/retinue/internal/workspace"
)

// --- Task / dispatch effort plumbing ---

func TestResolveTaskEffort_TaskOverridesWorkspace(t *testing.T) {
	ws := &workspace.Workspace{Config: workspace.Config{Effort: "low"}}
	tk := &task.Task{Effort: "high"}

	got := resolveTaskEffort(tk, ws)
	if got != "high" {
		t.Errorf("resolveTaskEffort = %q, want %q (task should override workspace)", got, "high")
	}
}

func TestResolveTaskEffort_FallsBackToWorkspace(t *testing.T) {
	ws := &workspace.Workspace{Config: workspace.Config{Effort: "max"}}
	tk := &task.Task{Effort: ""}

	got := resolveTaskEffort(tk, ws)
	if got != "max" {
		t.Errorf("resolveTaskEffort = %q, want %q (should fall back to workspace effort)", got, "max")
	}
}

func TestResolveTaskEffort_NeitherSet(t *testing.T) {
	ws := &workspace.Workspace{Config: workspace.Config{}}
	tk := &task.Task{}

	got := resolveTaskEffort(tk, ws)
	if got != "" {
		t.Errorf("resolveTaskEffort = %q, want empty string (no --effort flag)", got)
	}
}

// --- Standing agent effort plumbing ---

func TestResolveAgentEffort_AgentOverridesWorkspace(t *testing.T) {
	ws := &workspace.Workspace{Config: workspace.Config{Effort: "low"}}
	ag := &standing.Agent{Effort: "xhigh"}

	got := resolveAgentEffort(ag, ws)
	if got != "xhigh" {
		t.Errorf("resolveAgentEffort = %q, want %q", got, "xhigh")
	}
}

func TestResolveAgentEffort_FallsBackToWorkspace(t *testing.T) {
	ws := &workspace.Workspace{Config: workspace.Config{Effort: "medium"}}
	ag := &standing.Agent{Effort: ""}

	got := resolveAgentEffort(ag, ws)
	if got != "medium" {
		t.Errorf("resolveAgentEffort = %q, want %q (should fall back to workspace)", got, "medium")
	}
}

func TestResolveAgentEffort_NeitherSet(t *testing.T) {
	ws := &workspace.Workspace{Config: workspace.Config{}}
	ag := &standing.Agent{}

	got := resolveAgentEffort(ag, ws)
	if got != "" {
		t.Errorf("resolveAgentEffort = %q, want empty string", got)
	}
}

// --- Standing agent claude-args building ---

func TestBuildAgentClaudeArgs_WithEffort(t *testing.T) {
	args := buildAgentClaudeArgs("system prompt", "claude-opus-4-7", "high")

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--effort high") {
		t.Errorf("expected '--effort high' in args, got: %v", args)
	}
	if !strings.Contains(joined, "--model claude-opus-4-7") {
		t.Errorf("expected '--model claude-opus-4-7' in args, got: %v", args)
	}
}

func TestBuildAgentClaudeArgs_WithoutEffort(t *testing.T) {
	args := buildAgentClaudeArgs("system prompt", "claude-opus-4-7", "")

	for _, a := range args {
		if a == "--effort" {
			t.Errorf("--effort should not be present when effort is empty, got args: %v", args)
		}
	}
}

func TestBuildAgentClaudeArgs_WithoutModel(t *testing.T) {
	args := buildAgentClaudeArgs("system prompt", "", "")
	for _, a := range args {
		if a == "--model" || a == "--effort" {
			t.Errorf("--model/--effort should not be present, got args: %v", args)
		}
	}
}

// --- Woland claude-args building ---

func TestBuildWolandClaudeArgs_PassesWorkspaceEffort(t *testing.T) {
	args := buildWolandClaudeArgs("you are woland", "claude-opus-4-7", "max")

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--effort max") {
		t.Errorf("expected '--effort max' in args, got: %v", args)
	}
}

func TestBuildWolandClaudeArgs_NoEffort(t *testing.T) {
	args := buildWolandClaudeArgs("you are woland", "claude-opus-4-7", "")

	for _, a := range args {
		if a == "--effort" {
			t.Errorf("--effort should not be present when effort is empty, got args: %v", args)
		}
	}
}

func TestBuildWolandClaudeArgs_NoModelOrEffort(t *testing.T) {
	args := buildWolandClaudeArgs("system prompt", "", "")

	// Must always include --dangerously-skip-permissions and --system-prompt.
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--dangerously-skip-permissions") {
		t.Errorf("expected --dangerously-skip-permissions, got: %v", args)
	}
	if !strings.Contains(joined, "--system-prompt") {
		t.Errorf("expected --system-prompt, got: %v", args)
	}
}
