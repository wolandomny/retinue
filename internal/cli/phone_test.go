package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wolandomny/retinue/internal/session"
	"github.com/wolandomny/retinue/internal/standing"
	"github.com/wolandomny/retinue/internal/workspace"
)

// phoneMode returns "group" or "legacy" based on the same logic
// newPhoneServeCmd uses: if agents.yaml has agents, group mode; otherwise legacy.
func phoneMode(ws *workspace.Workspace) string {
	agentStore := standing.NewFileStore(ws.AgentsPath())
	agents, _ := agentStore.Load()
	if len(agents) > 0 {
		return "group"
	}
	return "legacy"
}

func TestPhoneServe_LegacyModeWithoutAgentsYAML(t *testing.T) {
	ws := setupTestWorkspace(t)

	// Override workspace detection to use our test workspace.
	originalWorkspaceFlag := workspaceFlag
	workspaceFlag = ws.Path
	defer func() { workspaceFlag = originalWorkspaceFlag }()

	// No agents.yaml exists — should use legacy mode.
	mode := phoneMode(ws)
	if mode != "legacy" {
		t.Errorf("expected legacy mode when no agents.yaml exists, got %q", mode)
	}
}

func TestPhoneServe_LegacyModeWithEmptyAgentsYAML(t *testing.T) {
	ws := setupTestWorkspace(t)

	// Override workspace detection to use our test workspace.
	originalWorkspaceFlag := workspaceFlag
	workspaceFlag = ws.Path
	defer func() { workspaceFlag = originalWorkspaceFlag }()

	// Create an empty agents.yaml (no agents defined).
	if err := os.WriteFile(ws.AgentsPath(), []byte("agents: []\n"), 0o644); err != nil {
		t.Fatalf("failed to create agents.yaml: %v", err)
	}

	mode := phoneMode(ws)
	if mode != "legacy" {
		t.Errorf("expected legacy mode when agents.yaml is empty, got %q", mode)
	}
}

func TestPhoneServe_GroupModeWithAgents(t *testing.T) {
	ws := setupTestWorkspace(t)

	// Override workspace detection to use our test workspace.
	originalWorkspaceFlag := workspaceFlag
	workspaceFlag = ws.Path
	defer func() { workspaceFlag = originalWorkspaceFlag }()

	// Create agents.yaml with agents defined.
	agentsYAML := `agents:
  - id: ci-watcher
    name: CI Watcher
    prompt: Watch CI
    enabled: true
  - id: gardener
    name: Gardener
    prompt: Tend the codebase
    enabled: true
`
	if err := os.WriteFile(ws.AgentsPath(), []byte(agentsYAML), 0o644); err != nil {
		t.Fatalf("failed to create agents.yaml: %v", err)
	}

	mode := phoneMode(ws)
	if mode != "group" {
		t.Errorf("expected group mode when agents are defined, got %q", mode)
	}
}

func TestPhoneServe_AgentsPathFromWorkspace(t *testing.T) {
	ws := setupTestWorkspace(t)

	// Verify AgentsPath returns the expected path.
	expected := filepath.Join(ws.Path, "agents.yaml")
	if ws.AgentsPath() != expected {
		t.Errorf("AgentsPath() = %q, want %q", ws.AgentsPath(), expected)
	}
}

func TestPhoneServeDetectsExistingBusWatcher(t *testing.T) {
	ctx := context.Background()
	mgr := session.NewFakeManager()

	// When no bus-watcher window exists, phone serve in group mode should
	// want to start one.
	if !shouldStartBusWatcher(ctx, mgr) {
		t.Error("expected shouldStartBusWatcher=true when no bus-watcher window exists")
	}

	// Simulate bus-watcher window already running (e.g. started by agent start).
	if err := mgr.CreateWindow(ctx, session.ApartmentSession, busWatcherWindow, "/tmp", "retinue bus serve"); err != nil {
		t.Fatalf("creating bus-watcher window: %v", err)
	}

	// Now phone serve should detect the existing bus-watcher and NOT try to start another.
	if shouldStartBusWatcher(ctx, mgr) {
		t.Error("expected shouldStartBusWatcher=false when bus-watcher window already exists")
	}
}

func TestPhoneServeGroupModeStartsBusWatcher(t *testing.T) {
	ctx := context.Background()
	mgr := session.NewFakeManager()

	// In group mode with no bus-watcher running, phone serve should start it.
	ws := setupTestWorkspace(t)

	// Create agents.yaml with agents defined.
	agentsYAML := `agents:
  - id: ci-watcher
    name: CI Watcher
    prompt: Watch CI
    enabled: true
`
	if err := os.WriteFile(ws.AgentsPath(), []byte(agentsYAML), 0o644); err != nil {
		t.Fatalf("failed to create agents.yaml: %v", err)
	}

	// Verify it's group mode.
	mode := phoneMode(ws)
	if mode != "group" {
		t.Fatalf("expected group mode, got %q", mode)
	}

	// In group mode, bus watcher should be needed.
	if !shouldStartBusWatcher(ctx, mgr) {
		t.Fatal("bus watcher should be started in group mode with no existing watcher")
	}

	// Simulate starting it.
	if err := mgr.CreateWindow(ctx, session.ApartmentSession, busWatcherWindow, ws.Path, "retinue bus serve"); err != nil {
		t.Fatalf("starting bus watcher: %v", err)
	}

	// Now it should not need to start again.
	if shouldStartBusWatcher(ctx, mgr) {
		t.Fatal("bus watcher should not start again after it's already running")
	}
}

func TestPhoneServeLegacyModeSkipsBusWatcher(t *testing.T) {
	ctx := context.Background()
	mgr := session.NewFakeManager()

	// In legacy mode (no agents), the bus watcher is irrelevant, but
	// shouldStartBusWatcher still returns true (it just checks if the window
	// exists). The caller is responsible for only starting it in group mode.
	ws := setupTestWorkspace(t)

	mode := phoneMode(ws)
	if mode != "legacy" {
		t.Fatalf("expected legacy mode, got %q", mode)
	}

	// The phone serve command checks mode first, then decides whether to
	// start the watcher. Verify the mode gating works correctly.
	needsWatcher := mode == "group" && shouldStartBusWatcher(ctx, mgr)
	if needsWatcher {
		t.Fatal("legacy mode should not need a bus watcher")
	}
}
