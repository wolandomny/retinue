package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wolandomny/retinue/internal/session"
)

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

	// When no bus-watcher window exists, phone serve should
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

func TestPhoneServeStartsBusWatcher(t *testing.T) {
	ctx := context.Background()
	mgr := session.NewFakeManager()
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

	// Bus watcher should be needed when no existing watcher is running.
	if !shouldStartBusWatcher(ctx, mgr) {
		t.Fatal("bus watcher should be started when no existing watcher is running")
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

func TestPhoneServeBusWatcherWithNoAgents(t *testing.T) {
	ctx := context.Background()
	mgr := session.NewFakeManager()

	// Even with no agents, the bus watcher should still be started.
	// The bus watcher gracefully handles zero agents.
	if !shouldStartBusWatcher(ctx, mgr) {
		t.Fatal("bus watcher should be started even with no agents")
	}
}

// TestPhoneAndAgentShareBusWatcherWindow verifies that phone serve and agent
// start use the same tmux window name for the bus watcher, so whichever runs
// first creates it and the second detects it and skips. This was the root cause
// of the double-bus-watcher bug: phone used to start an in-process goroutine
// that was invisible to agent's tmux window check.
func TestPhoneAndAgentShareBusWatcherWindow(t *testing.T) {
	ctx := context.Background()
	mgr := session.NewFakeManager()
	ws := setupTestWorkspace(t)

	// Simulate phone serve creating the bus watcher as a tmux window (the fix).
	bwCmd := busWatcherCommand(ws)
	if err := mgr.CreateWindow(ctx, session.ApartmentSession, busWatcherWindow, ws.Path, bwCmd); err != nil {
		t.Fatalf("phone creating bus watcher window: %v", err)
	}

	// Now agent start checks shouldStartBusWatcher — it must see the
	// window phone created and NOT start a second one.
	if shouldStartBusWatcher(ctx, mgr) {
		t.Fatal("agent should detect bus watcher started by phone and not create a second one")
	}

	// Verify the reverse: agent creates it first, phone detects it.
	mgr2 := session.NewFakeManager()
	if err := mgr2.CreateWindow(ctx, session.ApartmentSession, busWatcherWindow, ws.Path, bwCmd); err != nil {
		t.Fatalf("agent creating bus watcher window: %v", err)
	}

	// Phone's check (same function) should also detect it.
	if shouldStartBusWatcher(ctx, mgr2) {
		t.Fatal("phone should detect bus watcher started by agent and not create a second one")
	}
}
