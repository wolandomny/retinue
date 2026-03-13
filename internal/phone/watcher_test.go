package phone

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseLine_AssistantText(t *testing.T) {
	line := `{"type":"assistant","uuid":"abc-123","message":{"content":[{"type":"text","text":"Hello from Woland!"}]}}`
	text, uuid, ok := ParseLine(line)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if uuid != "abc-123" {
		t.Errorf("expected uuid 'abc-123', got %q", uuid)
	}
	if text != "Hello from Woland!" {
		t.Errorf("expected text 'Hello from Woland!', got %q", text)
	}
}

func TestParseLine_MultipleTextBlocks(t *testing.T) {
	line := `{"type":"assistant","uuid":"def-456","message":{"content":[{"type":"text","text":"Part 1"},{"type":"tool_use","text":"ignored"},{"type":"text","text":"Part 2"}]}}`
	text, uuid, ok := ParseLine(line)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if uuid != "def-456" {
		t.Errorf("expected uuid 'def-456', got %q", uuid)
	}
	if text != "Part 1\nPart 2" {
		t.Errorf("expected 'Part 1\\nPart 2', got %q", text)
	}
}

func TestParseLine_NonAssistant(t *testing.T) {
	line := `{"type":"human","uuid":"ghi-789","message":{"content":[{"type":"text","text":"user input"}]}}`
	text, _, ok := ParseLine(line)
	if !ok {
		t.Fatal("expected ok=true for valid JSON")
	}
	if text != "" {
		t.Errorf("expected empty text for non-assistant message, got %q", text)
	}
}

func TestParseLine_AssistantNoText(t *testing.T) {
	line := `{"type":"assistant","uuid":"jkl-012","message":{"content":[{"type":"tool_use","text":"tool data"}]}}`
	text, uuid, ok := ParseLine(line)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if uuid != "jkl-012" {
		t.Errorf("expected uuid 'jkl-012', got %q", uuid)
	}
	if text != "" {
		t.Errorf("expected empty text, got %q", text)
	}
}

func TestParseLine_InvalidJSON(t *testing.T) {
	line := `{not valid json`
	_, _, ok := ParseLine(line)
	if ok {
		t.Fatal("expected ok=false for invalid JSON")
	}
}

func TestParseLine_EmptyLine(t *testing.T) {
	_, _, ok := ParseLine("")
	if ok {
		t.Fatal("expected ok=false for empty line")
	}
}

func TestParseLine_WhitespaceOnly(t *testing.T) {
	_, _, ok := ParseLine("   ")
	if ok {
		t.Fatal("expected ok=false for whitespace-only line")
	}
}

func TestClaudeProjectDir(t *testing.T) {
	tests := []struct {
		name     string
		aptPath  string
		wantSuffix string
	}{
		{
			name:       "standard path",
			aptPath:    "/Users/broc.oppler/apt",
			wantSuffix: ".claude/projects/-Users-broc-oppler-apt",
		},
		{
			name:       "path with dots",
			aptPath:    "/Users/john.doe/workspace",
			wantSuffix: ".claude/projects/-Users-john-doe-workspace",
		},
		{
			name:       "simple path",
			aptPath:    "/home/user/work",
			wantSuffix: ".claude/projects/-home-user-work",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClaudeProjectDir(tt.aptPath)
			if !strings.HasSuffix(got, tt.wantSuffix) {
				t.Errorf("ClaudeProjectDir(%q) = %q, want suffix %q", tt.aptPath, got, tt.wantSuffix)
			}
		})
	}
}

func TestFindActiveSession(t *testing.T) {
	// Create a temp directory with multiple .jsonl files.
	dir := t.TempDir()

	// Create files with different modification times.
	files := []struct {
		name    string
		content string
		delay   time.Duration
	}{
		{"old-session.jsonl", `{"type":"human"}`, 0},
		{"newer-session.jsonl", `{"type":"human"}`, 50 * time.Millisecond},
		{"newest-session.jsonl", `{"type":"human"}`, 100 * time.Millisecond},
	}

	for _, f := range files {
		if f.delay > 0 {
			time.Sleep(f.delay)
		}
		path := filepath.Join(dir, f.name)
		if err := os.WriteFile(path, []byte(f.content), 0o644); err != nil {
			t.Fatalf("writing test file: %v", err)
		}
	}

	// Also create a subdirectory with a .jsonl file (should be ignored).
	subDir := filepath.Join(dir, "subagent")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("creating subdirectory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "sub.jsonl"), []byte(`{"type":"human"}`), 0o644); err != nil {
		t.Fatalf("writing subdir file: %v", err)
	}

	// Create a non-.jsonl file (should be ignored).
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatalf("writing non-jsonl file: %v", err)
	}

	w := &Watcher{
		projectDir: dir,
		logger:     log.New(os.Stderr, "test: ", 0),
		seen:       make(map[string]bool),
	}

	got, err := w.findActiveSession()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := filepath.Join(dir, "newest-session.jsonl")
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestFindActiveSession_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	w := &Watcher{
		projectDir: dir,
		logger:     log.New(os.Stderr, "test: ", 0),
		seen:       make(map[string]bool),
	}

	got, err := w.findActiveSession()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty string for empty dir, got %q", got)
	}
}

func TestFindActiveSession_NonexistentDir(t *testing.T) {
	w := &Watcher{
		projectDir: "/nonexistent/path",
		logger:     log.New(os.Stderr, "test: ", 0),
		seen:       make(map[string]bool),
	}

	got, err := w.findActiveSession()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty string for nonexistent dir, got %q", got)
	}
}

func TestWatcher_RealFile(t *testing.T) {
	dir := t.TempDir()

	// Create an initial session file.
	sessionFile := filepath.Join(dir, "test-session.jsonl")
	if err := os.WriteFile(sessionFile, []byte(""), 0o644); err != nil {
		t.Fatalf("creating session file: %v", err)
	}

	w := &Watcher{
		projectDir: dir,
		logger:     log.New(os.Stderr, "test: ", 0),
		seen:       make(map[string]bool),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch := w.Watch(ctx, nil)

	// Give the watcher time to start and find the file.
	time.Sleep(200 * time.Millisecond)

	// Write assistant messages to the file.
	f, err := os.OpenFile(sessionFile, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("opening session file for append: %v", err)
	}

	lines := []string{
		`{"type":"human","uuid":"h1","message":{"content":[{"type":"text","text":"hello"}]}}`,
		`{"type":"assistant","uuid":"a1","message":{"content":[{"type":"text","text":"Hi there!"}]}}`,
		`{"type":"assistant","uuid":"a2","message":{"content":[{"type":"text","text":"How can I help?"}]}}`,
	}

	for _, line := range lines {
		fmt.Fprintln(f, line)
	}
	f.Close()

	// Collect messages from the channel.
	var received []string
	timeout := time.After(4 * time.Second)
	for len(received) < 2 {
		select {
		case msg := <-ch:
			received = append(received, msg)
		case <-timeout:
			t.Fatalf("timed out waiting for messages, got %d: %v", len(received), received)
		}
	}

	if len(received) != 2 {
		t.Fatalf("expected 2 messages, got %d: %v", len(received), received)
	}
	if received[0] != "Hi there!" {
		t.Errorf("first message = %q, want %q", received[0], "Hi there!")
	}
	if received[1] != "How can I help?" {
		t.Errorf("second message = %q, want %q", received[1], "How can I help?")
	}
}

func TestWatcher_DeduplicatesUUIDs(t *testing.T) {
	dir := t.TempDir()

	sessionFile := filepath.Join(dir, "test-session.jsonl")
	// Write the same UUID twice.
	content := strings.Join([]string{
		`{"type":"assistant","uuid":"dup-1","message":{"content":[{"type":"text","text":"First"}]}}`,
		`{"type":"assistant","uuid":"dup-1","message":{"content":[{"type":"text","text":"First"}]}}`,
		`{"type":"assistant","uuid":"dup-2","message":{"content":[{"type":"text","text":"Second"}]}}`,
	}, "\n") + "\n"

	if err := os.WriteFile(sessionFile, []byte(content), 0o644); err != nil {
		t.Fatalf("writing session file: %v", err)
	}

	w := &Watcher{
		projectDir: dir,
		logger:     log.New(os.Stderr, "test: ", 0),
		seen:       make(map[string]bool),
	}

	// Read all lines through readNewLines.
	out := make(chan string, 16)
	ctx := context.Background()
	w.readNewLines(ctx, sessionFile, 0, out)
	close(out)

	var received []string
	for msg := range out {
		received = append(received, msg)
	}

	if len(received) != 2 {
		t.Fatalf("expected 2 messages (deduped), got %d: %v", len(received), received)
	}
}

func TestWatcher_HandlesTruncation(t *testing.T) {
	dir := t.TempDir()

	sessionFile := filepath.Join(dir, "test-session.jsonl")
	initial := `{"type":"assistant","uuid":"t1","message":{"content":[{"type":"text","text":"Before truncation"}]}}` + "\n"
	if err := os.WriteFile(sessionFile, []byte(initial), 0o644); err != nil {
		t.Fatalf("writing session file: %v", err)
	}

	w := &Watcher{
		projectDir: dir,
		logger:     log.New(os.Stderr, "test: ", 0),
		seen:       make(map[string]bool),
	}

	out := make(chan string, 16)
	ctx := context.Background()

	// Read with offset beyond file size (simulates truncation).
	newOffset := w.readNewLines(ctx, sessionFile, 99999, out)

	// Should reset to 0 and read from start.
	if newOffset == 99999 {
		t.Error("expected offset to change after truncation detection")
	}
}
