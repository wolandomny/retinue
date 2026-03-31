package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wolandomny/retinue/internal/bus"
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

func TestBuildAgentSystemPromptWithBusHistory(t *testing.T) {
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
		Prompt: "Watch CI pipelines.",
	}

	// Write messages to the bus file so buildAgentSystemPrompt includes them.
	busInstance := bus.New(ws.BusPath())
	msgs := []*bus.Message{
		bus.NewMessage("user", bus.TypeUser, "Please check CI"),
		bus.NewMessage("behemoth", bus.TypeChat, "All clear on my end"),
	}
	for _, msg := range msgs {
		if err := busInstance.Append(msg); err != nil {
			t.Fatalf("Failed to append message: %v", err)
		}
	}

	prompt := buildAgentSystemPrompt(ws, agent)

	if !strings.Contains(prompt, "Recent Group Chat History") {
		t.Error("prompt should include 'Recent Group Chat History' section when bus has messages")
	}
	if !strings.Contains(prompt, "Please check CI") {
		t.Error("prompt should include bus message text 'Please check CI'")
	}
	if !strings.Contains(prompt, "All clear on my end") {
		t.Error("prompt should include bus message text 'All clear on my end'")
	}
}

func TestBuildAgentSystemPromptNoBusFile(t *testing.T) {
	dir := t.TempDir()

	ws := &workspace.Workspace{
		Path: dir,
		Config: workspace.Config{
			Name:  "test-apt",
			Model: "claude-opus-4-6",
		},
	}

	agent := &standing.Agent{
		ID:     "azazello",
		Name:   "Azazello",
		Role:   "CI Watcher",
		Prompt: "Watch CI pipelines.",
	}

	// No bus file exists — buildAgentSystemPrompt should still work.
	prompt := buildAgentSystemPrompt(ws, agent)

	if strings.Contains(prompt, "Recent Group Chat History") {
		t.Error("prompt should not include 'Recent Group Chat History' when no bus file exists")
	}
	// But it should still have the basic sections.
	if !strings.Contains(prompt, "Azazello") {
		t.Error("prompt should contain agent name")
	}
	if !strings.Contains(prompt, "CI Watcher") {
		t.Error("prompt should contain agent role")
	}
}

func TestAgentModelOverride(t *testing.T) {
	// Test that agent model field takes precedence over workspace model.
	agentWithModel := &standing.Agent{
		ID:      "azazello",
		Name:    "Azazello",
		Role:    "CI Watcher",
		Prompt:  "Watch CI.",
		Model:   "claude-sonnet-4-20250514",
		Enabled: true,
	}
	agentNoModel := &standing.Agent{
		ID:      "behemoth",
		Name:    "Behemoth",
		Role:    "Codebase Gardener",
		Prompt:  "Keep it clean.",
		Enabled: true,
	}

	wsModel := "claude-opus-4-6"

	// When agent has a model, it should be used.
	model := agentWithModel.Model
	if model == "" {
		model = wsModel
	}
	if model != "claude-sonnet-4-20250514" {
		t.Errorf("expected agent model 'claude-sonnet-4-20250514', got %q", model)
	}

	// When agent has no model, workspace model should be used.
	model = agentNoModel.Model
	if model == "" {
		model = wsModel
	}
	if model != wsModel {
		t.Errorf("expected workspace model %q, got %q", wsModel, model)
	}
}

func TestAgentListStatusColumns(t *testing.T) {
	agents := []standing.Agent{
		{
			ID:      "azazello",
			Name:    "Azazello",
			Role:    "CI Watcher",
			Prompt:  "Watch CI.",
			Enabled: true,
		},
		{
			ID:      "behemoth",
			Name:    "Behemoth",
			Role:    "Codebase Gardener",
			Prompt:  "Keep it clean.",
			Enabled: false,
		},
		{
			ID:      "koroviev",
			Name:    "Koroviev",
			Role:    "PR Reviewer",
			Prompt:  "Review PRs.",
			Enabled: true,
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

	// Verify all four column headers are present.
	for _, header := range []string{"ID", "NAME", "ROLE", "STATUS"} {
		if !strings.Contains(output, header) {
			t.Errorf("missing table header %q in output:\n%s", header, output)
		}
	}

	// Verify all agents appear.
	for _, id := range []string{"azazello", "behemoth", "koroviev"} {
		if !strings.Contains(output, id) {
			t.Errorf("missing agent %q in output:\n%s", id, output)
		}
	}

	// Verify status values for enabled and disabled agents.
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.Contains(line, "behemoth") {
			if !strings.Contains(line, "disabled") {
				t.Errorf("behemoth should show 'disabled', got: %s", line)
			}
		}
		// Enabled agents without tmux should show "stopped".
		if strings.Contains(line, "azazello") {
			if !strings.Contains(line, "stopped") {
				t.Errorf("azazello should show 'stopped', got: %s", line)
			}
		}
		if strings.Contains(line, "koroviev") {
			if !strings.Contains(line, "stopped") {
				t.Errorf("koroviev should show 'stopped', got: %s", line)
			}
		}
	}
}

func TestAgentListOutputFormat(t *testing.T) {
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

	cmd := newAgentListCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")

	// Expect at least 3 lines: header, separator, and one agent row.
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines (header, separator, data), got %d:\n%s", len(lines), output)
	}

	// Header line should have all columns.
	header := lines[0]
	for _, col := range []string{"ID", "NAME", "ROLE", "STATUS"} {
		if !strings.Contains(header, col) {
			t.Errorf("header missing column %q: %q", col, header)
		}
	}

	// Separator line should have dashes.
	if !strings.Contains(lines[1], "--") {
		t.Errorf("expected separator line with dashes, got: %q", lines[1])
	}

	// Data line should have the agent info.
	dataLine := lines[2]
	if !strings.Contains(dataLine, "azazello") || !strings.Contains(dataLine, "Azazello") {
		t.Errorf("data line should contain agent info, got: %q", dataLine)
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
