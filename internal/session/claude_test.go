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
