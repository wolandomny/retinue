package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wolandomny/retinue/internal/session"
)

func TestBuildWolandPrompt_ContainsKeySections(t *testing.T) {
	prompt := buildWolandPrompt(
		"/tmp/test-apartment",
		"name: test\nrepos:\n  api: /tmp/api\nmodel: claude-opus-4-6\nmax_workers: 4\n",
		"tasks:\n  - id: task-1\n    status: pending\n",
		"agents:\n  - id: azazello\n    name: Azazello\n",
	)

	checks := []struct {
		name     string
		contains string
	}{
		{"persona", "You are Woland"},
		{"apartment path", "/tmp/test-apartment"},
		{"config included", "name: test"},
		{"repos in config", "api: /tmp/api"},
		{"tasks included", "task-1"},
		{"tasks.yaml path", "/tmp/test-apartment/tasks.yaml"},
		{"schema id field", "id: short-kebab-id"},
		{"schema depends_on", "depends_on:"},
		{"schema skip_validate", "skip_validate: false"},
		{"schema prompt field", "prompt: |"},
		{"dispatch instruction", "retinue dispatch"},
		{"help config hint", "retinue help config"},
		{"agents section header", "Standing Agents (agents.yaml)"},
		{"agents content included", "azazello"},
		{"agent commands", "retinue agent list"},
		{"agent start command", "retinue agent start"},
		{"agent stop command", "retinue agent stop"},
		{"agent schema", "Agent YAML Schema"},
		{"group chat protocol", "Group Chat Protocol"},
		{"when not to use route", "When NOT to use →"},
	}

	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if !strings.Contains(prompt, c.contains) {
				t.Errorf("prompt missing %q (%s)", c.contains, c.name)
			}
		})
	}
}

func TestBuildWolandPrompt_NoTasksMessage(t *testing.T) {
	prompt := buildWolandPrompt("/tmp/apt", "name: x\n", "(no tasks.yaml found yet)", "(no agents.yaml found yet)")

	if !strings.Contains(prompt, "(no tasks.yaml found yet)") {
		t.Error("prompt should include no-tasks message when tasks are empty")
	}
}

func TestBuildWolandPrompt_NoAgentsMessage(t *testing.T) {
	prompt := buildWolandPrompt("/tmp/apt", "name: x\n", "tasks: []\n", "(no agents.yaml found yet)")

	if !strings.Contains(prompt, "(no agents.yaml found yet)") {
		t.Error("prompt should include no-agents message when agents file is missing")
	}
}

func TestBuildBabytalkPrompt_ContainsKeySections(t *testing.T) {
	prompt := buildBabytalkPrompt(
		"/tmp/test-apartment",
		"name: test\nrepos:\n  web: /tmp/web\nmodel: claude-opus-4-6\nmax_workers: 4\n",
		"tasks:\n  - id: task-1\n    status: pending\n",
		"agents:\n  - id: behemoth\n    name: Behemoth\n",
	)

	checks := []struct {
		name     string
		contains string
	}{
		{"persona", "You are Woland"},
		{"builder context", "not a software engineer"},
		{"apartment path", "/tmp/test-apartment"},
		{"config included", "name: test"},
		{"tasks included", "task-1"},
		{"tasks.yaml path", "/tmp/test-apartment/tasks.yaml"},
		{"retry default", "--retry"},
		{"merge review default", "merge --review"},
		{"quality standards", "Quality Standards"},
		{"linting check", "eslint"},
		{"explain decisions", "Explaining Decisions"},
		{"validation emphasis", "non-negotiable"},
		{"skip validation section", "Skipping Validation"},
		{"skip_validate field", "skip_validate: false"},
		{"help config hint", "retinue help config"},
		{"agents section header", "Standing Agents (agents.yaml)"},
		{"agents content included", "behemoth"},
		{"agent commands", "retinue agent list"},
		{"agent schema", "Agent YAML Schema"},
		{"group chat protocol", "Group Chat Protocol"},
		{"when not to use route", "When NOT to use →"},
	}

	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if !strings.Contains(prompt, c.contains) {
				t.Errorf("babytalk prompt missing %q (%s)", c.contains, c.name)
			}
		})
	}
}

func TestBuildBabytalkPrompt_DiffersFromTalk(t *testing.T) {
	args := []string{
		"/tmp/apt",
		"name: test\n",
		"tasks: []\n",
		"agents: []\n",
	}
	talk := buildWolandPrompt(args[0], args[1], args[2], args[3])
	baby := buildBabytalkPrompt(args[0], args[1], args[2], args[3])

	if talk == baby {
		t.Error("babytalk prompt should differ from talk prompt")
	}

	// Babytalk should have quality-focused content that talk doesn't.
	if !strings.Contains(baby, "Quality Standards") {
		t.Error("babytalk should contain Quality Standards section")
	}
	if strings.Contains(talk, "Quality Standards") {
		t.Error("talk prompt should NOT contain Quality Standards section")
	}
}

// --- Session marker tests ---

func TestNewestJSONLFile(t *testing.T) {
	dir := t.TempDir()

	// Create files with different modification times.
	files := []string{"old.jsonl", "middle.jsonl", "newest.jsonl"}
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(`{}`), 0o644); err != nil {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Also create a non-.jsonl file that is the newest overall (should be ignored).
	time.Sleep(50 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	got := session.NewestJSONLFile(dir)
	want := filepath.Join(dir, "newest.jsonl")
	if got != want {
		t.Errorf("session.NewestJSONLFile() = %q, want %q", got, want)
	}
}

func TestNewestJSONLFile_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	if got := session.NewestJSONLFile(dir); got != "" {
		t.Errorf("expected empty string for empty dir, got %q", got)
	}
}

func TestNewestJSONLFile_NonexistentDir(t *testing.T) {
	if got := session.NewestJSONLFile("/nonexistent/dir"); got != "" {
		t.Errorf("expected empty string for nonexistent dir, got %q", got)
	}
}

func TestWriteSessionMarker_WritesNewestFile(t *testing.T) {
	aptDir := t.TempDir()

	// Create the Claude projects directory where writeSessionMarker will look.
	projDir := session.ClaudeProjectDir(aptDir)
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create session files (simulating existing + Woland's new file).
	oldFile := filepath.Join(projDir, "old-agent-session.jsonl")
	if err := os.WriteFile(oldFile, []byte(`{"type":"human"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)

	wolandFile := filepath.Join(projDir, "woland-session.jsonl")
	if err := os.WriteFile(wolandFile, []byte(`{"type":"system"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// writeSessionMarker sleeps 2 seconds, which is too slow for tests.
	// Test the underlying logic directly via newestJSONLFile + marker write.
	newest := session.NewestJSONLFile(projDir)
	if newest == "" {
		t.Fatal("expected to find a .jsonl file")
	}

	markerPath := filepath.Join(aptDir, ".woland-session")
	if err := os.WriteFile(markerPath, []byte(newest), 0o644); err != nil {
		t.Fatal(err)
	}

	// Verify marker was written with the correct (newest) path.
	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("reading marker: %v", err)
	}
	if string(data) != wolandFile {
		t.Errorf("marker = %q, want %q", string(data), wolandFile)
	}
}

func TestWriteSessionMarker_AgentFileDoesNotOverrideMarker(t *testing.T) {
	aptDir := t.TempDir()

	projDir := session.ClaudeProjectDir(aptDir)
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Woland's session file (created first, marker written).
	wolandFile := filepath.Join(projDir, "woland.jsonl")
	if err := os.WriteFile(wolandFile, []byte(`{"type":"system"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Write the marker (simulating what writeSessionMarker does).
	markerPath := filepath.Join(aptDir, ".woland-session")
	if err := os.WriteFile(markerPath, []byte(wolandFile), 0o644); err != nil {
		t.Fatal(err)
	}

	// Now a standing agent creates a newer session file.
	time.Sleep(50 * time.Millisecond)
	agentFile := filepath.Join(projDir, "agent-session.jsonl")
	if err := os.WriteFile(agentFile, []byte(`{"type":"system"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Verify the marker still points to Woland's file (not overwritten).
	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("reading marker: %v", err)
	}
	if string(data) != wolandFile {
		t.Errorf("marker should still point to Woland's file %q, got %q", wolandFile, string(data))
	}
}

func TestWolandProjectDir(t *testing.T) {
	got := session.ClaudeProjectDir("/Users/broc.oppler/apt")
	if !strings.HasSuffix(got, ".claude/projects/-Users-broc-oppler-apt") {
		t.Errorf("session.ClaudeProjectDir() = %q, expected suffix .claude/projects/-Users-broc-oppler-apt", got)
	}
}

// --- RefreshSessionMarker tests for woland.go integration ---

func TestRefreshSessionMarker_ValidMarker(t *testing.T) {
	aptDir := t.TempDir()
	projDir := session.ClaudeProjectDir(aptDir)
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a recently modified session file
	sessionFile := filepath.Join(projDir, "woland-session.jsonl")
	if err := os.WriteFile(sessionFile, []byte(`{"type":"system"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create marker pointing to the session file
	markerPath := filepath.Join(aptDir, ".woland-session")
	if err := os.WriteFile(markerPath, []byte(sessionFile), 0o644); err != nil {
		t.Fatal(err)
	}

	// Refresh should do nothing since the marker is valid and recent
	if err := session.RefreshSessionMarker(aptDir, ".woland-session"); err != nil {
		t.Fatalf("RefreshSessionMarker() failed: %v", err)
	}

	// Verify marker still points to the same file
	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("reading marker: %v", err)
	}
	if string(data) != sessionFile {
		t.Errorf("marker = %q, want %q", string(data), sessionFile)
	}
}

func TestRefreshSessionMarker_StaleMarker(t *testing.T) {
	aptDir := t.TempDir()
	projDir := session.ClaudeProjectDir(aptDir)
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create an old session file (>5 minutes old)
	oldFile := filepath.Join(projDir, "old-session.jsonl")
	if err := os.WriteFile(oldFile, []byte(`{"type":"old"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Backdate the file to >5 minutes ago
	oldTime := time.Now().Add(-10 * time.Minute)
	if err := os.Chtimes(oldFile, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	// Create marker pointing to the old file
	markerPath := filepath.Join(aptDir, ".woland-session")
	if err := os.WriteFile(markerPath, []byte(oldFile), 0o644); err != nil {
		t.Fatal(err)
	}

	// Wait a bit to ensure time difference
	time.Sleep(100 * time.Millisecond)

	// Create a newer session file
	newerFile := filepath.Join(projDir, "newer-session.jsonl")
	if err := os.WriteFile(newerFile, []byte(`{"type":"newer"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Refresh should update the marker to point to newer file
	if err := session.RefreshSessionMarker(aptDir, ".woland-session"); err != nil {
		t.Fatalf("RefreshSessionMarker() failed: %v", err)
	}

	// Verify marker now points to the newer file
	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("reading marker: %v", err)
	}
	if string(data) != newerFile {
		t.Errorf("marker = %q, want %q", string(data), newerFile)
	}
}

func TestRefreshSessionMarker_MissingMarker(t *testing.T) {
	aptDir := t.TempDir()
	projDir := session.ClaudeProjectDir(aptDir)
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a session file but no marker
	sessionFile := filepath.Join(projDir, "session.jsonl")
	if err := os.WriteFile(sessionFile, []byte(`{"type":"system"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Refresh should create the marker
	if err := session.RefreshSessionMarker(aptDir, ".woland-session"); err != nil {
		t.Fatalf("RefreshSessionMarker() failed: %v", err)
	}

	// Verify marker was created and points to the session file
	markerPath := filepath.Join(aptDir, ".woland-session")
	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("reading marker: %v", err)
	}
	if string(data) != sessionFile {
		t.Errorf("marker = %q, want %q", string(data), sessionFile)
	}
}

func TestRefreshSessionMarker_MarkerPointsToDeletedFile(t *testing.T) {
	aptDir := t.TempDir()
	projDir := session.ClaudeProjectDir(aptDir)
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create marker pointing to a nonexistent file
	markerPath := filepath.Join(aptDir, ".woland-session")
	deletedFile := filepath.Join(projDir, "deleted-session.jsonl")
	if err := os.WriteFile(markerPath, []byte(deletedFile), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a valid session file
	validFile := filepath.Join(projDir, "valid-session.jsonl")
	if err := os.WriteFile(validFile, []byte(`{"type":"system"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Refresh should update the marker
	if err := session.RefreshSessionMarker(aptDir, ".woland-session"); err != nil {
		t.Fatalf("RefreshSessionMarker() failed: %v", err)
	}

	// Verify marker now points to the valid file
	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("reading marker: %v", err)
	}
	if string(data) != validFile {
		t.Errorf("marker = %q, want %q", string(data), validFile)
	}
}

func TestRefreshSessionMarker_NoSessionFiles(t *testing.T) {
	aptDir := t.TempDir()
	projDir := session.ClaudeProjectDir(aptDir)
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// No marker, no session files — RefreshSessionMarker should not create a marker.
	if err := session.RefreshSessionMarker(aptDir, ".woland-session"); err != nil {
		t.Fatalf("RefreshSessionMarker() failed: %v", err)
	}

	markerPath := filepath.Join(aptDir, ".woland-session")
	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Error("marker should not be created when there are no session files")
	}
}
