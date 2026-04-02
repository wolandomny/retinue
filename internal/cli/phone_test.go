package cli

import (
	"os"
	"path/filepath"
	"testing"

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
