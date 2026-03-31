package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wolandomny/retinue/internal/standing"
	"github.com/wolandomny/retinue/internal/workspace"
)

// setupAgentWorkspace creates a temp workspace with an optional agents.yaml
// and returns the workspace path. Caller must set workspaceFlag before running
// commands that call loadWorkspace().
func setupAgentWorkspace(t *testing.T, agents []standing.Agent) string {
	t.Helper()
	dir := t.TempDir()

	// Write retinue.yaml
	ws := &workspace.Workspace{
		Path: dir,
		Config: workspace.Config{
			Name:  "test-apt",
			Model: "claude-opus-4-6",
			Repos: map[string]workspace.RepoConfig{
				"myrepo": {Path: "repos/myrepo"},
			},
		},
	}
	if err := ws.SaveConfig(); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	// Write agents.yaml if agents are provided
	if agents != nil {
		store := standing.NewFileStore(filepath.Join(dir, workspace.AgentsFile))
		if err := store.Save(agents); err != nil {
			t.Fatalf("saving agents: %v", err)
		}
	}

	return dir
}

func TestAgentListNoAgentsFile(t *testing.T) {
	dir := setupAgentWorkspace(t, nil)

	oldFlag := workspaceFlag
	workspaceFlag = dir
	defer func() { workspaceFlag = oldFlag }()

	cmd := newAgentListCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "No agents defined") {
		t.Errorf("expected 'No agents defined' message, got: %s", output)
	}
}

func TestAgentListEmptyAgents(t *testing.T) {
	dir := setupAgentWorkspace(t, []standing.Agent{})

	oldFlag := workspaceFlag
	workspaceFlag = dir
	defer func() { workspaceFlag = oldFlag }()

	cmd := newAgentListCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "No agents defined") {
		t.Errorf("expected 'No agents defined' message, got: %s", output)
	}
}

func TestAgentListWithAgents(t *testing.T) {
	agents := []standing.Agent{
		{
			ID:      "azazello",
			Name:    "Azazello",
			Role:    "CI Watcher",
			Prompt:  "Watch CI and report failures.",
			Enabled: true,
		},
		{
			ID:      "behemoth",
			Name:    "Behemoth",
			Role:    "Codebase Gardener",
			Prompt:  "Keep the codebase clean.",
			Enabled: false,
		},
	}
	dir := setupAgentWorkspace(t, agents)

	oldFlag := workspaceFlag
	workspaceFlag = dir
	defer func() { workspaceFlag = oldFlag }()

	cmd := newAgentListCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()

	// Check header
	if !strings.Contains(output, "ID") || !strings.Contains(output, "NAME") ||
		!strings.Contains(output, "ROLE") || !strings.Contains(output, "STATUS") {
		t.Errorf("missing table headers, got:\n%s", output)
	}

	// Check agent rows
	if !strings.Contains(output, "azazello") {
		t.Errorf("missing azazello in output:\n%s", output)
	}
	if !strings.Contains(output, "Azazello") {
		t.Errorf("missing Azazello name in output:\n%s", output)
	}
	if !strings.Contains(output, "CI Watcher") {
		t.Errorf("missing CI Watcher role in output:\n%s", output)
	}
	if !strings.Contains(output, "behemoth") {
		t.Errorf("missing behemoth in output:\n%s", output)
	}

	// Behemoth should show disabled since enabled is false
	// Split lines and check the behemoth line specifically
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.Contains(line, "behemoth") {
			if !strings.Contains(line, "disabled") {
				t.Errorf("behemoth should show 'disabled', got: %s", line)
			}
		}
	}

	// Azazello is enabled but tmux isn't running, so it should show stopped
	for _, line := range lines {
		if strings.Contains(line, "azazello") {
			if !strings.Contains(line, "stopped") {
				t.Errorf("azazello should show 'stopped' (no tmux), got: %s", line)
			}
		}
	}
}

func TestAgentStartNonexistent(t *testing.T) {
	agents := []standing.Agent{
		{
			ID:      "azazello",
			Name:    "Azazello",
			Role:    "CI Watcher",
			Prompt:  "Watch CI.",
			Enabled: true,
		},
	}
	dir := setupAgentWorkspace(t, agents)

	oldFlag := workspaceFlag
	workspaceFlag = dir
	defer func() { workspaceFlag = oldFlag }()

	cmd := newAgentStartCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"nonexistent"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for nonexistent agent")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got: %v", err)
	}
}

func TestAgentStartDisabled(t *testing.T) {
	agents := []standing.Agent{
		{
			ID:      "behemoth",
			Name:    "Behemoth",
			Role:    "Codebase Gardener",
			Prompt:  "Keep codebase clean.",
			Enabled: false,
		},
	}
	dir := setupAgentWorkspace(t, agents)

	oldFlag := workspaceFlag
	workspaceFlag = dir
	defer func() { workspaceFlag = oldFlag }()

	cmd := newAgentStartCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"behemoth"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for disabled agent")
	}
	if !strings.Contains(err.Error(), "disabled") {
		t.Errorf("expected 'disabled' in error, got: %v", err)
	}
}

func TestAgentStopNotRunning(t *testing.T) {
	agents := []standing.Agent{
		{
			ID:      "azazello",
			Name:    "Azazello",
			Role:    "CI Watcher",
			Prompt:  "Watch CI.",
			Enabled: true,
		},
	}
	dir := setupAgentWorkspace(t, agents)

	oldFlag := workspaceFlag
	workspaceFlag = dir
	defer func() { workspaceFlag = oldFlag }()

	cmd := newAgentStopCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"azazello"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for non-running agent")
	}
	if !strings.Contains(err.Error(), "not running") {
		t.Errorf("expected 'not running' in error, got: %v", err)
	}
}

func TestAgentStopNonexistent(t *testing.T) {
	agents := []standing.Agent{
		{
			ID:      "azazello",
			Name:    "Azazello",
			Role:    "CI Watcher",
			Prompt:  "Watch CI.",
			Enabled: true,
		},
	}
	dir := setupAgentWorkspace(t, agents)

	oldFlag := workspaceFlag
	workspaceFlag = dir
	defer func() { workspaceFlag = oldFlag }()

	cmd := newAgentStopCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"nonexistent"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for nonexistent agent")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got: %v", err)
	}
}

func TestAgentWindowName(t *testing.T) {
	tests := []struct {
		id   string
		want string
	}{
		{"azazello", "agent-azazello"},
		{"behemoth", "agent-behemoth"},
		{"ci-watcher", "agent-ci-watcher"},
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			got := agentWindowName(tt.id)
			if got != tt.want {
				t.Errorf("agentWindowName(%q) = %q, want %q", tt.id, got, tt.want)
			}
		})
	}
}

func TestBuildAgentSystemPrompt(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "repos/myrepo"), 0o755)

	ws := &workspace.Workspace{
		Path: dir,
		Config: workspace.Config{
			Name:  "test-apt",
			Model: "claude-opus-4-6",
			Repos: map[string]workspace.RepoConfig{
				"myrepo": {Path: "repos/myrepo"},
			},
		},
	}

	agent := &standing.Agent{
		ID:     "azazello",
		Name:   "Azazello",
		Role:   "CI Watcher",
		Repos:  []string{"myrepo"},
		Prompt: "Watch CI pipelines and report failures immediately.",
	}

	prompt := buildAgentSystemPrompt(ws, agent)

	checks := []struct {
		name     string
		contains string
	}{
		{"agent name", "Azazello"},
		{"role", "CI Watcher"},
		{"mandate", "Watch CI pipelines and report failures immediately."},
		{"repo name", "myrepo"},
		{"repo path", filepath.Join(dir, "repos/myrepo")},
		{"standing agent context", "standing agent"},
		{"apartment path", dir},
	}

	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if !strings.Contains(prompt, c.contains) {
				t.Errorf("prompt missing %q (%s)\nprompt:\n%s", c.contains, c.name, prompt)
			}
		})
	}
}

func TestBuildAgentSystemPromptNoRepos(t *testing.T) {
	ws := &workspace.Workspace{
		Path: "/tmp/test",
		Config: workspace.Config{
			Name: "test-apt",
		},
	}

	agent := &standing.Agent{
		ID:     "simple",
		Name:   "Simple",
		Role:   "Helper",
		Prompt: "Help out.",
	}

	prompt := buildAgentSystemPrompt(ws, agent)

	if strings.Contains(prompt, "Repositories") {
		t.Errorf("prompt should not have Repositories section when agent has no repos")
	}
}

func TestAgentCmdRegistered(t *testing.T) {
	root := newRootCmd()
	found := false
	for _, sub := range root.Commands() {
		if sub.Name() == "agent" {
			found = true
			// Check subcommands exist
			subNames := map[string]bool{}
			for _, s := range sub.Commands() {
				subNames[s.Name()] = true
			}
			for _, want := range []string{"list", "start", "stop"} {
				if !subNames[want] {
					t.Errorf("agent subcommand %q not found", want)
				}
			}
			break
		}
	}
	if !found {
		t.Error("'agent' command not found under root")
	}
}
