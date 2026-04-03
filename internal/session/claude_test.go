package session_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wolandomny/retinue/internal/session"
)

func TestClaudeProjectDir(t *testing.T) {
	got := session.ClaudeProjectDir("/Users/broc.oppler/apt")
	if !strings.HasSuffix(got, ".claude/projects/-Users-broc-oppler-apt") {
		t.Errorf("ClaudeProjectDir() = %q, expected suffix .claude/projects/-Users-broc-oppler-apt", got)
	}
}

func TestClaudeProjectDir_ManglesDotsAndSlashes(t *testing.T) {
	got := session.ClaudeProjectDir("/home/user/.hidden/path.v2")
	if !strings.Contains(got, "-home-user--hidden-path-v2") {
		t.Errorf("ClaudeProjectDir(.hidden/path.v2) = %q, expected mangled dots and slashes", got)
	}
}

func TestClaudeProjectDir_ContainsClaudeProjects(t *testing.T) {
	got := session.ClaudeProjectDir("/any/path")
	if !strings.Contains(got, ".claude/projects") {
		t.Errorf("ClaudeProjectDir should contain .claude/projects, got %q", got)
	}
}

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
		t.Errorf("NewestJSONLFile() = %q, want %q", got, want)
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

func TestNewestJSONLFile_SingleFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "only.jsonl")
	if err := os.WriteFile(path, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := session.NewestJSONLFile(dir)
	if got != path {
		t.Errorf("NewestJSONLFile(single) = %q, want %q", got, path)
	}
}

func TestNewestJSONLFile_IgnoresDirectories(t *testing.T) {
	dir := t.TempDir()

	// Create a directory ending in .jsonl (should be ignored).
	os.MkdirAll(filepath.Join(dir, "fake.jsonl"), 0o755)

	// Create a real file.
	realFile := filepath.Join(dir, "real.jsonl")
	os.WriteFile(realFile, []byte(`{}`), 0o644)

	got := session.NewestJSONLFile(dir)
	if got != realFile {
		t.Errorf("NewestJSONLFile() = %q, want %q", got, realFile)
	}
}

// --- SnapshotJSONLFiles tests ---

func TestSnapshotJSONLFiles(t *testing.T) {
	dir := t.TempDir()

	// Create 3 .jsonl files and 2 .txt files.
	jsonlFiles := []string{"a.jsonl", "b.jsonl", "c.jsonl"}
	txtFiles := []string{"x.txt", "y.txt"}

	for _, name := range jsonlFiles {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(`{}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range txtFiles {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("text"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	snap := session.SnapshotJSONLFiles(dir)

	// Verify snapshot contains exactly the 3 .jsonl files.
	if len(snap) != 3 {
		t.Fatalf("SnapshotJSONLFiles() returned %d files, want 3", len(snap))
	}
	for _, name := range jsonlFiles {
		fullPath := filepath.Join(dir, name)
		if !snap[fullPath] {
			t.Errorf("SnapshotJSONLFiles() missing %q", fullPath)
		}
	}
	for _, name := range txtFiles {
		fullPath := filepath.Join(dir, name)
		if snap[fullPath] {
			t.Errorf("SnapshotJSONLFiles() should not contain %q", fullPath)
		}
	}
}

func TestSnapshotJSONLFiles_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	snap := session.SnapshotJSONLFiles(dir)
	if len(snap) != 0 {
		t.Errorf("SnapshotJSONLFiles(empty) returned %d files, want 0", len(snap))
	}
}

func TestSnapshotJSONLFiles_NonexistentDir(t *testing.T) {
	snap := session.SnapshotJSONLFiles("/nonexistent/dir")
	if len(snap) != 0 {
		t.Errorf("SnapshotJSONLFiles(nonexistent) returned %d files, want 0", len(snap))
	}
}

// --- WaitForNewJSONL tests ---

func TestWaitForNewJSONL(t *testing.T) {
	dir := t.TempDir()

	// Create 2 existing .jsonl files.
	for _, name := range []string{"existing1.jsonl", "existing2.jsonl"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(`{}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Take snapshot.
	snap := session.SnapshotJSONLFiles(dir)

	// Start a goroutine that creates a new .jsonl file after 1 second.
	newFilePath := filepath.Join(dir, "brand-new.jsonl")
	go func() {
		time.Sleep(1 * time.Second)
		os.WriteFile(newFilePath, []byte(`{"new":true}`), 0o644)
	}()

	// Call WaitForNewJSONL with 5 second timeout.
	got := session.WaitForNewJSONL(dir, snap, 5*time.Second)

	// Verify it returns the new file, not either existing one.
	if got != newFilePath {
		t.Errorf("WaitForNewJSONL() = %q, want %q", got, newFilePath)
	}
}

func TestWaitForNewJSONLTimeout(t *testing.T) {
	dir := t.TempDir()

	// Create 2 existing files.
	for _, name := range []string{"existing1.jsonl", "existing2.jsonl"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(`{}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Take snapshot.
	snap := session.SnapshotJSONLFiles(dir)

	// Call WaitForNewJSONL with 1 second timeout (don't create new file).
	got := session.WaitForNewJSONL(dir, snap, 1*time.Second)

	// Verify it returns empty string.
	if got != "" {
		t.Errorf("WaitForNewJSONL() = %q, want empty string on timeout", got)
	}
}

func TestWaitForNewJSONLExistingFilesIgnored(t *testing.T) {
	dir := t.TempDir()

	// Create existing files.
	existingPath := filepath.Join(dir, "existing.jsonl")
	if err := os.WriteFile(existingPath, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Take snapshot.
	snap := session.SnapshotJSONLFiles(dir)

	// Modify the existing file (change its mod time) — should NOT be returned.
	time.Sleep(50 * time.Millisecond)
	os.WriteFile(existingPath, []byte(`{"updated":true}`), 0o644)

	// Start a goroutine that creates a truly new file after a short delay.
	newFilePath := filepath.Join(dir, "truly-new.jsonl")
	go func() {
		time.Sleep(500 * time.Millisecond)
		os.WriteFile(newFilePath, []byte(`{"new":true}`), 0o644)
	}()

	got := session.WaitForNewJSONL(dir, snap, 5*time.Second)

	// Verify the modified existing file is NOT returned — the truly new one is.
	if got != newFilePath {
		t.Errorf("WaitForNewJSONL() = %q, want %q (should ignore modified existing files)", got, newFilePath)
	}
}
