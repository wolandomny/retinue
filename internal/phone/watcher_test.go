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

	// Create an initial session file with a Woland system prompt so the
	// session filter recognises it as a Woland planning session.
	sessionFile := filepath.Join(dir, "test-session.jsonl")
	systemLine := `{"type":"system","message":{"content":[{"type":"text","text":"You are Woland, the planning agent."}]}}` + "\n"
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

// --- Woland session filtering tests ---

// wolandSessionContent returns realistic JSONL content for a Woland planning session.
func wolandSessionContent() string {
	return strings.Join([]string{
		`{"type":"system","message":{"content":[{"type":"text","text":"You are Woland, the master planning agent of the Retinue system. You coordinate with Koroviev and other agents to manage the apartment."}]}}`,
		`{"type":"human","uuid":"h1","message":{"content":[{"type":"text","text":"Hello Woland, what's the status?"}]}}`,
		`{"type":"assistant","uuid":"a1","message":{"content":[{"type":"text","text":"I'll check on things for you."}]}}`,
	}, "\n") + "\n"
}

// workerAgentContent returns realistic JSONL content for a worker agent session.
func workerAgentContent() string {
	return strings.Join([]string{
		`{"type":"system","message":{"content":[{"type":"text","text":"You are a worker agent in the Retinue system. Your task ID is \"fix-bug-123\". Complete the following task thoroughly and report your results."}]}}`,
		`{"type":"human","uuid":"h2","message":{"content":[{"type":"text","text":"Fix the bug in watcher.go"}]}}`,
		`{"type":"assistant","uuid":"a2","message":{"content":[{"type":"text","text":"I'll fix the bug now."}]}}`,
	}, "\n") + "\n"
}

func TestCheckWolandSession_WolandFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "woland.jsonl")
	if err := os.WriteFile(path, []byte(wolandSessionContent()), 0o644); err != nil {
		t.Fatal(err)
	}

	isWoland, conclusive := CheckWolandSession(path)
	if !isWoland {
		t.Error("expected isWoland=true for Woland session file")
	}
	if !conclusive {
		t.Error("expected conclusive=true for Woland session file")
	}
}

func TestCheckWolandSession_WorkerFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "worker.jsonl")
	if err := os.WriteFile(path, []byte(workerAgentContent()), 0o644); err != nil {
		t.Fatal(err)
	}

	isWoland, conclusive := CheckWolandSession(path)
	if isWoland {
		t.Error("expected isWoland=false for worker agent session file")
	}
	if !conclusive {
		t.Error("expected conclusive=true for worker agent file (has enough content)")
	}
}

func TestCheckWolandSession_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.jsonl")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	isWoland, conclusive := CheckWolandSession(path)
	if isWoland {
		t.Error("expected isWoland=false for empty file")
	}
	if conclusive {
		t.Error("expected conclusive=false for empty file")
	}
}

func TestCheckWolandSession_SmallFileInconclusive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "small.jsonl")
	// Content smaller than minBytesForConclusive without Woland keywords.
	if err := os.WriteFile(path, []byte(`{"type":"human"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	isWoland, conclusive := CheckWolandSession(path)
	if isWoland {
		t.Error("expected isWoland=false for file without Woland keywords")
	}
	if conclusive {
		t.Error("expected conclusive=false for small file (below minBytesForConclusive)")
	}
}

func TestCheckWolandSession_NonexistentFile(t *testing.T) {
	isWoland, conclusive := CheckWolandSession("/nonexistent/path/file.jsonl")
	if isWoland {
		t.Error("expected isWoland=false for nonexistent file")
	}
	if conclusive {
		t.Error("expected conclusive=false for nonexistent file")
	}
}

func TestCheckWolandSession_CaseInsensitive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mixed-case.jsonl")
	// Use mixed case to verify case-insensitive matching.
	content := strings.Repeat("x", 300) + "\n" + `{"type":"system","message":{"content":[{"type":"text","text":"You are WOLAND the planning agent."}]}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	isWoland, conclusive := CheckWolandSession(path)
	if !isWoland {
		t.Error("expected isWoland=true for file containing 'WOLAND' (case-insensitive)")
	}
	if !conclusive {
		t.Error("expected conclusive=true")
	}
}

func TestCheckWolandSession_KorovievKeyword(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "koroviev.jsonl")
	content := `{"type":"system","message":{"content":[{"type":"text","text":"Coordinate with Koroviev to dispatch tasks to worker agents."}]}}` + "\n"
	content += `{"type":"human","uuid":"h1","message":{"content":[{"type":"text","text":"hello"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	isWoland, conclusive := CheckWolandSession(path)
	if !isWoland {
		t.Error("expected isWoland=true for file containing 'Koroviev'")
	}
	if !conclusive {
		t.Error("expected conclusive=true")
	}
}

func TestFindActiveSession_FiltersWorkerAgents(t *testing.T) {
	dir := t.TempDir()

	// Create a Woland session file (older).
	wolandPath := filepath.Join(dir, "woland-session.jsonl")
	if err := os.WriteFile(wolandPath, []byte(wolandSessionContent()), 0o644); err != nil {
		t.Fatal(err)
	}

	// Wait so the worker file gets a newer modification time.
	time.Sleep(50 * time.Millisecond)

	// Create a worker agent session file (newer).
	workerPath := filepath.Join(dir, "worker-session.jsonl")
	if err := os.WriteFile(workerPath, []byte(workerAgentContent()), 0o644); err != nil {
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

	// Should pick the Woland file even though the worker file is newer.
	if got != wolandPath {
		t.Errorf("expected %q (Woland session), got %q", wolandPath, got)
	}
}

func TestFindActiveSession_WorkerOnlyReturnsEmpty(t *testing.T) {
	dir := t.TempDir()

	// Create only worker agent session files.
	for i := 0; i < 3; i++ {
		path := filepath.Join(dir, fmt.Sprintf("worker-%d.jsonl", i))
		if err := os.WriteFile(path, []byte(workerAgentContent()), 0o644); err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
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

	if got != "" {
		t.Errorf("expected empty string when only worker sessions exist, got %q", got)
	}
}

func TestFindActiveSession_MultipleWolandPicksNewest(t *testing.T) {
	dir := t.TempDir()

	// Create an older Woland session.
	oldWoland := filepath.Join(dir, "old-woland.jsonl")
	if err := os.WriteFile(oldWoland, []byte(wolandSessionContent()), 0o644); err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)

	// Create a worker session (should be filtered out).
	workerPath := filepath.Join(dir, "worker.jsonl")
	if err := os.WriteFile(workerPath, []byte(workerAgentContent()), 0o644); err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)

	// Create a newer Woland session.
	newWoland := filepath.Join(dir, "new-woland.jsonl")
	if err := os.WriteFile(newWoland, []byte(wolandSessionContent()), 0o644); err != nil {
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

	if got != newWoland {
		t.Errorf("expected %q (newest Woland session), got %q", newWoland, got)
	}
}

func TestFindActiveSession_InconclusiveFilesStillConsidered(t *testing.T) {
	dir := t.TempDir()

	// Create a file with very little content (inconclusive — could be a
	// brand-new Woland session that hasn't had its prompt written yet).
	newFile := filepath.Join(dir, "new-session.jsonl")
	if err := os.WriteFile(newFile, []byte(`{"type":"human"}`+"\n"), 0o644); err != nil {
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

	// Inconclusive files should still be considered as candidates.
	if got != newFile {
		t.Errorf("expected %q (inconclusive file should be allowed), got %q", newFile, got)
	}
}

func TestWatcher_SessionCachePreventsRecheck(t *testing.T) {
	dir := t.TempDir()

	// Create a worker agent file.
	workerPath := filepath.Join(dir, "worker.jsonl")
	if err := os.WriteFile(workerPath, []byte(workerAgentContent()), 0o644); err != nil {
		t.Fatal(err)
	}

	w := &Watcher{
		projectDir:   dir,
		logger:       log.New(os.Stderr, "test: ", 0),
		seen:         make(map[string]bool),
		sessionCache: make(map[string]bool),
	}

	// First call should check and cache the result.
	got1, _ := w.findActiveSession()
	if got1 != "" {
		t.Errorf("first call: expected empty, got %q", got1)
	}

	// Verify the result was cached.
	if _, cached := w.sessionCache[workerPath]; !cached {
		t.Fatal("expected worker file to be cached after first check")
	}
	if w.sessionCache[workerPath] {
		t.Fatal("expected cached value to be false for worker file")
	}

	// Second call should use the cache (file won't be re-read).
	got2, _ := w.findActiveSession()
	if got2 != "" {
		t.Errorf("second call: expected empty, got %q", got2)
	}
}

func TestWatcher_NoSessionSwitchForWorkerAgent(t *testing.T) {
	dir := t.TempDir()

	// Create a Woland session file.
	wolandPath := filepath.Join(dir, "woland.jsonl")
	if err := os.WriteFile(wolandPath, []byte(wolandSessionContent()), 0o644); err != nil {
		t.Fatal(err)
	}

	w := &Watcher{
		projectDir:   dir,
		logger:       log.New(os.Stderr, "test: ", 0),
		seen:         make(map[string]bool),
		sessionCache: make(map[string]bool),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sessionSwitch := make(chan struct{}, 4)
	ch := w.Watch(ctx, sessionSwitch)

	// Wait for the watcher to discover the Woland file.
	time.Sleep(500 * time.Millisecond)

	// Now create a worker agent file that is newer.
	workerPath := filepath.Join(dir, "worker.jsonl")
	if err := os.WriteFile(workerPath, []byte(workerAgentContent()), 0o644); err != nil {
		t.Fatal(err)
	}

	// Keep updating the worker file to simulate an active worker.
	go func() {
		for i := 0; i < 5; i++ {
			time.Sleep(200 * time.Millisecond)
			f, err := os.OpenFile(workerPath, os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				return
			}
			fmt.Fprintf(f, `{"type":"assistant","uuid":"w%d","message":{"content":[{"type":"text","text":"worker msg %d"}]}}`+"\n", i, i)
			f.Close()
		}
	}()

	// Wait enough time for the watcher to poll multiple times.
	time.Sleep(2 * time.Second)
	cancel()

	// Drain the watch channel.
	for range ch {
	}

	// No session switch should have occurred.
	select {
	case <-sessionSwitch:
		t.Error("unexpected session switch — watcher should not switch to worker agent file")
	default:
		// Good — no switch happened.
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
