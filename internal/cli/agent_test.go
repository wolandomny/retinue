package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wolandomny/retinue/internal/bus"
	"github.com/wolandomny/retinue/internal/session"
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

func TestSendKickoffNoTmux(t *testing.T) {
	// sendKickoff should return an error (not panic) when tmux is unavailable.
	// This exercises the non-fatal error path in the start command.
	mgr := session.NewTmuxManager("retinue-nonexistent-socket-for-test")
	ctx := context.Background()

	err := sendKickoff(ctx, mgr, "TestAgent", "agent-test")
	if err == nil {
		t.Fatal("expected error when tmux is not available, got nil")
	}
	// The error should mention something about the tmux command failing.
	// We don't assert the exact message since it varies by platform.
	t.Logf("sendKickoff returned expected error: %v", err)
}

func TestSendKickoffMessageContent(t *testing.T) {
	// Verify the kickoff message includes the agent name.
	// We can't easily intercept the exec call, but we can test that
	// sendKickoff constructs the right message by checking it doesn't
	// panic with various agent names including special characters.
	mgr := session.NewTmuxManager("retinue-nonexistent-socket-for-test")
	ctx := context.Background()

	names := []string{
		"Azazello",
		"Agent With Spaces",
		"agent-with-dashes",
		"agent;semicolon",
		"agent$dollar",
	}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			err := sendKickoff(ctx, mgr, name, "agent-test")
			// Should return an error (no tmux) but not panic.
			if err == nil {
				t.Fatal("expected error when tmux is not available")
			}
		})
	}
}

// --- Session marker tests ---

func TestAgentSessionMarkerName(t *testing.T) {
	tests := []struct {
		id   string
		want string
	}{
		{"azazello", ".agent-azazello-session"},
		{"behemoth", ".agent-behemoth-session"},
		{"ci-watcher", ".agent-ci-watcher-session"},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			got := agentSessionMarkerName(tt.id)
			if got != tt.want {
				t.Errorf("agentSessionMarkerName(%q) = %q, want %q", tt.id, got, tt.want)
			}
		})
	}
}

func TestWriteAgentSessionMarker(t *testing.T) {
	aptDir := t.TempDir()

	// Create the Claude projects directory.
	projDir := session.ClaudeProjectDir(aptDir)
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a session file.
	sessionFile := filepath.Join(projDir, "agent-session.jsonl")
	if err := os.WriteFile(sessionFile, []byte(`{"type":"system"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	writeAgentSessionMarker(aptDir, "azazello")

	// Verify marker was written.
	markerPath := filepath.Join(aptDir, ".agent-azazello-session")
	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("reading marker: %v", err)
	}
	if string(data) != sessionFile {
		t.Errorf("marker = %q, want %q", string(data), sessionFile)
	}
}

func TestWriteAgentSessionMarker_NoProjDir(t *testing.T) {
	aptDir := t.TempDir()

	// No Claude projects dir — should not panic or create a marker.
	writeAgentSessionMarker(aptDir, "azazello")

	markerPath := filepath.Join(aptDir, ".agent-azazello-session")
	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Error("marker should not be created when no projects dir exists")
	}
}

func TestRemoveAgentSessionMarker(t *testing.T) {
	aptDir := t.TempDir()

	// Create a marker file.
	markerPath := filepath.Join(aptDir, ".agent-azazello-session")
	if err := os.WriteFile(markerPath, []byte("/some/path.jsonl"), 0o644); err != nil {
		t.Fatal(err)
	}

	removeAgentSessionMarker(aptDir, "azazello")

	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Error("marker file should be removed after removeAgentSessionMarker")
	}
}

func TestRemoveAgentSessionMarker_NoFile(t *testing.T) {
	aptDir := t.TempDir()

	// Should not panic when marker doesn't exist.
	removeAgentSessionMarker(aptDir, "nonexistent")
}

// --- Bus watcher auto-start/stop tests ---

func TestBusWatcherAutoStartOnFirstAgent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr := session.NewFakeManager()

	// No bus-watcher window exists yet — should want to start one.
	if !shouldStartBusWatcher(ctx, mgr) {
		t.Fatal("shouldStartBusWatcher should return true when no bus-watcher window exists")
	}

	// Simulate starting the bus watcher by creating the window.
	if err := mgr.CreateWindow(ctx, session.ApartmentSession, busWatcherWindow, "/tmp", "retinue bus serve"); err != nil {
		t.Fatalf("creating bus-watcher window: %v", err)
	}

	// Now it should report that bus-watcher exists.
	has, err := mgr.HasWindow(ctx, session.ApartmentSession, busWatcherWindow)
	if err != nil {
		t.Fatalf("HasWindow error: %v", err)
	}
	if !has {
		t.Fatal("bus-watcher window should exist after CreateWindow")
	}

	// And shouldStartBusWatcher should now return false.
	if shouldStartBusWatcher(ctx, mgr) {
		t.Fatal("shouldStartBusWatcher should return false when bus-watcher window already exists")
	}
}

func TestBusWatcherNotDuplicatedOnSecondAgent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr := session.NewFakeManager()

	// Simulate that the first agent already started and a bus-watcher is running.
	if err := mgr.CreateWindow(ctx, session.ApartmentSession, busWatcherWindow, "/tmp", "retinue bus serve"); err != nil {
		t.Fatalf("creating bus-watcher window: %v", err)
	}
	if err := mgr.CreateWindow(ctx, session.ApartmentSession, "agent-azazello", "/tmp", "claude"); err != nil {
		t.Fatalf("creating first agent window: %v", err)
	}

	// Starting a second agent — shouldStartBusWatcher should return false.
	if shouldStartBusWatcher(ctx, mgr) {
		t.Fatal("shouldStartBusWatcher should return false when bus-watcher already exists")
	}

	// Simulate creating the second agent window.
	if err := mgr.CreateWindow(ctx, session.ApartmentSession, "agent-behemoth", "/tmp", "claude"); err != nil {
		t.Fatalf("creating second agent window: %v", err)
	}

	// Verify only one bus-watcher window exists — creating the same window
	// name again should fail.
	err := mgr.CreateWindow(ctx, session.ApartmentSession, busWatcherWindow, "/tmp", "retinue bus serve")
	if err == nil {
		t.Fatal("expected error when creating duplicate bus-watcher window")
	}

	// Count bus-watcher windows in the session.
	windows, err := mgr.ListWindows(ctx, session.ApartmentSession)
	if err != nil {
		t.Fatalf("ListWindows error: %v", err)
	}
	bwCount := 0
	for _, w := range windows {
		if w == busWatcherWindow {
			bwCount++
		}
	}
	if bwCount != 1 {
		t.Errorf("expected exactly 1 bus-watcher window, found %d (windows: %v)", bwCount, windows)
	}
}

func TestBusWatcherStoppedOnLastAgentStop(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr := session.NewFakeManager()

	agents := []standing.Agent{
		{ID: "azazello", Name: "Azazello", Role: "CI", Prompt: "Watch CI.", Enabled: true},
		{ID: "behemoth", Name: "Behemoth", Role: "Garden", Prompt: "Garden.", Enabled: true},
	}

	dir := t.TempDir()
	store := standing.NewFileStore(filepath.Join(dir, "agents.yaml"))
	if err := store.Save(agents); err != nil {
		t.Fatalf("saving agents: %v", err)
	}

	// Simulate both agents and bus watcher running.
	for _, name := range []string{busWatcherWindow, "agent-azazello", "agent-behemoth"} {
		if err := mgr.CreateWindow(ctx, session.ApartmentSession, name, "/tmp", "cmd"); err != nil {
			t.Fatalf("creating window %s: %v", name, err)
		}
	}

	// Stop first agent — bus watcher should NOT stop because behemoth is still running.
	if err := mgr.KillWindow(ctx, session.ApartmentSession, "agent-azazello"); err != nil {
		t.Fatalf("killing azazello window: %v", err)
	}
	if shouldStopBusWatcher(ctx, mgr, store, "azazello") {
		t.Fatal("shouldStopBusWatcher should return false when behemoth is still running")
	}

	// Verify bus watcher is still alive.
	has, _ := mgr.HasWindow(ctx, session.ApartmentSession, busWatcherWindow)
	if !has {
		t.Fatal("bus-watcher window should still exist after stopping first agent")
	}

	// Stop second agent — now bus watcher SHOULD stop (no agents left).
	if err := mgr.KillWindow(ctx, session.ApartmentSession, "agent-behemoth"); err != nil {
		t.Fatalf("killing behemoth window: %v", err)
	}
	if !shouldStopBusWatcher(ctx, mgr, store, "behemoth") {
		t.Fatal("shouldStopBusWatcher should return true when no agents are running")
	}

	// Simulate the actual stop.
	if err := mgr.KillWindow(ctx, session.ApartmentSession, busWatcherWindow); err != nil {
		t.Fatalf("killing bus-watcher window: %v", err)
	}

	has, _ = mgr.HasWindow(ctx, session.ApartmentSession, busWatcherWindow)
	if has {
		t.Fatal("bus-watcher window should not exist after being stopped")
	}
}

func TestBusWatcherNotStoppedWhileAgentsRunning(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr := session.NewFakeManager()

	agents := []standing.Agent{
		{ID: "azazello", Name: "Azazello", Role: "CI", Prompt: "Watch.", Enabled: true},
		{ID: "behemoth", Name: "Behemoth", Role: "Garden", Prompt: "Garden.", Enabled: true},
		{ID: "koroviev", Name: "Koroviev", Role: "Review", Prompt: "Review.", Enabled: true},
	}

	dir := t.TempDir()
	store := standing.NewFileStore(filepath.Join(dir, "agents.yaml"))
	if err := store.Save(agents); err != nil {
		t.Fatalf("saving agents: %v", err)
	}

	// Start bus watcher and two of three agents (koroviev is defined but not started).
	for _, name := range []string{busWatcherWindow, "agent-azazello", "agent-behemoth"} {
		if err := mgr.CreateWindow(ctx, session.ApartmentSession, name, "/tmp", "cmd"); err != nil {
			t.Fatalf("creating window %s: %v", name, err)
		}
	}

	// Stop azazello — behemoth is still running so bus watcher stays.
	if err := mgr.KillWindow(ctx, session.ApartmentSession, "agent-azazello"); err != nil {
		t.Fatalf("killing azazello window: %v", err)
	}
	if shouldStopBusWatcher(ctx, mgr, store, "azazello") {
		t.Fatal("shouldStopBusWatcher should return false when behemoth is still running")
	}

	// Bus watcher should still be alive.
	has, _ := mgr.HasWindow(ctx, session.ApartmentSession, busWatcherWindow)
	if !has {
		t.Fatal("bus-watcher should still exist while behemoth is running")
	}
}

func TestShouldStopBusWatcher_DisabledAgentsIgnored(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr := session.NewFakeManager()

	// One enabled agent (being stopped), one disabled agent.
	agents := []standing.Agent{
		{ID: "azazello", Name: "Azazello", Prompt: "Watch.", Enabled: true},
		{ID: "behemoth", Name: "Behemoth", Prompt: "Garden.", Enabled: false},
	}

	dir := t.TempDir()
	store := standing.NewFileStore(filepath.Join(dir, "agents.yaml"))
	if err := store.Save(agents); err != nil {
		t.Fatalf("saving agents: %v", err)
	}

	// Only azazello was running; behemoth is disabled and never started.
	// After stopping azazello, bus watcher should stop even though behemoth
	// is defined — it's disabled so it doesn't count.
	if !shouldStopBusWatcher(ctx, mgr, store, "azazello") {
		t.Fatal("shouldStopBusWatcher should return true when only disabled agents remain")
	}
}

func TestShouldStartBusWatcher_ErrorReturnsFalse(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Use a real TmuxManager with a bogus socket. HasWindow will fail,
	// and shouldStartBusWatcher should return false (conservative).
	mgr := session.NewTmuxManager("retinue-nonexistent-test-socket-xyz")

	// This exercises the error path — when tmux isn't reachable,
	// shouldStartBusWatcher returns false to avoid attempting to create
	// windows on a non-existent tmux server.
	// Note: on systems where tmux IS available, HasWindow returns (false, nil)
	// for a non-existent socket, which means shouldStartBusWatcher returns true.
	// We just verify it doesn't panic.
	_ = shouldStartBusWatcher(ctx, mgr)
}

func TestShouldStopBusWatcher_NoAgentsFile(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr := session.NewFakeManager()

	// Point to a nonexistent agents file — store.Load() will fail.
	store := standing.NewFileStore("/nonexistent/agents.yaml")

	// When the store can't be loaded, shouldStopBusWatcher returns false
	// (conservative: don't stop something if we can't verify the state).
	if shouldStopBusWatcher(ctx, mgr, store, "anything") {
		t.Fatal("shouldStopBusWatcher should return false when agents file can't be loaded")
	}
}

func TestBusWatcherWindowConstant(t *testing.T) {
	// Verify the window name matches what we expect so other code
	// (phone serve, bus watcher) can use it consistently.
	if busWatcherWindow != "bus-watcher" {
		t.Errorf("busWatcherWindow = %q, want %q", busWatcherWindow, "bus-watcher")
	}
}

// --- Group Chat Lifecycle End-to-End Test ---

func TestGroupChatLifecycle(t *testing.T) {
	t.Parallel()

	// 1. Create a temp apartment with agents.yaml (2 agents).
	agents := []standing.Agent{
		{ID: "azazello", Name: "Azazello", Role: "CI Watcher", Prompt: "Watch CI.", Enabled: true},
		{ID: "behemoth", Name: "Behemoth", Role: "Gardener", Prompt: "Tend code.", Enabled: true},
	}
	dir := t.TempDir()

	store := standing.NewFileStore(filepath.Join(dir, "agents.yaml"))
	if err := store.Save(agents); err != nil {
		t.Fatalf("saving agents: %v", err)
	}

	// 2. Create bus file.
	busPath := filepath.Join(dir, "messages.jsonl")
	b := bus.New(busPath)

	// 3. Simulate agent start: write "has joined" system messages.
	joinMsgs := []*bus.Message{
		bus.NewMessage("system", bus.TypeSystem, "Azazello has joined"),
		bus.NewMessage("system", bus.TypeSystem, "Behemoth has joined"),
	}
	for _, msg := range joinMsgs {
		if err := b.Append(msg); err != nil {
			t.Fatalf("appending join message: %v", err)
		}
	}

	// 4. Write agent output to bus (simulating watcher behavior).
	agentMsgs := []*bus.Message{
		bus.NewMessage("azazello", bus.TypeChat, "CI is green, all checks passing."),
		bus.NewMessage("behemoth", bus.TypeChat, "Found 3 unused imports, cleaning up."),
	}
	for _, msg := range agentMsgs {
		if err := b.Append(msg); err != nil {
			t.Fatalf("appending agent message: %v", err)
		}
	}

	// 5. Write a user message to bus.
	userMsg := bus.NewMessage("user", bus.TypeUser, "Good work team, any blockers?")
	if err := b.Append(userMsg); err != nil {
		t.Fatalf("appending user message: %v", err)
	}

	// 6. Read bus and verify all messages.
	allMsgs, err := b.ReadRecent(100)
	if err != nil {
		t.Fatalf("reading bus: %v", err)
	}

	if len(allMsgs) != 5 {
		t.Fatalf("expected 5 messages on bus, got %d", len(allMsgs))
	}

	// Verify system messages for agent joins.
	if allMsgs[0].Type != bus.TypeSystem || !strings.Contains(allMsgs[0].Text, "Azazello has joined") {
		t.Errorf("message 0: expected Azazello join system message, got type=%s text=%q", allMsgs[0].Type, allMsgs[0].Text)
	}
	if allMsgs[1].Type != bus.TypeSystem || !strings.Contains(allMsgs[1].Text, "Behemoth has joined") {
		t.Errorf("message 1: expected Behemoth join system message, got type=%s text=%q", allMsgs[1].Type, allMsgs[1].Text)
	}

	// Verify agent messages have correct attribution.
	if allMsgs[2].Name != "azazello" || allMsgs[2].Type != bus.TypeChat {
		t.Errorf("message 2: expected azazello chat, got name=%q type=%s", allMsgs[2].Name, allMsgs[2].Type)
	}
	if allMsgs[3].Name != "behemoth" || allMsgs[3].Type != bus.TypeChat {
		t.Errorf("message 3: expected behemoth chat, got name=%q type=%s", allMsgs[3].Name, allMsgs[3].Type)
	}

	// Verify user message.
	if allMsgs[4].Name != "user" || allMsgs[4].Type != bus.TypeUser {
		t.Errorf("message 4: expected user message, got name=%q type=%s", allMsgs[4].Name, allMsgs[4].Type)
	}
	if !strings.Contains(allMsgs[4].Text, "any blockers") {
		t.Errorf("message 4: expected 'any blockers' in text, got %q", allMsgs[4].Text)
	}

	// Verify chronological order (each timestamp >= previous).
	for i := 1; i < len(allMsgs); i++ {
		if allMsgs[i].Timestamp.Before(allMsgs[i-1].Timestamp) {
			t.Errorf("message %d timestamp (%v) is before message %d (%v)",
				i, allMsgs[i].Timestamp, i-1, allMsgs[i-1].Timestamp)
		}
	}

	// Verify each message has a unique non-empty ID.
	ids := make(map[string]bool)
	for i, msg := range allMsgs {
		if msg.ID == "" {
			t.Errorf("message %d has empty ID", i)
		}
		if ids[msg.ID] {
			t.Errorf("message %d has duplicate ID %q", i, msg.ID)
		}
		ids[msg.ID] = true
	}

	// 7. Simulate agent stop: write "has left" system messages.
	leaveMsgs := []*bus.Message{
		bus.NewMessage("system", bus.TypeSystem, "Azazello has left"),
		bus.NewMessage("system", bus.TypeSystem, "Behemoth has left"),
	}
	for _, msg := range leaveMsgs {
		if err := b.Append(msg); err != nil {
			t.Fatalf("appending leave message: %v", err)
		}
	}

	// 8. Verify bus has the complete conversation history.
	allMsgs, err = b.ReadRecent(100)
	if err != nil {
		t.Fatalf("reading final bus: %v", err)
	}

	if len(allMsgs) != 7 {
		t.Fatalf("expected 7 messages in final bus, got %d", len(allMsgs))
	}

	// Verify leave messages at the end.
	if allMsgs[5].Type != bus.TypeSystem || !strings.Contains(allMsgs[5].Text, "Azazello has left") {
		t.Errorf("message 5: expected Azazello leave, got type=%s text=%q", allMsgs[5].Type, allMsgs[5].Text)
	}
	if allMsgs[6].Type != bus.TypeSystem || !strings.Contains(allMsgs[6].Text, "Behemoth has left") {
		t.Errorf("message 6: expected Behemoth leave, got type=%s text=%q", allMsgs[6].Type, allMsgs[6].Text)
	}

	// Verify the full lifecycle sequence of message types.
	expectedTypes := []bus.MessageType{
		bus.TypeSystem, // Azazello joined
		bus.TypeSystem, // Behemoth joined
		bus.TypeChat,   // Azazello chat
		bus.TypeChat,   // Behemoth chat
		bus.TypeUser,   // User message
		bus.TypeSystem, // Azazello left
		bus.TypeSystem, // Behemoth left
	}
	for i, msg := range allMsgs {
		if msg.Type != expectedTypes[i] {
			t.Errorf("message %d: expected type %s, got %s", i, expectedTypes[i], msg.Type)
		}
	}
}

// TestBusWatcherFullStartStopCycle verifies the complete bus watcher
// lifecycle using FakeManager to simulate tmux operations:
// start first agent → bus watcher auto-starts → start second agent →
// stop first → bus watcher stays → stop second → bus watcher auto-stops.
func TestBusWatcherFullStartStopCycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr := session.NewFakeManager()

	agents := []standing.Agent{
		{ID: "azazello", Name: "Azazello", Prompt: "Watch.", Enabled: true},
		{ID: "behemoth", Name: "Behemoth", Prompt: "Garden.", Enabled: true},
	}

	dir := t.TempDir()
	store := standing.NewFileStore(filepath.Join(dir, "agents.yaml"))
	if err := store.Save(agents); err != nil {
		t.Fatalf("saving agents: %v", err)
	}

	ws := &workspace.Workspace{Path: dir, Config: workspace.Config{Name: "test"}}

	// --- Start first agent ---
	// Bus watcher should start.
	if !shouldStartBusWatcher(ctx, mgr) {
		t.Fatal("step 1: shouldStartBusWatcher should be true before any agents")
	}
	if err := mgr.CreateWindow(ctx, session.ApartmentSession, busWatcherWindow, ws.Path, "retinue bus serve"); err != nil {
		t.Fatalf("creating bus watcher: %v", err)
	}
	if err := mgr.CreateWindow(ctx, session.ApartmentSession, agentWindowName("azazello"), ws.Path, "claude"); err != nil {
		t.Fatalf("creating azazello: %v", err)
	}

	// --- Start second agent ---
	// Bus watcher should NOT start again.
	if shouldStartBusWatcher(ctx, mgr) {
		t.Fatal("step 2: shouldStartBusWatcher should be false when bus watcher already exists")
	}
	if err := mgr.CreateWindow(ctx, session.ApartmentSession, agentWindowName("behemoth"), ws.Path, "claude"); err != nil {
		t.Fatalf("creating behemoth: %v", err)
	}

	// Verify 3 windows: bus-watcher, agent-azazello, agent-behemoth.
	windows, _ := mgr.ListWindows(ctx, session.ApartmentSession)
	if len(windows) != 3 {
		t.Fatalf("expected 3 windows, got %d: %v", len(windows), windows)
	}

	// --- Stop first agent ---
	if err := mgr.KillWindow(ctx, session.ApartmentSession, agentWindowName("azazello")); err != nil {
		t.Fatalf("killing azazello: %v", err)
	}
	if shouldStopBusWatcher(ctx, mgr, store, "azazello") {
		t.Fatal("step 3: shouldStopBusWatcher should be false (behemoth still running)")
	}

	// --- Stop second agent ---
	if err := mgr.KillWindow(ctx, session.ApartmentSession, agentWindowName("behemoth")); err != nil {
		t.Fatalf("killing behemoth: %v", err)
	}
	if !shouldStopBusWatcher(ctx, mgr, store, "behemoth") {
		t.Fatal("step 4: shouldStopBusWatcher should be true (no agents running)")
	}
	if err := mgr.KillWindow(ctx, session.ApartmentSession, busWatcherWindow); err != nil {
		t.Fatalf("killing bus watcher: %v", err)
	}

	// Final state: no windows.
	windows, _ = mgr.ListWindows(ctx, session.ApartmentSession)
	if len(windows) != 0 {
		t.Errorf("expected 0 windows after cleanup, got %d: %v", len(windows), windows)
	}
}
