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
	// System messages should still return empty text.
	line := `{"type":"system","uuid":"ghi-789","message":{"content":[{"type":"text","text":"system prompt"}]}}`
	text, _, ok := ParseLine(line)
	if !ok {
		t.Fatal("expected ok=true for valid JSON")
	}
	if text != "" {
		t.Errorf("expected empty text for system message, got %q", text)
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
	systemLine := `{"type":"system","message":{"content":[{"type":"text","text":"System prompt goes here."}]}}` + "\n"
	if err := os.WriteFile(sessionFile, []byte(systemLine), 0o644); err != nil {
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

	// Collect messages from the channel. Now includes the human message too.
	var received []string
	timeout := time.After(4 * time.Second)
	for len(received) < 3 {
		select {
		case msg := <-ch:
			received = append(received, msg)
		case <-timeout:
			t.Fatalf("timed out waiting for messages, got %d: %v", len(received), received)
		}
	}

	if len(received) != 3 {
		t.Fatalf("expected 3 messages, got %d: %v", len(received), received)
	}
	if received[0] != "hello" {
		t.Errorf("first message = %q, want %q", received[0], "hello")
	}
	if received[1] != "Hi there!" {
		t.Errorf("second message = %q, want %q", received[1], "Hi there!")
	}
	if received[2] != "How can I help?" {
		t.Errorf("third message = %q, want %q", received[2], "How can I help?")
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

// TestWatcher_PartialLineAcrossReads verifies that a JSONL line split across
// two filesystem flushes (and thus two poll cycles) is correctly reassembled.
func TestWatcher_PartialLineAcrossReads(t *testing.T) {
	dir := t.TempDir()
	sessionFile := filepath.Join(dir, "session.jsonl")

	// Write first half of a JSONL line (no trailing newline).
	firstHalf := `{"type":"assistant","uuid":"partial-1","message":{"content":[{"type":"text",`
	if err := os.WriteFile(sessionFile, []byte(firstHalf), 0o644); err != nil {
		t.Fatal(err)
	}

	w := &Watcher{
		projectDir: dir,
		logger:     log.New(os.Stderr, "test: ", 0),
		seen:       make(map[string]bool),
	}

	out := make(chan string, 16)
	ctx := context.Background()

	// First read — should buffer the partial line, emit nothing.
	offset := w.readNewLines(ctx, sessionFile, 0, out)

	select {
	case msg := <-out:
		t.Fatalf("unexpected message from partial read: %q", msg)
	default:
	}

	if w.partialLine == "" {
		t.Fatal("expected partialLine to be non-empty after reading incomplete data")
	}

	// Append the second half with a trailing newline.
	secondHalf := `"text":"Complete message!"}]}}` + "\n"
	f, err := os.OpenFile(sessionFile, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(f, secondHalf)
	f.Close()

	// Second read — should reassemble the line and emit the message.
	w.readNewLines(ctx, sessionFile, offset, out)

	select {
	case msg := <-out:
		if msg != "Complete message!" {
			t.Errorf("got %q, want %q", msg, "Complete message!")
		}
	default:
		t.Fatal("expected message after completing partial line")
	}
}

// TestWatcher_PartialLineThenMoreLines verifies partial line handling when
// more complete lines follow the completed partial.
func TestWatcher_PartialLineThenMoreLines(t *testing.T) {
	dir := t.TempDir()
	sessionFile := filepath.Join(dir, "session.jsonl")

	// Write a partial line.
	firstHalf := `{"type":"assistant","uuid":"p1","message":{"content":[{"type":"text",`
	if err := os.WriteFile(sessionFile, []byte(firstHalf), 0o644); err != nil {
		t.Fatal(err)
	}

	w := &Watcher{
		projectDir: dir,
		logger:     log.New(os.Stderr, "test: ", 0),
		seen:       make(map[string]bool),
	}

	out := make(chan string, 16)
	ctx := context.Background()

	// First read — buffers partial line.
	offset := w.readNewLines(ctx, sessionFile, 0, out)

	// Append: rest of line + newline + another complete message + newline.
	rest := `"text":"Reassembled"}]}}` + "\n" +
		`{"type":"assistant","uuid":"p2","message":{"content":[{"type":"text","text":"Second msg"}]}}` + "\n"
	f, err := os.OpenFile(sessionFile, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(f, rest)
	f.Close()

	// Second read.
	w.readNewLines(ctx, sessionFile, offset, out)

	var received []string
	for {
		select {
		case msg := <-out:
			received = append(received, msg)
		default:
			goto done
		}
	}
done:
	if len(received) != 2 {
		t.Fatalf("expected 2 messages, got %d: %v", len(received), received)
	}
	if received[0] != "Reassembled" {
		t.Errorf("first message = %q, want %q", received[0], "Reassembled")
	}
	if received[1] != "Second msg" {
		t.Errorf("second message = %q, want %q", received[1], "Second msg")
	}
}

// TestWatcher_StartupReadsRecentMessages verifies that when the watcher
// discovers a session file with existing content, it reads the last 4KB
// window instead of seeking to the exact end.
func TestWatcher_StartupReadsRecentMessages(t *testing.T) {
	dir := t.TempDir()
	sessionFile := filepath.Join(dir, "session.jsonl")

	// Create a large old message so the 4KB startup window only captures
	// the most recent message (old message body > 4KB).
	oldText := strings.Repeat("x", 5000)
	oldLine := fmt.Sprintf(
		`{"type":"assistant","uuid":"old-1","message":{"content":[{"type":"text","text":"%s"}]}}`,
		oldText,
	) + "\n"
	recentLine := `{"type":"assistant","uuid":"recent-1","message":{"content":[{"type":"text","text":"Recent message"}]}}` + "\n"

	if err := os.WriteFile(sessionFile, []byte(oldLine+recentLine), 0o644); err != nil {
		t.Fatal(err)
	}

	// Simulate what watchLoop does on startup: compute offset with the
	// 4KB window and seekToLineStart.
	info, err := os.Stat(sessionFile)
	if err != nil {
		t.Fatal(err)
	}

	startOffset := info.Size() - startupWindow
	if startOffset < 0 {
		startOffset = 0
	}

	adjusted, err := SeekToLineStart(sessionFile, startOffset)
	if err != nil {
		t.Fatal(err)
	}

	w := &Watcher{
		projectDir: dir,
		logger:     log.New(os.Stderr, "test: ", 0),
		seen:       make(map[string]bool),
	}

	out := make(chan string, 16)
	ctx := context.Background()
	w.readNewLines(ctx, sessionFile, adjusted, out)

	// Should have received the recent message.
	select {
	case msg := <-out:
		if msg != "Recent message" {
			t.Errorf("got %q, want %q", msg, "Recent message")
		}
	default:
		t.Fatal("expected to receive recent message from startup window")
	}

	// Should NOT have received the old message (it's before the window).
	select {
	case msg := <-out:
		t.Errorf("unexpected extra message: %q (len=%d)", msg[:min(50, len(msg))], len(msg))
	default:
		// good — only the recent message was captured
	}
}

// TestWatcher_StartupSmallFile verifies that a file smaller than the
// startup window is read from the beginning.
func TestWatcher_StartupSmallFile(t *testing.T) {
	dir := t.TempDir()
	sessionFile := filepath.Join(dir, "session.jsonl")

	line := `{"type":"assistant","uuid":"s1","message":{"content":[{"type":"text","text":"Small file"}]}}` + "\n"
	if err := os.WriteFile(sessionFile, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(sessionFile)
	if err != nil {
		t.Fatal(err)
	}

	startOffset := info.Size() - startupWindow
	if startOffset < 0 {
		startOffset = 0
	}
	// startOffset should be 0 for a small file.
	if startOffset != 0 {
		t.Fatalf("expected startOffset=0 for small file, got %d", startOffset)
	}

	w := &Watcher{
		projectDir: dir,
		logger:     log.New(os.Stderr, "test: ", 0),
		seen:       make(map[string]bool),
	}

	out := make(chan string, 16)
	ctx := context.Background()
	w.readNewLines(ctx, sessionFile, startOffset, out)

	select {
	case msg := <-out:
		if msg != "Small file" {
			t.Errorf("got %q, want %q", msg, "Small file")
		}
	default:
		t.Fatal("expected to receive message from small file")
	}
}

// TestWatcher_LargeMessage verifies that JSONL lines larger than 64KB
// (the default bufio.Scanner limit) are handled correctly.
func TestWatcher_LargeMessage(t *testing.T) {
	dir := t.TempDir()
	sessionFile := filepath.Join(dir, "session.jsonl")

	// Create a message larger than 64KB.
	largeText := strings.Repeat("A", 100*1024) // 100KB of text
	line := fmt.Sprintf(
		`{"type":"assistant","uuid":"large-1","message":{"content":[{"type":"text","text":"%s"}]}}`,
		largeText,
	) + "\n"

	if err := os.WriteFile(sessionFile, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	w := &Watcher{
		projectDir: dir,
		logger:     log.New(os.Stderr, "test: ", 0),
		seen:       make(map[string]bool),
	}

	out := make(chan string, 16)
	ctx := context.Background()
	w.readNewLines(ctx, sessionFile, 0, out)

	select {
	case msg := <-out:
		if msg != largeText {
			t.Errorf("large message length = %d, want %d", len(msg), len(largeText))
		}
	default:
		t.Fatal("expected large message to be read successfully")
	}
}

// TestWatcher_UUIDDeduplicationAcrossReads verifies that the UUID dedup
// map persists across multiple readNewLines calls, preventing re-sends
// when the startup window overlaps with previously seen messages.
func TestWatcher_UUIDDeduplicationAcrossReads(t *testing.T) {
	dir := t.TempDir()
	sessionFile := filepath.Join(dir, "session.jsonl")

	line1 := `{"type":"assistant","uuid":"dedup-1","message":{"content":[{"type":"text","text":"First"}]}}` + "\n"
	if err := os.WriteFile(sessionFile, []byte(line1), 0o644); err != nil {
		t.Fatal(err)
	}

	w := &Watcher{
		projectDir: dir,
		logger:     log.New(os.Stderr, "test: ", 0),
		seen:       make(map[string]bool),
	}

	out := make(chan string, 16)
	ctx := context.Background()

	// First read — message is new.
	offset := w.readNewLines(ctx, sessionFile, 0, out)

	select {
	case msg := <-out:
		if msg != "First" {
			t.Errorf("got %q, want %q", msg, "First")
		}
	default:
		t.Fatal("expected message on first read")
	}

	// Append a duplicate UUID and a new message.
	more := `{"type":"assistant","uuid":"dedup-1","message":{"content":[{"type":"text","text":"Duplicate"}]}}` + "\n" +
		`{"type":"assistant","uuid":"dedup-2","message":{"content":[{"type":"text","text":"New"}]}}` + "\n"
	f, err := os.OpenFile(sessionFile, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(f, more)
	f.Close()

	// Second read — duplicate should be skipped, new one emitted.
	w.readNewLines(ctx, sessionFile, offset, out)

	var received []string
	for {
		select {
		case msg := <-out:
			received = append(received, msg)
		default:
			goto done
		}
	}
done:
	if len(received) != 1 {
		t.Fatalf("expected 1 message (dedup should skip duplicate), got %d: %v", len(received), received)
	}
	if received[0] != "New" {
		t.Errorf("got %q, want %q", received[0], "New")
	}
}

// --- Recency-based session detection tests ---

// TestFindActiveSession_ReturnsNewestRegardlessOfContent verifies that
// findActiveSession returns the most recently modified .jsonl file even
// when it doesn't contain any Woland-identifying keywords.
func TestFindActiveSession_ReturnsNewestRegardlessOfContent(t *testing.T) {
	dir := t.TempDir()

	// Create an older file that mentions "Woland".
	olderPath := filepath.Join(dir, "older-session.jsonl")
	olderContent := `{"type":"system","message":{"content":[{"type":"text","text":"You are Woland."}]}}` + "\n"
	if err := os.WriteFile(olderPath, []byte(olderContent), 0o644); err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)

	// Create a newer file with NO Woland keywords — just ordinary messages.
	newerPath := filepath.Join(dir, "newer-session.jsonl")
	newerContent := strings.Join([]string{
		`{"type":"human","uuid":"h1","message":{"content":[{"type":"text","text":"stepping away"}]}}`,
		`{"type":"assistant","uuid":"a1","message":{"content":[{"type":"text","text":"phone bridge active"}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(newerPath, []byte(newerContent), 0o644); err != nil {
		t.Fatal(err)
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

	// Should pick the newer file regardless of keyword content.
	if got != newerPath {
		t.Errorf("expected %q (newest file), got %q", newerPath, got)
	}
}

// TestWatcher_SessionSwitchOnNewFile verifies that when aptPath is set, the
// watcher locks onto the first session via a marker file and does NOT switch
// when a newer file appears. A switch only happens after the marker is deleted.
func TestWatcher_SessionSwitchOnNewFile(t *testing.T) {
	aptDir := t.TempDir()
	projDir := filepath.Join(aptDir, "projects")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create an initial session file (file A) with a system line only.
	fileA := filepath.Join(projDir, "session-a.jsonl")
	systemLine := `{"type":"system","message":{"content":[{"type":"text","text":"System prompt."}]}}` + "\n"
	if err := os.WriteFile(fileA, []byte(systemLine), 0o644); err != nil {
		t.Fatal(err)
	}

	w := &Watcher{
		projectDir: projDir,
		aptPath:    aptDir,
		logger:     log.New(os.Stderr, "test: ", 0),
		seen:       make(map[string]bool),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	sessionSwitch := make(chan struct{}, 4)
	ch := w.Watch(ctx, sessionSwitch)

	// Give the watcher time to discover file A and drain the startup window.
	time.Sleep(500 * time.Millisecond)

	// Append an assistant message to file A AFTER the watcher has started.
	f, err := os.OpenFile(fileA, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(f, `{"type":"assistant","uuid":"a1","message":{"content":[{"type":"text","text":"Message from A"}]}}`)
	f.Close()

	// Wait for the watcher to forward the message from file A.
	select {
	case msg := <-ch:
		if msg != "Message from A" {
			t.Errorf("expected %q from file A, got %q", "Message from A", msg)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("timed out waiting for message from file A")
	}

	// Verify the marker file was written.
	markerPath := filepath.Join(aptDir, ".woland-session")
	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("expected marker file to exist: %v", err)
	}
	if strings.TrimSpace(string(data)) != fileA {
		t.Errorf("marker contains %q, want %q", string(data), fileA)
	}

	// Create file B — a newer session file (simulates a worker agent).
	// Only write a system line initially so the content isn't drained.
	time.Sleep(100 * time.Millisecond)
	fileB := filepath.Join(projDir, "session-b.jsonl")
	if err := os.WriteFile(fileB, []byte(systemLine), 0o644); err != nil {
		t.Fatal(err)
	}

	// Wait long enough for several poll cycles — no switch should happen.
	select {
	case <-sessionSwitch:
		t.Fatal("unexpected session switch — marker should prevent switching to newer file")
	case <-time.After(5 * time.Second):
		// Good — no switch.
	}

	// Now delete the marker (simulates Woland restart) to trigger re-scan.
	if err := os.Remove(markerPath); err != nil {
		t.Fatalf("removing marker: %v", err)
	}

	// The watcher should re-scan, find file B (newest), and switch.
	select {
	case <-sessionSwitch:
		// Good — session switch detected after marker deletion.
	case <-time.After(6 * time.Second):
		t.Fatal("timed out waiting for session switch after marker deletion")
	}

	// Append an assistant message to file B AFTER the watcher has switched.
	fb, err := os.OpenFile(fileB, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(fb, `{"type":"assistant","uuid":"b1","message":{"content":[{"type":"text","text":"Message from B"}]}}`)
	fb.Close()

	// Should receive the message from file B.
	select {
	case msg := <-ch:
		if msg != "Message from B" {
			t.Errorf("expected %q from file B, got %q", "Message from B", msg)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("timed out waiting for message from file B")
	}

	cancel()
	for range ch {
	}
}

// TestSeekToLineStart verifies that seekToLineStart correctly finds the
// first complete line boundary after a given offset.
func TestSeekToLineStart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")

	// File content: "aaaa\nbbbb\ncccc\n"
	//                01234 56789 ...
	content := "aaaa\nbbbb\ncccc\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// seekToLineStart skips forward from offset to the byte after the first
	// newline it finds. offset=0 is a special case that returns 0 immediately.
	tests := []struct {
		name       string
		offset     int64
		wantOffset int64
	}{
		{"at zero", 0, 0},                         // special case, no skip
		{"mid first line", 1, 5},                   // reads "aaa\n" (4 bytes) → 1+4=5
		{"start of second line", 5, 10},            // reads "bbbb\n" → 5+5=10
		{"mid second line", 7, 10},                 // reads "bb\n" → 7+3=10
		{"start of third line", 10, 15},            // reads "cccc\n" → 10+5=15
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SeekToLineStart(path, tt.offset)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.wantOffset {
				t.Errorf("SeekToLineStart(offset=%d) = %d, want %d", tt.offset, got, tt.wantOffset)
			}
		})
	}
}

// TestWatcher_StartupDrainDoesNotForward verifies that existing assistant
// messages in a session file are NOT forwarded when the watcher first
// attaches (draining mode). Their UUIDs should be recorded in the seen
// map for deduplication, but the out channel should remain empty.
func TestWatcher_StartupDrainDoesNotForward(t *testing.T) {
	dir := t.TempDir()
	sessionFile := filepath.Join(dir, "session.jsonl")

	// Pre-populate the session file with a system prompt and two assistant
	// messages that exist BEFORE the watcher starts.
	content := strings.Join([]string{
		`{"type":"system","message":{"content":[{"type":"text","text":"You are Woland, the planning agent."}]}}`,
		`{"type":"assistant","uuid":"stale-1","message":{"content":[{"type":"text","text":"Old message 1"}]}}`,
		`{"type":"assistant","uuid":"stale-2","message":{"content":[{"type":"text","text":"Old message 2"}]}}`,
	}, "\n") + "\n"

	if err := os.WriteFile(sessionFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	w := &Watcher{
		projectDir: dir,
		logger:     log.New(os.Stderr, "test: ", 0),
		seen:       make(map[string]bool),
	}

	out := make(chan string, 16)
	ctx := context.Background()

	// Simulate the startup drain: set draining=true, read, then clear it.
	w.draining = true
	w.readNewLines(ctx, sessionFile, 0, out)
	w.draining = false

	// The out channel should be empty — no stale messages forwarded.
	select {
	case msg := <-out:
		t.Errorf("unexpected message during drain: %q", msg)
	default:
		// good — no messages forwarded
	}

	// But the UUIDs should be recorded in the seen map.
	if !w.seen["stale-1"] {
		t.Error("expected UUID 'stale-1' to be in seen map after drain")
	}
	if !w.seen["stale-2"] {
		t.Error("expected UUID 'stale-2' to be in seen map after drain")
	}
}

// TestWatcher_MessagesAfterStartupAreForwarded verifies that assistant
// messages written AFTER the watcher has started (and finished draining)
// are correctly forwarded to the out channel, and that stale messages
// from before startup are not re-sent even if their UUIDs reappear.
func TestWatcher_MessagesAfterStartupAreForwarded(t *testing.T) {
	dir := t.TempDir()
	sessionFile := filepath.Join(dir, "session.jsonl")

	// Pre-populate with a system prompt and one existing assistant message.
	initial := strings.Join([]string{
		`{"type":"system","message":{"content":[{"type":"text","text":"You are Woland, the planning agent."}]}}`,
		`{"type":"assistant","uuid":"pre-1","message":{"content":[{"type":"text","text":"Pre-existing message"}]}}`,
	}, "\n") + "\n"

	if err := os.WriteFile(sessionFile, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	w := &Watcher{
		projectDir: dir,
		logger:     log.New(os.Stderr, "test: ", 0),
		seen:       make(map[string]bool),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch := w.Watch(ctx, nil)

	// Give the watcher time to start, find the file, and drain.
	time.Sleep(500 * time.Millisecond)

	// Verify no stale message was forwarded during startup.
	select {
	case msg := <-ch:
		t.Fatalf("unexpected stale message on startup: %q", msg)
	default:
		// good — startup drain suppressed stale messages
	}

	// Now append new messages AFTER the watcher has started.
	f, err := os.OpenFile(sessionFile, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(f, `{"type":"assistant","uuid":"new-1","message":{"content":[{"type":"text","text":"Fresh message"}]}}`)
	fmt.Fprintln(f, `{"type":"assistant","uuid":"pre-1","message":{"content":[{"type":"text","text":"Pre-existing message"}]}}`)
	f.Close()

	// Collect messages — should only get the new one, not the duplicate.
	var received []string
	timeout := time.After(4 * time.Second)
	for len(received) < 1 {
		select {
		case msg := <-ch:
			received = append(received, msg)
		case <-timeout:
			t.Fatalf("timed out waiting for new message, got %d: %v", len(received), received)
		}
	}

	// Give a moment for any extra messages to arrive.
	time.Sleep(200 * time.Millisecond)

	// Drain any additional messages.
	for {
		select {
		case msg := <-ch:
			received = append(received, msg)
		default:
			goto done
		}
	}
done:

	if len(received) != 1 {
		t.Fatalf("expected 1 message (new only), got %d: %v", len(received), received)
	}
	if received[0] != "Fresh message" {
		t.Errorf("got %q, want %q", received[0], "Fresh message")
	}
}

// --- Marker file (.woland-session) tests ---

// TestFindActiveSession_WritesMarkerOnDiscovery verifies that findActiveSession
// writes a .woland-session marker file containing the discovered session path.
func TestFindActiveSession_WritesMarkerOnDiscovery(t *testing.T) {
	aptDir := t.TempDir()
	projDir := filepath.Join(aptDir, "projects")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	sessionFile := filepath.Join(projDir, "session.jsonl")
	if err := os.WriteFile(sessionFile, []byte(`{"type":"human"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	w := &Watcher{
		projectDir: projDir,
		aptPath:    aptDir,
		logger:     log.New(os.Stderr, "test: ", 0),
		seen:       make(map[string]bool),
	}

	got, err := w.findActiveSession()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != sessionFile {
		t.Errorf("findActiveSession() = %q, want %q", got, sessionFile)
	}

	// Verify marker was written.
	markerPath := filepath.Join(aptDir, wolandSessionMarker)
	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("expected marker file at %s: %v", markerPath, err)
	}
	if string(data) != sessionFile {
		t.Errorf("marker contains %q, want %q", string(data), sessionFile)
	}
}

// TestFindActiveSession_ReadsMarkerOnSubsequentPolls verifies that when a
// marker file exists and points to a valid session, findActiveSession returns
// the marker's path even when a newer .jsonl file exists.
func TestFindActiveSession_ReadsMarkerOnSubsequentPolls(t *testing.T) {
	aptDir := t.TempDir()
	projDir := filepath.Join(aptDir, "projects")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create the Woland session file.
	wolandFile := filepath.Join(projDir, "woland-session.jsonl")
	if err := os.WriteFile(wolandFile, []byte(`{"type":"human"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Write the marker pointing to the Woland session.
	markerPath := filepath.Join(aptDir, wolandSessionMarker)
	if err := os.WriteFile(markerPath, []byte(wolandFile), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a newer file (simulates a worker agent session).
	time.Sleep(50 * time.Millisecond)
	workerFile := filepath.Join(projDir, "worker-session.jsonl")
	if err := os.WriteFile(workerFile, []byte(`{"type":"human"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	w := &Watcher{
		projectDir: projDir,
		aptPath:    aptDir,
		logger:     log.New(os.Stderr, "test: ", 0),
		seen:       make(map[string]bool),
	}

	got, err := w.findActiveSession()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should return the marker's file, NOT the newer worker file.
	if got != wolandFile {
		t.Errorf("findActiveSession() = %q, want %q (marker file should take precedence)", got, wolandFile)
	}
}

// TestFindActiveSession_MarkerDeleteCausesRescan verifies that deleting the
// marker file causes findActiveSession to re-scan the directory, find the
// newest file, and write a new marker.
func TestFindActiveSession_MarkerDeleteCausesRescan(t *testing.T) {
	aptDir := t.TempDir()
	projDir := filepath.Join(aptDir, "projects")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create an older file.
	olderFile := filepath.Join(projDir, "older.jsonl")
	if err := os.WriteFile(olderFile, []byte(`{"type":"human"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)

	// Create a newer file.
	newerFile := filepath.Join(projDir, "newer.jsonl")
	if err := os.WriteFile(newerFile, []byte(`{"type":"human"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	w := &Watcher{
		projectDir: projDir,
		aptPath:    aptDir,
		logger:     log.New(os.Stderr, "test: ", 0),
		seen:       make(map[string]bool),
	}

	// No marker exists — should scan, find newest, and write marker.
	got, err := w.findActiveSession()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != newerFile {
		t.Errorf("first call: got %q, want %q", got, newerFile)
	}

	// Verify marker was written.
	markerPath := filepath.Join(aptDir, wolandSessionMarker)
	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("marker should exist: %v", err)
	}
	if string(data) != newerFile {
		t.Errorf("marker = %q, want %q", string(data), newerFile)
	}

	// Delete the marker (simulates Woland restart).
	if err := os.Remove(markerPath); err != nil {
		t.Fatal(err)
	}

	// Create an even newer file.
	time.Sleep(50 * time.Millisecond)
	newestFile := filepath.Join(projDir, "newest.jsonl")
	if err := os.WriteFile(newestFile, []byte(`{"type":"human"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Should re-scan and find the newest file.
	got, err = w.findActiveSession()
	if err != nil {
		t.Fatalf("unexpected error after marker delete: %v", err)
	}
	if got != newestFile {
		t.Errorf("after marker delete: got %q, want %q", got, newestFile)
	}

	// Verify new marker was written.
	data, err = os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("new marker should exist: %v", err)
	}
	if string(data) != newestFile {
		t.Errorf("new marker = %q, want %q", string(data), newestFile)
	}
}

// TestFindActiveSession_StaleMarkerRemovedAndRescans verifies that if the
// marker points to a file that no longer exists, it is removed and a
// re-scan occurs.
func TestFindActiveSession_StaleMarkerRemovedAndRescans(t *testing.T) {
	aptDir := t.TempDir()
	projDir := filepath.Join(aptDir, "projects")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write a marker pointing to a nonexistent file.
	markerPath := filepath.Join(aptDir, wolandSessionMarker)
	if err := os.WriteFile(markerPath, []byte("/nonexistent/session.jsonl"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a real session file.
	realFile := filepath.Join(projDir, "real-session.jsonl")
	if err := os.WriteFile(realFile, []byte(`{"type":"human"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	w := &Watcher{
		projectDir: projDir,
		aptPath:    aptDir,
		logger:     log.New(os.Stderr, "test: ", 0),
		seen:       make(map[string]bool),
	}

	got, err := w.findActiveSession()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != realFile {
		t.Errorf("got %q, want %q (should fall back to scan)", got, realFile)
	}

	// Verify the stale marker was replaced.
	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("marker should still exist: %v", err)
	}
	if string(data) != realFile {
		t.Errorf("updated marker = %q, want %q", string(data), realFile)
	}
}

// TestFindActiveSession_NoMarkerWithoutAptPath verifies that when aptPath
// is empty (e.g., in tests that construct Watcher directly), the marker
// file logic is skipped entirely and the watcher falls back to scanning.
func TestFindActiveSession_NoMarkerWithoutAptPath(t *testing.T) {
	dir := t.TempDir()

	file1 := filepath.Join(dir, "a.jsonl")
	if err := os.WriteFile(file1, []byte(`{"type":"human"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)

	file2 := filepath.Join(dir, "b.jsonl")
	if err := os.WriteFile(file2, []byte(`{"type":"human"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	w := &Watcher{
		projectDir: dir,
		// aptPath intentionally empty — no marker logic.
		logger: log.New(os.Stderr, "test: ", 0),
		seen:   make(map[string]bool),
	}

	got, err := w.findActiveSession()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != file2 {
		t.Errorf("got %q, want %q (newest file)", got, file2)
	}

	// No marker file should have been created.
	markerPath := filepath.Join(dir, wolandSessionMarker)
	if _, err := os.Stat(markerPath); err == nil {
		t.Error("marker file should NOT be created when aptPath is empty")
	}
}

// TestFindActiveSession_FallbackLogsWarningWithAptPath verifies that when
// aptPath is set but no marker file exists, findActiveSession logs a warning
// about falling back to the newest-file scan.
func TestFindActiveSession_FallbackLogsWarningWithAptPath(t *testing.T) {
	aptDir := t.TempDir()
	projDir := filepath.Join(aptDir, "projects")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	sessionFile := filepath.Join(projDir, "session.jsonl")
	if err := os.WriteFile(sessionFile, []byte(`{"type":"human"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var logBuf strings.Builder
	logger := log.New(&logBuf, "", 0)

	w := &Watcher{
		projectDir: projDir,
		aptPath:    aptDir,
		logger:     logger,
		seen:       make(map[string]bool),
	}

	got, err := w.findActiveSession()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != sessionFile {
		t.Errorf("got %q, want %q", got, sessionFile)
	}

	// Verify warning was logged about missing marker.
	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "no Woland session marker found") {
		t.Errorf("expected warning about missing marker in log, got: %q", logOutput)
	}
}

// TestFindActiveSession_NoFallbackWarningWithoutAptPath verifies that when
// aptPath is empty, no warning about missing marker is logged.
func TestFindActiveSession_NoFallbackWarningWithoutAptPath(t *testing.T) {
	dir := t.TempDir()

	sessionFile := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(sessionFile, []byte(`{"type":"human"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var logBuf strings.Builder
	logger := log.New(&logBuf, "", 0)

	w := &Watcher{
		projectDir: dir,
		// aptPath intentionally empty.
		logger: logger,
		seen:   make(map[string]bool),
	}

	_, err := w.findActiveSession()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	logOutput := logBuf.String()
	if strings.Contains(logOutput, "no Woland session marker found") {
		t.Errorf("warning should NOT be logged when aptPath is empty, got: %q", logOutput)
	}
}

// TestFindActiveSession_WolandMarkerSurvivesMultipleAgentFiles verifies that
// when a marker exists, the watcher ignores multiple newer agent session files.
// This is the specific bug scenario: standing agents creating session files
// that are newer than Woland's.
func TestFindActiveSession_WolandMarkerSurvivesMultipleAgentFiles(t *testing.T) {
	aptDir := t.TempDir()
	projDir := filepath.Join(aptDir, "projects")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Woland's session file.
	wolandFile := filepath.Join(projDir, "woland-session.jsonl")
	if err := os.WriteFile(wolandFile, []byte(`{"type":"system"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Write marker pointing to Woland's file (as woland.go now does).
	markerPath := filepath.Join(aptDir, wolandSessionMarker)
	if err := os.WriteFile(markerPath, []byte(wolandFile), 0o644); err != nil {
		t.Fatal(err)
	}

	// Multiple standing agents create newer session files.
	for i := 0; i < 3; i++ {
		time.Sleep(50 * time.Millisecond)
		agentFile := filepath.Join(projDir, fmt.Sprintf("agent-%d-session.jsonl", i))
		if err := os.WriteFile(agentFile, []byte(`{"type":"system"}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	w := &Watcher{
		projectDir: projDir,
		aptPath:    aptDir,
		logger:     log.New(os.Stderr, "test: ", 0),
		seen:       make(map[string]bool),
	}

	// Call findActiveSession multiple times to simulate polling.
	for i := 0; i < 5; i++ {
		got, err := w.findActiveSession()
		if err != nil {
			t.Fatalf("poll %d: unexpected error: %v", i, err)
		}
		if got != wolandFile {
			t.Errorf("poll %d: got %q, want %q (marker should hold)", i, got, wolandFile)
		}
	}
}

// --- User message parsing tests ---

// TestParseLine_UserMessageStringContent verifies that user messages with
// plain string content (the common format in Claude Code sessions) are
// correctly parsed and their text is extracted.
func TestParseLine_UserMessageStringContent(t *testing.T) {
	line := `{"type":"user","uuid":"u1","message":{"role":"user","content":"tell me a joke"}}`
	text, uuid, ok := ParseLine(line)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if uuid != "u1" {
		t.Errorf("expected uuid 'u1', got %q", uuid)
	}
	if text != "tell me a joke" {
		t.Errorf("expected text 'tell me a joke', got %q", text)
	}
}

// TestParseLine_UserMessageArrayContent verifies that user messages with
// array content (containing text blocks) are correctly parsed.
func TestParseLine_UserMessageArrayContent(t *testing.T) {
	line := `{"type":"user","uuid":"u2","message":{"role":"user","content":[{"type":"text","text":"hello from user"}]}}`
	text, uuid, ok := ParseLine(line)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if uuid != "u2" {
		t.Errorf("expected uuid 'u2', got %q", uuid)
	}
	if text != "hello from user" {
		t.Errorf("expected text 'hello from user', got %q", text)
	}
}

// TestParseLine_UserMessageToolResultOnly verifies that user messages
// containing only tool_result blocks (no text blocks) return empty text
// and are NOT propagated.
func TestParseLine_UserMessageToolResultOnly(t *testing.T) {
	line := `{"type":"user","uuid":"u3","message":{"role":"user","content":[{"type":"tool_result","text":"tool output data"}]}}`
	text, uuid, ok := ParseLine(line)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if uuid != "u3" {
		t.Errorf("expected uuid 'u3', got %q", uuid)
	}
	if text != "" {
		t.Errorf("expected empty text for tool_result-only message, got %q", text)
	}
}

// TestParseLine_UserMessageMixedContent verifies that user messages with
// a mix of text and tool_result blocks only extract the text blocks.
func TestParseLine_UserMessageMixedContent(t *testing.T) {
	line := `{"type":"user","uuid":"u4","message":{"role":"user","content":[{"type":"tool_result","text":"internal data"},{"type":"text","text":"actual user text"}]}}`
	text, uuid, ok := ParseLine(line)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if uuid != "u4" {
		t.Errorf("expected uuid 'u4', got %q", uuid)
	}
	if text != "actual user text" {
		t.Errorf("expected 'actual user text', got %q", text)
	}
}

// TestParseLine_HumanTypeAlsoWorks verifies that messages with type "human"
// (the legacy format) are still processed correctly.
func TestParseLine_HumanTypeAlsoWorks(t *testing.T) {
	line := `{"type":"human","uuid":"h1","message":{"role":"user","content":"legacy human input"}}`
	text, uuid, ok := ParseLine(line)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if uuid != "h1" {
		t.Errorf("expected uuid 'h1', got %q", uuid)
	}
	if text != "legacy human input" {
		t.Errorf("expected 'legacy human input', got %q", text)
	}
}

// TestParseLine_AssistantStillWorks verifies that assistant message parsing
// is not broken by the user message changes.
func TestParseLine_AssistantStillWorks(t *testing.T) {
	line := `{"type":"assistant","uuid":"a1","message":{"content":[{"type":"text","text":"Hello!"},{"type":"tool_use","text":"ignored"},{"type":"text","text":"World!"}]}}`
	text, uuid, ok := ParseLine(line)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if uuid != "a1" {
		t.Errorf("expected uuid 'a1', got %q", uuid)
	}
	if text != "Hello!\nWorld!" {
		t.Errorf("expected 'Hello!\\nWorld!', got %q", text)
	}
}

// TestParseLine_SystemTypeSkipped verifies that system messages are still
// skipped (not processed for text extraction).
func TestParseLine_SystemTypeSkipped(t *testing.T) {
	line := `{"type":"system","uuid":"s1","message":{"content":[{"type":"text","text":"system prompt"}]}}`
	text, _, ok := ParseLine(line)
	if !ok {
		t.Fatal("expected ok=true for valid JSON")
	}
	if text != "" {
		t.Errorf("expected empty text for system message, got %q", text)
	}
}

// TestWatcher_UserMessagesFlowThroughReadNewLines verifies that user messages
// with string content are forwarded through readNewLines to the output channel.
func TestWatcher_UserMessagesFlowThroughReadNewLines(t *testing.T) {
	dir := t.TempDir()
	sessionFile := filepath.Join(dir, "session.jsonl")

	content := strings.Join([]string{
		`{"type":"user","uuid":"u1","message":{"role":"user","content":"hello Woland"}}`,
		`{"type":"assistant","uuid":"a1","message":{"content":[{"type":"text","text":"Hi there!"}]}}`,
		`{"type":"user","uuid":"u2","message":{"role":"user","content":"tester tell me a joke"}}`,
	}, "\n") + "\n"

	if err := os.WriteFile(sessionFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	w := &Watcher{
		projectDir: dir,
		logger:     log.New(os.Stderr, "test: ", 0),
		seen:       make(map[string]bool),
	}

	out := make(chan string, 16)
	ctx := context.Background()
	w.readNewLines(ctx, sessionFile, 0, out)
	close(out)

	var received []string
	for msg := range out {
		received = append(received, msg)
	}

	if len(received) != 3 {
		t.Fatalf("expected 3 messages (2 user + 1 assistant), got %d: %v", len(received), received)
	}
	if received[0] != "hello Woland" {
		t.Errorf("first message = %q, want %q", received[0], "hello Woland")
	}
	if received[1] != "Hi there!" {
		t.Errorf("second message = %q, want %q", received[1], "Hi there!")
	}
	if received[2] != "tester tell me a joke" {
		t.Errorf("third message = %q, want %q", received[2], "tester tell me a joke")
	}
}

// TestWatcher_ToolResultOnlyNotPropagated verifies that user messages containing
// only tool_result blocks (no text blocks) are NOT forwarded to the output channel.
func TestWatcher_ToolResultOnlyNotPropagated(t *testing.T) {
	dir := t.TempDir()
	sessionFile := filepath.Join(dir, "session.jsonl")

	content := strings.Join([]string{
		`{"type":"user","uuid":"u1","message":{"role":"user","content":[{"type":"tool_result","text":"internal tool output"}]}}`,
		`{"type":"assistant","uuid":"a1","message":{"content":[{"type":"text","text":"Real message"}]}}`,
	}, "\n") + "\n"

	if err := os.WriteFile(sessionFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	w := &Watcher{
		projectDir: dir,
		logger:     log.New(os.Stderr, "test: ", 0),
		seen:       make(map[string]bool),
	}

	out := make(chan string, 16)
	ctx := context.Background()
	w.readNewLines(ctx, sessionFile, 0, out)
	close(out)

	var received []string
	for msg := range out {
		received = append(received, msg)
	}

	// Only the assistant message should come through; tool_result-only user message is skipped.
	if len(received) != 1 {
		t.Fatalf("expected 1 message (tool_result should be filtered), got %d: %v", len(received), received)
	}
	if received[0] != "Real message" {
		t.Errorf("got %q, want %q", received[0], "Real message")
	}
}
