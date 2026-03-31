package standing

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestFileStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agents.yaml")

	store := NewFileStore(path)

	agents := []Agent{
		{
			ID:       "azazello",
			Name:     "Azazello",
			Role:     "CI Watcher",
			Repos:    []string{"api", "frontend"},
			Schedule: "every 4h",
			Prompt:   "Watch CI pipelines and report failures.",
			Enabled:  true,
		},
		{
			ID:     "behemoth",
			Name:   "Behemoth",
			Role:   "Codebase Gardener",
			Repos:  []string{"api"},
			Model:  "claude-sonnet-4-20250514",
			Prompt: "Keep the codebase tidy.",
		},
	}

	if err := store.Save(agents); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if len(loaded) != len(agents) {
		t.Fatalf("Load() returned %d agents, want %d", len(loaded), len(agents))
	}

	for i, got := range loaded {
		want := agents[i]
		if got.ID != want.ID {
			t.Errorf("agent[%d].ID = %q, want %q", i, got.ID, want.ID)
		}
		if got.Name != want.Name {
			t.Errorf("agent[%d].Name = %q, want %q", i, got.Name, want.Name)
		}
		if got.Role != want.Role {
			t.Errorf("agent[%d].Role = %q, want %q", i, got.Role, want.Role)
		}
		if got.Prompt != want.Prompt {
			t.Errorf("agent[%d].Prompt = %q, want %q", i, got.Prompt, want.Prompt)
		}
		if got.Enabled != want.Enabled {
			t.Errorf("agent[%d].Enabled = %v, want %v", i, got.Enabled, want.Enabled)
		}
	}
}

func TestFileStoreGet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agents.yaml")
	store := NewFileStore(path)

	agents := []Agent{
		{ID: "azazello", Name: "Azazello", Prompt: "Watch CI."},
		{ID: "behemoth", Name: "Behemoth", Prompt: "Guard the code."},
	}
	if err := store.Save(agents); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, err := store.Get("behemoth")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.ID != "behemoth" {
		t.Errorf("Get() returned agent %q, want %q", got.ID, "behemoth")
	}
	if got.Name != "Behemoth" {
		t.Errorf("Get() returned name %q, want %q", got.Name, "Behemoth")
	}

	_, err = store.Get("nonexistent")
	if err == nil {
		t.Error("Get() expected error for nonexistent agent")
	}
}

func TestFileStoreUpdate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agents.yaml")
	store := NewFileStore(path)

	agents := []Agent{
		{ID: "azazello", Name: "Azazello", Prompt: "Watch CI.", Enabled: false},
	}
	if err := store.Save(agents); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	err := store.Update("azazello", func(a *Agent) {
		a.Enabled = true
		a.Role = "CI Watcher"
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	got, err := store.Get("azazello")
	if err != nil {
		t.Fatalf("Get() after update error = %v", err)
	}
	if !got.Enabled {
		t.Error("Enabled should be true after update")
	}
	if got.Role != "CI Watcher" {
		t.Errorf("Role = %q, want %q", got.Role, "CI Watcher")
	}
}

func TestFileStoreUpdateNotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agents.yaml")
	store := NewFileStore(path)

	if err := store.Save([]Agent{{ID: "a", Prompt: "p"}}); err != nil {
		t.Fatal(err)
	}

	err := store.Update("nonexistent", func(a *Agent) {
		a.Enabled = true
	})
	if err == nil {
		t.Error("Update() expected error for nonexistent agent")
	}
}

func TestFileStoreLoadMissingFile(t *testing.T) {
	store := NewFileStore("/nonexistent/agents.yaml")
	_, err := store.Load()
	if err == nil {
		t.Error("Load() expected error for missing file")
	}
}

func TestFileStoreYAMLFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agents.yaml")

	// Write raw YAML matching the expected format.
	raw := `agents:
  - id: azazello
    name: Azazello
    role: CI Watcher
    repos:
      - api
      - frontend
    schedule: every 4h
    prompt: |
      Watch CI pipelines and report failures.
    enabled: true
`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	store := NewFileStore(path)
	agents, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if len(agents) != 1 {
		t.Fatalf("Load() returned %d agents, want 1", len(agents))
	}

	if agents[0].ID != "azazello" {
		t.Errorf("agent.ID = %q, want %q", agents[0].ID, "azazello")
	}
	if agents[0].Role != "CI Watcher" {
		t.Errorf("agent.Role = %q, want %q", agents[0].Role, "CI Watcher")
	}
	if !agents[0].Enabled {
		t.Error("agent.Enabled should be true")
	}
	if len(agents[0].Repos) != 2 {
		t.Errorf("agent.Repos length = %d, want 2", len(agents[0].Repos))
	}
}

func TestFileStoreEnabledField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agents.yaml")
	store := NewFileStore(path)

	agents := []Agent{
		{
			ID:      "enabled-agent",
			Name:    "Enabled",
			Prompt:  "Do stuff.",
			Enabled: true,
		},
		{
			ID:      "disabled-agent",
			Name:    "Disabled",
			Prompt:  "Do other stuff.",
			Enabled: false,
		},
	}

	if err := store.Save(agents); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if len(loaded) != 2 {
		t.Fatalf("Load() returned %d agents, want 2", len(loaded))
	}

	if !loaded[0].Enabled {
		t.Error("loaded[0].Enabled should be true")
	}
	if loaded[1].Enabled {
		t.Error("loaded[1].Enabled should be false")
	}

	// Verify YAML contains enabled: true for the first agent.
	fileContent, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading file: %v", err)
	}

	if !strings.Contains(string(fileContent), "enabled: true") {
		t.Errorf("YAML file should contain 'enabled: true', got:\n%s", string(fileContent))
	}
}

// TestConcurrentUpdates verifies that many goroutines can call Update
// concurrently without any updates being lost.
func TestConcurrentUpdates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agents.yaml")
	store := NewFileStore(path)

	const numAgents = 20

	// Create agents, all starting as disabled.
	agents := make([]Agent, numAgents)
	for i := range agents {
		agents[i] = Agent{
			ID:     fmt.Sprintf("agent-%d", i),
			Name:   fmt.Sprintf("Agent %d", i),
			Prompt: fmt.Sprintf("Prompt for agent %d", i),
		}
	}
	if err := store.Save(agents); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Concurrently enable every agent.
	var wg sync.WaitGroup
	wg.Add(numAgents)
	for i := 0; i < numAgents; i++ {
		go func(id string) {
			defer wg.Done()
			if err := store.Update(id, func(a *Agent) {
				a.Enabled = true
			}); err != nil {
				t.Errorf("Update(%q) error = %v", id, err)
			}
		}(fmt.Sprintf("agent-%d", i))
	}
	wg.Wait()

	// Verify all agents were enabled — none should still be disabled.
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(loaded) != numAgents {
		t.Fatalf("expected %d agents, got %d (agents lost!)", numAgents, len(loaded))
	}
	for _, a := range loaded {
		if !a.Enabled {
			t.Errorf("agent %q should be enabled", a.ID)
		}
	}
}

// TestConcurrentReadsAndWrites verifies that concurrent Load and Update
// calls don't cause data races or corruption.
func TestConcurrentReadsAndWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agents.yaml")
	store := NewFileStore(path)

	const numAgents = 10

	agents := make([]Agent, numAgents)
	for i := range agents {
		agents[i] = Agent{
			ID:     fmt.Sprintf("agent-%d", i),
			Name:   fmt.Sprintf("Agent %d", i),
			Prompt: fmt.Sprintf("Prompt for agent %d", i),
		}
	}
	if err := store.Save(agents); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	var wg sync.WaitGroup

	// Spawn readers.
	for i := 0; i < numAgents; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := store.Load(); err != nil {
				t.Errorf("Load() error = %v", err)
			}
		}()
	}

	// Spawn writers.
	for i := 0; i < numAgents; i++ {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if err := store.Update(id, func(a *Agent) {
				a.Enabled = true
			}); err != nil {
				t.Errorf("Update(%q) error = %v", id, err)
			}
		}(fmt.Sprintf("agent-%d", i))
	}

	wg.Wait()

	// Just verify no data was lost.
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(loaded) != numAgents {
		t.Fatalf("expected %d agents, got %d (agents lost!)", numAgents, len(loaded))
	}
}
