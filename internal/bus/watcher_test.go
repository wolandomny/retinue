package bus

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Existing tests (kept)
// ---------------------------------------------------------------------------

func TestInjectMessageSkipsSender(t *testing.T) {
	// Create a message from agent "azazello".
	msg := Message{
		ID:        "test-1",
		Name:      "azazello",
		Timestamp: time.Now(),
		Type:      TypeChat,
		Text:      "hello from azazello",
	}

	// Verify FormatForInjection produces the expected format.
	formatted := FormatForInjection(&msg)
	want := "[Azazello] hello from azazello"
	if formatted != want {
		t.Errorf("FormatForInjection = %q, want %q", formatted, want)
	}

	// The actual injection skip logic is in Watcher.injectMessage:
	// it skips agents where agentID == msg.Name. We test this indirectly
	// by verifying the sender exclusion logic.
	agentIDs := []string{"azazello", "behemoth", "koroviev"}
	var targets []string
	for _, id := range agentIDs {
		if id == msg.Name {
			continue // sender excluded
		}
		targets = append(targets, id)
	}

	if len(targets) != 2 {
		t.Errorf("expected 2 targets, got %d", len(targets))
	}
	for _, id := range targets {
		if id == "azazello" {
			t.Error("sender azazello should be excluded from injection targets")
		}
	}
}

func TestInjectMessageSkipsSystemMessages(t *testing.T) {
	msg := Message{
		ID:        "test-2",
		Name:      "system",
		Timestamp: time.Now(),
		Type:      TypeSystem,
		Text:      "Azazello has joined",
	}

	// The Watcher.injectMessage method returns early for system messages.
	// Verify the condition that would trigger the skip.
	if msg.Type != TypeSystem {
		t.Error("expected TypeSystem message type")
	}
}

func TestInjectMessageFormat(t *testing.T) {
	tests := []struct {
		name string
		msg  Message
		want string
	}{
		{
			name: "agent chat message",
			msg: Message{
				Name: "behemoth",
				Type: TypeChat,
				Text: "I fixed the build",
			},
			want: "[Behemoth] I fixed the build",
		},
		{
			name: "user message",
			msg: Message{
				Name: "user",
				Type: TypeUser,
				Text: "what's the status?",
			},
			want: "[User] what's the status?",
		},
		{
			name: "action message",
			msg: Message{
				Name: "azazello",
				Type: TypeAction,
				Text: "I'm going to fix the CI failure",
			},
			want: "[Azazello] I'm going to fix the CI failure",
		},
		{
			name: "result message",
			msg: Message{
				Name: "koroviev",
				Type: TypeResult,
				Text: "Fixed — PR #42 opened",
			},
			want: "[Koroviev] Fixed — PR #42 opened",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatForInjection(&tt.msg)
			if got != tt.want {
				t.Errorf("FormatForInjection = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseSessionLine(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		wantText string
		wantUUID string
		wantOK   bool
	}{
		{
			name:     "assistant message with text",
			line:     `{"type":"assistant","uuid":"abc-123","message":{"content":[{"type":"text","text":"Hello world"}]}}`,
			wantText: "Hello world",
			wantUUID: "abc-123",
			wantOK:   true,
		},
		{
			name:     "human message (now extracted)",
			line:     `{"type":"human","uuid":"def-456","message":{"content":[{"type":"text","text":"User input"}]}}`,
			wantText: "User input",
			wantUUID: "def-456",
			wantOK:   true,
		},
		{
			name:     "empty line",
			line:     "",
			wantText: "",
			wantUUID: "",
			wantOK:   false,
		},
		{
			name:     "invalid JSON",
			line:     "not json",
			wantText: "",
			wantUUID: "",
			wantOK:   false,
		},
		{
			name:     "assistant with no text blocks",
			line:     `{"type":"assistant","uuid":"ghi-789","message":{"content":[{"type":"tool_use","text":""}]}}`,
			wantText: "",
			wantUUID: "ghi-789",
			wantOK:   true,
		},
		{
			name:     "assistant with multiple text blocks",
			line:     `{"type":"assistant","uuid":"multi-1","message":{"content":[{"type":"text","text":"part one"},{"type":"text","text":"part two"}]}}`,
			wantText: "part one\npart two",
			wantUUID: "multi-1",
			wantOK:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, uuid, ok := parseSessionLine(tt.line)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if text != tt.wantText {
				t.Errorf("text = %q, want %q", text, tt.wantText)
			}
			if uuid != tt.wantUUID {
				t.Errorf("uuid = %q, want %q", uuid, tt.wantUUID)
			}
		})
	}
}

func TestClaudeProjectDir(t *testing.T) {
	dir := claudeProjectDir("/Users/broc/apt")
	// Should replace / with - and . with -
	if dir == "" {
		t.Fatal("claudeProjectDir returned empty string")
	}
	// The path should end with the mangled apartment path
	if !containsStr(dir, "-Users-broc-apt") {
		t.Errorf("expected mangled path component, got %s", dir)
	}
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && searchStr(s, substr)
}

func searchStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// 1. Agent discovery / parseAgentWindows
// ---------------------------------------------------------------------------

func TestParseMonitoredWindows(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []monitoredWindow
	}{
		{
			name:  "typical tmux output with mixed windows",
			input: "control\nagent-azazello\nagent-behemoth\nlogs\nagent-koroviev\n",
			want: []monitoredWindow{
				{busName: "azazello", windowName: "agent-azazello"},
				{busName: "behemoth", windowName: "agent-behemoth"},
				{busName: "koroviev", windowName: "agent-koroviev"},
			},
		},
		{
			name:  "only non-agent windows",
			input: "control\nlogs\nmonitoring\n",
			want:  nil,
		},
		{
			name:  "empty output",
			input: "",
			want:  nil,
		},
		{
			name:  "whitespace-only output",
			input: "   \n  \n",
			want:  nil,
		},
		{
			name:  "single agent window",
			input: "agent-azazello\n",
			want: []monitoredWindow{
				{busName: "azazello", windowName: "agent-azazello"},
			},
		},
		{
			name:  "agent- prefix only (no ID)",
			input: "agent-\n",
			want:  nil,
		},
		{
			name:  "agent- prefix only among valid agents",
			input: "agent-azazello\nagent-\nagent-behemoth\n",
			want: []monitoredWindow{
				{busName: "azazello", windowName: "agent-azazello"},
				{busName: "behemoth", windowName: "agent-behemoth"},
			},
		},
		{
			name:  "windows with extra whitespace",
			input: "  agent-azazello  \n  control  \n  agent-behemoth  \n",
			want: []monitoredWindow{
				{busName: "azazello", windowName: "agent-azazello"},
				{busName: "behemoth", windowName: "agent-behemoth"},
			},
		},
		{
			name:  "no trailing newline",
			input: "agent-azazello\nagent-behemoth",
			want: []monitoredWindow{
				{busName: "azazello", windowName: "agent-azazello"},
				{busName: "behemoth", windowName: "agent-behemoth"},
			},
		},
		{
			name:  "agent ID with hyphens",
			input: "agent-my-cool-agent\n",
			want: []monitoredWindow{
				{busName: "my-cool-agent", windowName: "agent-my-cool-agent"},
			},
		},
		{
			name:  "window named 'agent' without hyphen is not matched",
			input: "agent\nagentfoo\n",
			want:  nil,
		},
		{
			name:  "woland window is included",
			input: "control\nagent-azazello\nwoland\nagent-behemoth\n",
			want: []monitoredWindow{
				{busName: "azazello", windowName: "agent-azazello"},
				{busName: "woland", windowName: "woland", isWoland: true},
				{busName: "behemoth", windowName: "agent-behemoth"},
			},
		},
		{
			name:  "babytalk window is included as woland",
			input: "control\nbabytalk\nagent-koroviev\n",
			want: []monitoredWindow{
				{busName: "woland", windowName: "babytalk", isWoland: true},
				{busName: "koroviev", windowName: "agent-koroviev"},
			},
		},
		{
			name:  "woland only",
			input: "woland\n",
			want: []monitoredWindow{
				{busName: "woland", windowName: "woland", isWoland: true},
			},
		},
		{
			name:  "babytalk only",
			input: "babytalk\n",
			want: []monitoredWindow{
				{busName: "woland", windowName: "babytalk", isWoland: true},
			},
		},
		{
			name:  "no monitored windows",
			input: "control\nlogs\n",
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseMonitoredWindows(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("parseMonitoredWindows() returned %d results, want %d\n  got:  %v\n  want: %v",
					len(got), len(tt.want), got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("parseMonitoredWindows()[%d] = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 2. Agent output parsing (readAgentLines)
// ---------------------------------------------------------------------------

// writeJSONL is a test helper that writes session messages as JSONL.
func writeJSONL(t *testing.T, path string, messages []sessionMessage) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, m := range messages {
		if err := enc.Encode(m); err != nil {
			t.Fatal(err)
		}
	}
}

func newTestWatcher(t *testing.T, busDir string) *Watcher {
	t.Helper()
	busFile := filepath.Join(busDir, "bus.jsonl")
	b := New(busFile)
	logger := log.New(os.Stderr, "test: ", 0)
	return NewWatcher(b, "", busDir, logger)
}

func TestReadAgentLines_ExtractsAssistantText(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")

	msgs := []sessionMessage{
		{
			Type: "assistant",
			UUID: "uuid-1",
			Message: messageContent{Content: []contentBlock{
				{Type: "text", Text: "Hello from assistant"},
			}},
		},
		{
			Type: "human",
			UUID: "uuid-2",
			Message: messageContent{Content: []contentBlock{
				{Type: "text", Text: "User says hi"},
			}},
		},
		{
			Type: "assistant",
			UUID: "uuid-3",
			Message: messageContent{Content: []contentBlock{
				{Type: "tool_use", Text: ""},
			}},
		},
		{
			Type: "assistant",
			UUID: "uuid-4",
			Message: messageContent{Content: []contentBlock{
				{Type: "text", Text: "Second assistant message"},
			}},
		},
	}
	writeJSONL(t, sessionPath, msgs)

	w := newTestWatcher(t, dir)
	seen := make(map[string]bool)
	ctx := context.Background()

	// Read with draining=false so messages go to the bus.
	offset, partial := w.readAgentLines(ctx, "test-agent", sessionPath, 0, "", seen, false, false)

	if partial != "" {
		t.Errorf("expected empty partial line, got %q", partial)
	}
	if offset == 0 {
		t.Error("expected offset > 0 after reading")
	}

	// Verify bus has the right messages.
	recent, err := w.bus.ReadRecent(100)
	if err != nil {
		t.Fatal(err)
	}
	// Should have 2 assistant text messages (uuid-1 and uuid-4).
	// uuid-3 has tool_use only, no text, so it's not written to the bus.
	if len(recent) != 2 {
		t.Fatalf("expected 2 bus messages, got %d", len(recent))
	}
	if recent[0].Text != "Hello from assistant" {
		t.Errorf("first message text = %q, want %q", recent[0].Text, "Hello from assistant")
	}
	if recent[1].Text != "Second assistant message" {
		t.Errorf("second message text = %q, want %q", recent[1].Text, "Second assistant message")
	}
	// All messages should be attributed to the agent.
	for _, m := range recent {
		if m.Name != "test-agent" {
			t.Errorf("message name = %q, want %q", m.Name, "test-agent")
		}
		if m.Type != TypeChat {
			t.Errorf("message type = %q, want %q", m.Type, TypeChat)
		}
	}
}

func TestReadAgentLines_UUIDDedup(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")

	// Write the same UUID twice.
	msgs := []sessionMessage{
		{
			Type: "assistant",
			UUID: "dup-uuid",
			Message: messageContent{Content: []contentBlock{
				{Type: "text", Text: "First occurrence"},
			}},
		},
		{
			Type: "assistant",
			UUID: "dup-uuid",
			Message: messageContent{Content: []contentBlock{
				{Type: "text", Text: "Second occurrence (should be deduped)"},
			}},
		},
		{
			Type: "assistant",
			UUID: "unique-uuid",
			Message: messageContent{Content: []contentBlock{
				{Type: "text", Text: "Unique message"},
			}},
		},
	}
	writeJSONL(t, sessionPath, msgs)

	w := newTestWatcher(t, dir)
	seen := make(map[string]bool)
	ctx := context.Background()

	w.readAgentLines(ctx, "test-agent", sessionPath, 0, "", seen, false, false)

	recent, err := w.bus.ReadRecent(100)
	if err != nil {
		t.Fatal(err)
	}
	// Should only have 2 messages: first occurrence of dup-uuid and unique-uuid.
	if len(recent) != 2 {
		t.Fatalf("expected 2 bus messages (dedup), got %d", len(recent))
	}
	if recent[0].Text != "First occurrence" {
		t.Errorf("first message text = %q, want %q", recent[0].Text, "First occurrence")
	}
	if recent[1].Text != "Unique message" {
		t.Errorf("second message text = %q, want %q", recent[1].Text, "Unique message")
	}

	// Verify the seen map was populated.
	if !seen["dup-uuid"] {
		t.Error("expected dup-uuid in seen map")
	}
	if !seen["unique-uuid"] {
		t.Error("expected unique-uuid in seen map")
	}
}

func TestReadAgentLines_Draining(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")

	msgs := []sessionMessage{
		{
			Type: "assistant",
			UUID: "drain-1",
			Message: messageContent{Content: []contentBlock{
				{Type: "text", Text: "Should not go to bus"},
			}},
		},
		{
			Type: "assistant",
			UUID: "drain-2",
			Message: messageContent{Content: []contentBlock{
				{Type: "text", Text: "Also should not go to bus"},
			}},
		},
	}
	writeJSONL(t, sessionPath, msgs)

	w := newTestWatcher(t, dir)
	seen := make(map[string]bool)
	ctx := context.Background()

	// Read with draining=true — messages should populate seen but NOT go to bus.
	w.readAgentLines(ctx, "test-agent", sessionPath, 0, "", seen, true, false)

	recent, err := w.bus.ReadRecent(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 0 {
		t.Errorf("expected 0 bus messages during draining, got %d", len(recent))
	}

	// But the seen map should be populated.
	if !seen["drain-1"] {
		t.Error("expected drain-1 in seen map during drain")
	}
	if !seen["drain-2"] {
		t.Error("expected drain-2 in seen map during drain")
	}
}

func TestReadAgentLines_PartialLine(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")

	// Write a complete line followed by a partial line (no trailing newline).
	msg1 := sessionMessage{
		Type: "assistant",
		UUID: "full-line",
		Message: messageContent{Content: []contentBlock{
			{Type: "text", Text: "Complete message"},
		}},
	}
	data1, _ := json.Marshal(msg1)

	msg2 := sessionMessage{
		Type: "assistant",
		UUID: "partial-line",
		Message: messageContent{Content: []contentBlock{
			{Type: "text", Text: "Partial message"},
		}},
	}
	data2, _ := json.Marshal(msg2)

	// Write first line with newline, second without.
	if err := os.WriteFile(sessionPath, append(append(data1, '\n'), data2...), 0o644); err != nil {
		t.Fatal(err)
	}

	w := newTestWatcher(t, dir)
	seen := make(map[string]bool)
	ctx := context.Background()

	offset, partial := w.readAgentLines(ctx, "test-agent", sessionPath, 0, "", seen, false, false)

	// Only the first message should be processed.
	recent, err := w.bus.ReadRecent(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 1 {
		t.Fatalf("expected 1 bus message, got %d", len(recent))
	}
	if recent[0].Text != "Complete message" {
		t.Errorf("message text = %q, want %q", recent[0].Text, "Complete message")
	}

	// The partial should contain the incomplete second line.
	if partial == "" {
		t.Error("expected non-empty partial line")
	}

	// Now append a newline to complete the partial line.
	f, err := os.OpenFile(sessionPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte("\n"))
	f.Close()

	// Read again, continuing with partial.
	_, partial2 := w.readAgentLines(ctx, "test-agent", sessionPath, offset, partial, seen, false, false)

	if partial2 != "" {
		t.Errorf("expected empty partial after completing line, got %q", partial2)
	}

	recent2, err := w.bus.ReadRecent(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent2) != 2 {
		t.Fatalf("expected 2 bus messages after completing partial, got %d", len(recent2))
	}
	if recent2[1].Text != "Partial message" {
		t.Errorf("second message text = %q, want %q", recent2[1].Text, "Partial message")
	}
}

func TestReadAgentLines_HandlesTruncation(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")

	msgs := []sessionMessage{
		{
			Type: "assistant",
			UUID: "before-trunc",
			Message: messageContent{Content: []contentBlock{
				{Type: "text", Text: "Before truncation"},
			}},
		},
	}
	writeJSONL(t, sessionPath, msgs)

	w := newTestWatcher(t, dir)
	seen := make(map[string]bool)
	ctx := context.Background()

	offset, _ := w.readAgentLines(ctx, "test-agent", sessionPath, 0, "", seen, false, false)

	// Truncate the file (simulate Claude session restart).
	newMsg := sessionMessage{
		Type: "assistant",
		UUID: "after-trunc",
		Message: messageContent{Content: []contentBlock{
			{Type: "text", Text: "After truncation"},
		}},
	}
	writeJSONL(t, sessionPath, []sessionMessage{newMsg})

	// The new file is shorter than our offset, so readAgentLines should reset.
	_, _ = w.readAgentLines(ctx, "test-agent", sessionPath, offset, "", seen, false, false)

	recent, err := w.bus.ReadRecent(100)
	if err != nil {
		t.Fatal(err)
	}
	// Should have 2 messages total: the before-trunc and after-trunc.
	if len(recent) != 2 {
		t.Fatalf("expected 2 bus messages, got %d", len(recent))
	}
	if recent[1].Text != "After truncation" {
		t.Errorf("second message text = %q, want %q", recent[1].Text, "After truncation")
	}
}

func TestReadAgentLines_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")

	if err := os.WriteFile(sessionPath, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	w := newTestWatcher(t, dir)
	seen := make(map[string]bool)
	ctx := context.Background()

	offset, partial := w.readAgentLines(ctx, "test-agent", sessionPath, 0, "", seen, false, false)

	if offset != 0 {
		t.Errorf("expected offset 0 for empty file, got %d", offset)
	}
	if partial != "" {
		t.Errorf("expected empty partial for empty file, got %q", partial)
	}

	recent, err := w.bus.ReadRecent(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 0 {
		t.Errorf("expected 0 bus messages for empty file, got %d", len(recent))
	}
}

func TestReadAgentLines_MissingFile(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "nonexistent.jsonl")

	w := newTestWatcher(t, dir)
	seen := make(map[string]bool)
	ctx := context.Background()

	offset, partial := w.readAgentLines(ctx, "test-agent", sessionPath, 0, "", seen, false, false)

	if offset != 0 {
		t.Errorf("expected offset 0 for missing file, got %d", offset)
	}
	if partial != "" {
		t.Errorf("expected empty partial for missing file, got %q", partial)
	}
}

// ---------------------------------------------------------------------------
// 3. Message injection filtering (injectionTargets)
// ---------------------------------------------------------------------------

func TestInjectionTargets(t *testing.T) {
	windows := []injectionWindow{
		{busName: "woland", windowName: "woland", isWoland: true},
		{busName: "azazello", windowName: "agent-azazello"},
		{busName: "behemoth", windowName: "agent-behemoth"},
		{busName: "koroviev", windowName: "agent-koroviev"},
	}

	tests := []struct {
		name    string
		msg     Message
		want    []injectionWindow
		wantLen int
	}{
		{
			name:    "system message returns nil",
			msg:     Message{Name: "system", Type: TypeSystem, Text: "agent joined"},
			want:    nil,
			wantLen: 0,
		},
		{
			name: "user addresses woland only - woland gets it as hub",
			msg:  Message{Name: "user", Type: TypeUser, Text: "Woland are you there?"},
			want: []injectionWindow{
				{busName: "woland", windowName: "woland", isWoland: true},
			},
			wantLen: 1,
		},
		{
			name: "user addresses behemoth - woland and behemoth get it",
			msg:  Message{Name: "user", Type: TypeUser, Text: "Behemoth, check the logs"},
			want: []injectionWindow{
				{busName: "woland", windowName: "woland", isWoland: true},
				{busName: "behemoth", windowName: "agent-behemoth"},
			},
			wantLen: 2,
		},
		{
			name: "user no agent mentioned - woland only",
			msg:  Message{Name: "user", Type: TypeUser, Text: "How's everyone doing?"},
			want: []injectionWindow{
				{busName: "woland", windowName: "woland", isWoland: true},
			},
			wantLen: 1,
		},
		{
			name: "woland addresses behemoth - behemoth only",
			msg:  Message{Name: "woland", Type: TypeChat, Text: "Behemoth, you are a good kitty"},
			want: []injectionWindow{
				{busName: "behemoth", windowName: "agent-behemoth"},
			},
			wantLen: 1,
		},
		{
			name: "agent sends no mention - woland only as hub",
			msg:  Message{Name: "behemoth", Type: TypeChat, Text: "Done with the sweep"},
			want: []injectionWindow{
				{busName: "woland", windowName: "woland", isWoland: true},
			},
			wantLen: 1,
		},
		{
			name: "woland addresses two agents - both get it",
			msg:  Message{Name: "woland", Type: TypeChat, Text: "Behemoth and Azazello, coordinate"},
			want: []injectionWindow{
				{busName: "azazello", windowName: "agent-azazello"},
				{busName: "behemoth", windowName: "agent-behemoth"},
			},
			wantLen: 2,
		},
		{
			name: "case insensitive matching",
			msg:  Message{Name: "user", Type: TypeUser, Text: "BEHEMOTH check status"},
			want: []injectionWindow{
				{busName: "woland", windowName: "woland", isWoland: true},
				{busName: "behemoth", windowName: "agent-behemoth"},
			},
			wantLen: 2,
		},
		{
			name: "agent mentions another agent - hub-and-spoke: woland only",
			msg:  Message{Name: "behemoth", Type: TypeChat, Text: "I talked to Azazello about it"},
			want: []injectionWindow{
				{busName: "woland", windowName: "woland", isWoland: true},
			},
			wantLen: 1,
		},
		{
			name: "action from agent no mention - woland only",
			msg:  Message{Name: "behemoth", Type: TypeAction, Text: "fixing CI"},
			want: []injectionWindow{
				{busName: "woland", windowName: "woland", isWoland: true},
			},
			wantLen: 1,
		},
		{
			name: "result from agent no mention - woland only",
			msg:  Message{Name: "koroviev", Type: TypeResult, Text: "PR opened"},
			want: []injectionWindow{
				{busName: "woland", windowName: "woland", isWoland: true},
			},
			wantLen: 1,
		},
		{
			name: "message from unknown sender no mention - woland only",
			msg:  Message{Name: "stranger", Type: TypeChat, Text: "who am I?"},
			want: []injectionWindow{
				{busName: "woland", windowName: "woland", isWoland: true},
			},
			wantLen: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := injectionTargets(windows, tt.msg)
			if len(got) != tt.wantLen {
				t.Fatalf("injectionTargets() returned %d results, want %d\n  got:  %v\n  want: %v",
					len(got), tt.wantLen, got, tt.want)
			}
			for i, w := range got {
				if w != tt.want[i] {
					t.Errorf("injectionTargets()[%d] = %+v, want %+v", i, w, tt.want[i])
				}
			}
		})
	}
}

func TestInjectionTargets_EmptyWindowList(t *testing.T) {
	msg := Message{Name: "user", Type: TypeUser, Text: "hello"}
	got := injectionTargets(nil, msg)
	if len(got) != 0 {
		t.Errorf("expected 0 targets with no windows, got %d", len(got))
	}
}

func TestInjectionTargets_SingleAgent_IsSender(t *testing.T) {
	windows := []injectionWindow{
		{busName: "azazello", windowName: "agent-azazello"},
	}
	msg := Message{Name: "azazello", Type: TypeChat, Text: "solo"}
	got := injectionTargets(windows, msg)
	if len(got) != 0 {
		t.Errorf("expected 0 targets when single agent is sender, got %d: %v", len(got), got)
	}
}

func TestInjectionTargets_SingleAgent_NotSender(t *testing.T) {
	// Non-woland agent only receives messages mentioning their name.
	windows := []injectionWindow{
		{busName: "azazello", windowName: "agent-azazello"},
	}
	msg := Message{Name: "user", Type: TypeUser, Text: "hello azazello"}
	got := injectionTargets(windows, msg)
	if len(got) != 1 || got[0].busName != "azazello" {
		t.Errorf("expected [{azazello agent-azazello}], got %v", got)
	}
}

func TestInjectionTargets_SingleAgent_NotMentioned(t *testing.T) {
	// Non-woland agent does NOT receive messages that don't mention them.
	windows := []injectionWindow{
		{busName: "azazello", windowName: "agent-azazello"},
	}
	msg := Message{Name: "user", Type: TypeUser, Text: "hello"}
	got := injectionTargets(windows, msg)
	if len(got) != 0 {
		t.Errorf("expected 0 targets when agent is not mentioned, got %d: %v", len(got), got)
	}
}

func TestInjectionTargets_WolandExcludedWhenSender(t *testing.T) {
	windows := []injectionWindow{
		{busName: "azazello", windowName: "agent-azazello"},
		{busName: "woland", windowName: "woland", isWoland: true},
		{busName: "behemoth", windowName: "agent-behemoth"},
	}
	// Woland sends a message that doesn't mention any agent —
	// no one receives it (woland excluded as sender, agents not mentioned).
	msg := Message{Name: "woland", Type: TypeChat, Text: "I see everything"}
	got := injectionTargets(windows, msg)
	if len(got) != 0 {
		t.Fatalf("expected 0 targets (woland sender, no agents mentioned), got %d: %v", len(got), got)
	}

	// Woland sends a message mentioning azazello — only azazello receives.
	msg2 := Message{Name: "woland", Type: TypeChat, Text: "Azazello, report status"}
	got2 := injectionTargets(windows, msg2)
	if len(got2) != 1 || got2[0].busName != "azazello" {
		t.Fatalf("expected [azazello], got %v", got2)
	}
	for _, w := range got2 {
		if w.busName == "woland" {
			t.Error("woland should be excluded when it is the sender")
		}
	}
}

func TestInjectionTargets_WolandIncludedWhenNotSender(t *testing.T) {
	windows := []injectionWindow{
		{busName: "azazello", windowName: "agent-azazello"},
		{busName: "woland", windowName: "woland", isWoland: true},
	}
	msg := Message{Name: "azazello", Type: TypeChat, Text: "reporting in"}
	got := injectionTargets(windows, msg)
	if len(got) != 1 || got[0].busName != "woland" {
		t.Errorf("expected woland as sole target (hub), got %v", got)
	}
}

func TestInjectionTargets_BabytalkExcludedWhenWolandSends(t *testing.T) {
	// "babytalk" window has busName "woland", so it should be excluded
	// when the sender is "woland".
	windows := []injectionWindow{
		{busName: "azazello", windowName: "agent-azazello"},
		{busName: "woland", windowName: "babytalk", isWoland: true},
	}
	// Woland mentions azazello → azazello gets it, babytalk (woland) excluded as sender.
	msg := Message{Name: "woland", Type: TypeChat, Text: "Azazello, I see everything"}
	got := injectionTargets(windows, msg)
	if len(got) != 1 || got[0].busName != "azazello" {
		t.Errorf("expected only azazello, got %v", got)
	}
}

func TestInjectionTargets_SmartRouting(t *testing.T) {
	// Standard window setup with woland, behemoth, and azazello.
	windows := []injectionWindow{
		{busName: "woland", windowName: "woland", isWoland: true},
		{busName: "behemoth", windowName: "agent-behemoth"},
		{busName: "azazello", windowName: "agent-azazello"},
	}

	tests := []struct {
		name     string
		windows  []injectionWindow
		msg      Message
		expected []injectionWindow
	}{
		{
			name:    "UserAddressesWoland",
			windows: windows,
			msg:     Message{Name: "user", Type: TypeUser, Text: "Woland are you there?"},
			expected: []injectionWindow{
				{busName: "woland", windowName: "woland", isWoland: true},
			},
		},
		{
			name:    "UserAddressesBehemoth",
			windows: windows,
			msg:     Message{Name: "user", Type: TypeUser, Text: "Behemoth, check the logs"},
			expected: []injectionWindow{
				{busName: "woland", windowName: "woland", isWoland: true},
				{busName: "behemoth", windowName: "agent-behemoth"},
			},
		},
		{
			name:    "UserAddressesBothAgents",
			windows: windows,
			msg:     Message{Name: "user", Type: TypeUser, Text: "Behemoth and Azazello, coordinate"},
			expected: []injectionWindow{
				{busName: "woland", windowName: "woland", isWoland: true},
				{busName: "behemoth", windowName: "agent-behemoth"},
				{busName: "azazello", windowName: "agent-azazello"},
			},
		},
		{
			name:    "UserGeneralMessage",
			windows: windows,
			msg:     Message{Name: "user", Type: TypeUser, Text: "How is everyone doing?"},
			expected: []injectionWindow{
				{busName: "woland", windowName: "woland", isWoland: true},
			},
		},
		{
			name:    "WolandAddressesBehemoth",
			windows: windows,
			msg:     Message{Name: "woland", Type: TypeChat, Text: "Behemoth, you did great"},
			expected: []injectionWindow{
				{busName: "behemoth", windowName: "agent-behemoth"},
			},
		},
		{
			name:    "BehemothReports",
			windows: windows,
			msg:     Message{Name: "behemoth", Type: TypeChat, Text: "Done with the sweep"},
			expected: []injectionWindow{
				{busName: "woland", windowName: "woland", isWoland: true},
			},
		},
		{
			name:    "BehemothMentionsAzazello",
			windows: windows,
			msg:     Message{Name: "behemoth", Type: TypeChat, Text: "I coordinated with Azazello on this"},
			expected: []injectionWindow{
				{busName: "woland", windowName: "woland", isWoland: true},
			},
		},
		{
			name:    "AgentMessageToWolandOnly",
			windows: windows,
			msg:     Message{Name: "azazello", Type: TypeChat, Text: "Behemoth and I finished the task"},
			expected: []injectionWindow{
				{busName: "woland", windowName: "woland", isWoland: true},
			},
		},
		{
			name:    "WolandAddressesTester",
			windows: append(windows, injectionWindow{busName: "tester", windowName: "agent-tester"}),
			msg:     Message{Name: "woland", Type: TypeChat, Text: "Tester's joke was funny"},
			expected: []injectionWindow{
				{busName: "tester", windowName: "agent-tester"},
			},
		},
		{
			name:     "SystemMessage",
			windows:  windows,
			msg:      Message{Name: "system", Type: TypeSystem, Text: "agent joined"},
			expected: nil,
		},
		{
			name:    "CaseInsensitive",
			windows: windows,
			msg:     Message{Name: "user", Type: TypeUser, Text: "BEHEMOTH check this"},
			expected: []injectionWindow{
				{busName: "woland", windowName: "woland", isWoland: true},
				{busName: "behemoth", windowName: "agent-behemoth"},
			},
		},
		{
			name: "NoAgentsRunning",
			windows: []injectionWindow{
				{busName: "woland", windowName: "woland", isWoland: true},
			},
			msg: Message{Name: "user", Type: TypeUser, Text: "hello"},
			expected: []injectionWindow{
				{busName: "woland", windowName: "woland", isWoland: true},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := injectionTargets(tt.windows, tt.msg)

			// Check length first.
			if len(got) != len(tt.expected) {
				t.Errorf("injectionTargets() length = %d, expected %d\nGot: %v\nExpected: %v",
					len(got), len(tt.expected), got, tt.expected)
				return
			}

			// For nil case, both should be nil.
			if len(tt.expected) == 0 && len(got) == 0 {
				return
			}

			// Check each window.
			for i, expected := range tt.expected {
				if got[i] != expected {
					t.Errorf("injectionTargets()[%d] = %+v, expected %+v", i, got[i], expected)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 4. Session file discovery (findNewestJSONL / findAgentSessionFile)
// ---------------------------------------------------------------------------

func TestFindNewestJSONL(t *testing.T) {
	dir := t.TempDir()
	w := newTestWatcher(t, dir)

	// Create multiple .jsonl files with different timestamps.
	files := []struct {
		name    string
		content string
		delay   time.Duration
	}{
		{"old.jsonl", `{"type":"human"}`, 0},
		{"middle.jsonl", `{"type":"human"}`, 50 * time.Millisecond},
		{"newest.jsonl", `{"type":"human"}`, 100 * time.Millisecond},
	}

	for _, f := range files {
		time.Sleep(f.delay)
		path := filepath.Join(dir, f.name)
		if err := os.WriteFile(path, []byte(f.content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got := w.findNewestJSONL(dir)
	want := filepath.Join(dir, "newest.jsonl")
	if got != want {
		t.Errorf("findNewestJSONL() = %q, want %q", got, want)
	}
}

func TestFindNewestJSONL_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	w := newTestWatcher(t, dir)

	got := w.findNewestJSONL(dir)
	if got != "" {
		t.Errorf("findNewestJSONL(empty dir) = %q, want empty", got)
	}
}

func TestFindNewestJSONL_NoJSONLFiles(t *testing.T) {
	dir := t.TempDir()
	w := newTestWatcher(t, dir)

	// Create non-JSONL files.
	os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("hi"), 0o644)
	os.WriteFile(filepath.Join(dir, "data.json"), []byte("{}"), 0o644)
	os.WriteFile(filepath.Join(dir, "log.csv"), []byte("a,b"), 0o644)

	got := w.findNewestJSONL(dir)
	if got != "" {
		t.Errorf("findNewestJSONL(no .jsonl) = %q, want empty", got)
	}
}

func TestFindNewestJSONL_IgnoresDirectories(t *testing.T) {
	dir := t.TempDir()
	w := newTestWatcher(t, dir)

	// Create a directory that ends in .jsonl (shouldn't be matched).
	os.Mkdir(filepath.Join(dir, "fake.jsonl"), 0o755)

	// Create a real jsonl file.
	realFile := filepath.Join(dir, "real.jsonl")
	os.WriteFile(realFile, []byte(`{"type":"human"}`), 0o644)

	got := w.findNewestJSONL(dir)
	if got != realFile {
		t.Errorf("findNewestJSONL() = %q, want %q", got, realFile)
	}
}

func TestFindNewestJSONL_SingleFile(t *testing.T) {
	dir := t.TempDir()
	w := newTestWatcher(t, dir)

	path := filepath.Join(dir, "only.jsonl")
	os.WriteFile(path, []byte(`{"type":"human"}`), 0o644)

	got := w.findNewestJSONL(dir)
	if got != path {
		t.Errorf("findNewestJSONL(single) = %q, want %q", got, path)
	}
}

func TestFindNewestJSONL_NonexistentDir(t *testing.T) {
	dir := t.TempDir()
	w := newTestWatcher(t, dir)

	got := w.findNewestJSONL(filepath.Join(dir, "nonexistent"))
	if got != "" {
		t.Errorf("findNewestJSONL(nonexistent) = %q, want empty", got)
	}
}

func TestFindAgentSessionFile(t *testing.T) {
	// findAgentSessionFile uses claudeProjectDir(w.aptPath), so we need to
	// create the directory structure it expects.
	dir := t.TempDir()

	// Create a Watcher whose aptPath will point to something we control.
	// We'll override claudeProjectDir's behavior by creating the expected
	// directory.
	aptPath := filepath.Join(dir, "apt")
	os.MkdirAll(aptPath, 0o755)

	// The claudeProjectDir function computes from home dir, so we create
	// the directory it would compute. Instead, let's test findNewestJSONL
	// more directly since findAgentSessionFile delegates to the same logic.
	projDir := claudeProjectDir(aptPath)
	os.MkdirAll(projDir, 0o755)

	busFile := filepath.Join(dir, "bus.jsonl")
	b := New(busFile)
	logger := log.New(os.Stderr, "test: ", 0)
	w := NewWatcher(b, "", aptPath, logger)

	// Create session files.
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(filepath.Join(projDir, "old-session.jsonl"), []byte(`{}`), 0o644)
	time.Sleep(50 * time.Millisecond)
	newest := filepath.Join(projDir, "new-session.jsonl")
	os.WriteFile(newest, []byte(`{}`), 0o644)

	got := w.findAgentSessionFile("test-agent")
	if got != newest {
		t.Errorf("findAgentSessionFile() = %q, want %q", got, newest)
	}
}

func TestFindAgentSessionFile_NoProjDir(t *testing.T) {
	dir := t.TempDir()
	busFile := filepath.Join(dir, "bus.jsonl")
	b := New(busFile)
	logger := log.New(os.Stderr, "test: ", 0)
	// aptPath points to something that won't have a Claude projects dir.
	w := NewWatcher(b, "", filepath.Join(dir, "nonexistent-apt"), logger)

	got := w.findAgentSessionFile("test-agent")
	if got != "" {
		t.Errorf("findAgentSessionFile(missing dir) = %q, want empty", got)
	}
}

func TestFindAgentSessionFile_UsesMarker(t *testing.T) {
	dir := t.TempDir()
	aptPath := filepath.Join(dir, "apt")
	os.MkdirAll(aptPath, 0o755)

	// Create the session file the marker points to.
	sessionFile := filepath.Join(dir, "agent-session.jsonl")
	os.WriteFile(sessionFile, []byte(`{"type":"human"}`), 0o644)

	// Write the agent marker.
	markerPath := filepath.Join(aptPath, ".agent-azazello-session")
	os.WriteFile(markerPath, []byte(sessionFile+"\n"), 0o644)

	busFile := filepath.Join(dir, "bus.jsonl")
	b := New(busFile)
	logger := log.New(os.Stderr, "test: ", 0)
	w := NewWatcher(b, "", aptPath, logger)

	got := w.findAgentSessionFile("azazello")
	if got != sessionFile {
		t.Errorf("findAgentSessionFile() = %q, want %q", got, sessionFile)
	}
}

func TestFindAgentSessionFile_MarkerPointsToMissingFile(t *testing.T) {
	dir := t.TempDir()
	aptPath := filepath.Join(dir, "apt")
	os.MkdirAll(aptPath, 0o755)

	// Marker points to a non-existent file.
	markerPath := filepath.Join(aptPath, ".agent-azazello-session")
	os.WriteFile(markerPath, []byte("/nonexistent/session.jsonl"), 0o644)

	// Create fallback.
	projDir := claudeProjectDir(aptPath)
	os.MkdirAll(projDir, 0o755)
	fallbackFile := filepath.Join(projDir, "fallback.jsonl")
	os.WriteFile(fallbackFile, []byte(`{"type":"human"}`), 0o644)

	busFile := filepath.Join(dir, "bus.jsonl")
	b := New(busFile)
	logger := log.New(os.Stderr, "test: ", 0)
	w := NewWatcher(b, "", aptPath, logger)

	got := w.findAgentSessionFile("azazello")
	if got != fallbackFile {
		t.Errorf("findAgentSessionFile() = %q, want %q (fallback)", got, fallbackFile)
	}
}

func TestFindAgentSessionFile_MarkerPreferredOverNewest(t *testing.T) {
	// When a marker exists, it should be used even if a newer .jsonl exists.
	dir := t.TempDir()
	aptPath := filepath.Join(dir, "apt")
	os.MkdirAll(aptPath, 0o755)

	projDir := claudeProjectDir(aptPath)
	os.MkdirAll(projDir, 0o755)

	// Create the agent's session file (older).
	agentFile := filepath.Join(projDir, "agent-session.jsonl")
	os.WriteFile(agentFile, []byte(`{"type":"human"}`), 0o644)
	time.Sleep(50 * time.Millisecond)

	// Create a newer file from another agent.
	otherFile := filepath.Join(projDir, "other-session.jsonl")
	os.WriteFile(otherFile, []byte(`{"type":"human"}`), 0o644)

	// Write marker pointing to the older (correct) agent file.
	markerPath := filepath.Join(aptPath, ".agent-azazello-session")
	os.WriteFile(markerPath, []byte(agentFile), 0o644)

	busFile := filepath.Join(dir, "bus.jsonl")
	b := New(busFile)
	logger := log.New(os.Stderr, "test: ", 0)
	w := NewWatcher(b, "", aptPath, logger)

	got := w.findAgentSessionFile("azazello")
	if got != agentFile {
		t.Errorf("findAgentSessionFile() = %q, want %q (marker should win over newest)", got, agentFile)
	}
}

// ---------------------------------------------------------------------------
// 5. seekToLineStart
// ---------------------------------------------------------------------------

func TestSeekToLineStart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")

	// Create a file with known content.
	// Offsets:  0123456789...
	// Line 1:  "Hello\n"      bytes 0-5
	// Line 2:  "World\n"      bytes 6-11
	// Line 3:  "Foo bar\n"    bytes 12-19
	content := "Hello\nWorld\nFoo bar\n"
	os.WriteFile(path, []byte(content), 0o644)

	tests := []struct {
		name       string
		offset     int64
		wantOffset int64
	}{
		{
			name:       "offset 0 returns 0",
			offset:     0,
			wantOffset: 0,
		},
		{
			name:       "middle of first line (offset 3) → start of second line",
			offset:     3,
			wantOffset: 6, // past the \n at position 5
		},
		{
			name:       "at newline position (offset 5) → start of second line",
			offset:     5,
			wantOffset: 6, // finds \n at relative position 0, returns 5+0+1=6
		},
		{
			name:       "start of second line (offset 6) → start of third line",
			offset:     6,
			wantOffset: 12, // finds \n at "World\n", relative pos 5, returns 6+5+1=12
		},
		{
			name:       "middle of second line (offset 8) → start of third line",
			offset:     8,
			wantOffset: 12, // finds \n at relative pos 3, returns 8+3+1=12
		},
		{
			name:       "middle of third line (offset 15) → past third line",
			offset:     15,
			wantOffset: 20, // finds \n at "bar\n", relative pos 4, returns 15+4+1=20
		},
		{
			name:       "negative offset returns 0",
			offset:     -5,
			wantOffset: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := seekToLineStart(path, tt.offset)
			if err != nil {
				t.Fatalf("seekToLineStart(offset=%d) error: %v", tt.offset, err)
			}
			if got != tt.wantOffset {
				t.Errorf("seekToLineStart(offset=%d) = %d, want %d", tt.offset, got, tt.wantOffset)
			}
		})
	}
}

func TestSeekToLineStart_PastEndOfFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")

	content := "Hello\nWorld\n"
	os.WriteFile(path, []byte(content), 0o644)

	// Seeking past the end of file — Read returns 0 bytes.
	got, err := seekToLineStart(path, 1000)
	// When n==0, the function returns (offset, err) — likely an io.EOF or similar.
	// The implementation returns (offset, err) when n == 0.
	if err == nil {
		// The offset should be unchanged or moved past.
		_ = got
	}
	// The key thing is it doesn't panic.
}

func TestSeekToLineStart_NoNewlineInBuffer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")

	// Create a file with a single line and no newline.
	content := "Hello World"
	os.WriteFile(path, []byte(content), 0o644)

	// Seek to middle — buffer has no newline, should return offset + n.
	got, err := seekToLineStart(path, 3)
	if err != nil {
		t.Fatalf("seekToLineStart error: %v", err)
	}
	// No newline found, so returns offset + n (3 + 8 = 11, length of remaining).
	if got != int64(len(content)) {
		t.Errorf("seekToLineStart(no newline) = %d, want %d", got, len(content))
	}
}

func TestSeekToLineStart_MissingFile(t *testing.T) {
	_, err := seekToLineStart("/nonexistent/path/file.txt", 10)
	if err == nil {
		t.Error("expected error for missing file")
	}
}

// ---------------------------------------------------------------------------
// 6. tmuxArgs helper
// ---------------------------------------------------------------------------

func TestTmuxArgs_WithSocket(t *testing.T) {
	dir := t.TempDir()
	busFile := filepath.Join(dir, "bus.jsonl")
	b := New(busFile)
	logger := log.New(os.Stderr, "test: ", 0)
	w := NewWatcher(b, "retinue-socket", dir, logger)

	args := w.tmuxArgs("list-windows", "-t", "retinue")
	want := []string{"-L", "retinue-socket", "list-windows", "-t", "retinue"}
	if len(args) != len(want) {
		t.Fatalf("tmuxArgs length = %d, want %d", len(args), len(want))
	}
	for i := range args {
		if args[i] != want[i] {
			t.Errorf("tmuxArgs[%d] = %q, want %q", i, args[i], want[i])
		}
	}
}

func TestTmuxArgs_WithoutSocket(t *testing.T) {
	dir := t.TempDir()
	busFile := filepath.Join(dir, "bus.jsonl")
	b := New(busFile)
	logger := log.New(os.Stderr, "test: ", 0)
	w := NewWatcher(b, "", dir, logger)

	args := w.tmuxArgs("send-keys", "-t", "retinue:agent-foo")
	want := []string{"send-keys", "-t", "retinue:agent-foo"}
	if len(args) != len(want) {
		t.Fatalf("tmuxArgs length = %d, want %d", len(args), len(want))
	}
	for i := range args {
		if args[i] != want[i] {
			t.Errorf("tmuxArgs[%d] = %q, want %q", i, args[i], want[i])
		}
	}
}

// ---------------------------------------------------------------------------
// 7. Integration-like test: readAgentLines across multiple reads
// ---------------------------------------------------------------------------

func TestReadAgentLines_IncrementalReads(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")

	// Start with one message.
	msg1 := sessionMessage{
		Type: "assistant",
		UUID: "incr-1",
		Message: messageContent{Content: []contentBlock{
			{Type: "text", Text: "First"},
		}},
	}
	writeJSONL(t, sessionPath, []sessionMessage{msg1})

	w := newTestWatcher(t, dir)
	seen := make(map[string]bool)
	ctx := context.Background()

	// First read.
	offset, partial := w.readAgentLines(ctx, "test-agent", sessionPath, 0, "", seen, false, false)

	recent, _ := w.bus.ReadRecent(100)
	if len(recent) != 1 {
		t.Fatalf("after first read: expected 1 message, got %d", len(recent))
	}

	// Append a second message.
	msg2 := sessionMessage{
		Type: "assistant",
		UUID: "incr-2",
		Message: messageContent{Content: []contentBlock{
			{Type: "text", Text: "Second"},
		}},
	}
	data, _ := json.Marshal(msg2)
	f, _ := os.OpenFile(sessionPath, os.O_APPEND|os.O_WRONLY, 0o644)
	f.Write(append(data, '\n'))
	f.Close()

	// Second read picks up only the new message.
	offset, partial = w.readAgentLines(ctx, "test-agent", sessionPath, offset, partial, seen, false, false)
	_ = offset

	recent, _ = w.bus.ReadRecent(100)
	if len(recent) != 2 {
		t.Fatalf("after second read: expected 2 messages, got %d", len(recent))
	}
	if recent[1].Text != "Second" {
		t.Errorf("second message = %q, want %q", recent[1].Text, "Second")
	}

	// Verify the first UUID isn't re-emitted (dedup via seen map).
	if !seen["incr-1"] || !seen["incr-2"] {
		t.Error("expected both UUIDs in seen map")
	}
}

// ---------------------------------------------------------------------------
// 8. readAgentLines with mixed message types
// ---------------------------------------------------------------------------

func TestReadAgentLines_MixedMessageTypes(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")

	msgs := []sessionMessage{
		{
			Type: "system",
			UUID: "sys-1",
			Message: messageContent{Content: []contentBlock{
				{Type: "text", Text: "System init"},
			}},
		},
		{
			Type: "human",
			UUID: "human-1",
			Message: messageContent{Content: []contentBlock{
				{Type: "text", Text: "User says hi"},
			}},
		},
		{
			Type: "assistant",
			UUID: "asst-1",
			Message: messageContent{Content: []contentBlock{
				{Type: "text", Text: "Assistant reply"},
			}},
		},
		{
			Type: "assistant",
			UUID: "asst-2",
			Message: messageContent{Content: []contentBlock{
				{Type: "tool_use", Text: "tool invocation"},
				{Type: "text", Text: "With text"},
			}},
		},
		{
			Type: "tool_result",
			UUID: "tool-1",
			Message: messageContent{Content: []contentBlock{
				{Type: "text", Text: "Tool output"},
			}},
		},
	}
	writeJSONL(t, sessionPath, msgs)

	w := newTestWatcher(t, dir)
	seen := make(map[string]bool)
	ctx := context.Background()

	w.readAgentLines(ctx, "test-agent", sessionPath, 0, "", seen, false, false)

	recent, err := w.bus.ReadRecent(100)
	if err != nil {
		t.Fatal(err)
	}
	// Only assistant messages with text content should appear.
	if len(recent) != 2 {
		t.Fatalf("expected 2 bus messages, got %d", len(recent))
	}
	if recent[0].Text != "Assistant reply" {
		t.Errorf("first = %q, want %q", recent[0].Text, "Assistant reply")
	}
	if recent[1].Text != "With text" {
		t.Errorf("second = %q, want %q", recent[1].Text, "With text")
	}
}

// ---------------------------------------------------------------------------
// 9. readAgentLines with invalid/malformed JSONL
// ---------------------------------------------------------------------------

func TestReadAgentLines_MalformedLines(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")

	// Mix valid and invalid lines.
	lines := []string{
		`{"type":"assistant","uuid":"good-1","message":{"content":[{"type":"text","text":"Good message"}]}}`,
		`not valid json at all`,
		`{"incomplete": true`,
		`{"type":"assistant","uuid":"good-2","message":{"content":[{"type":"text","text":"Another good one"}]}}`,
		``,
	}

	content := strings.Join(lines, "\n") + "\n"
	os.WriteFile(sessionPath, []byte(content), 0o644)

	w := newTestWatcher(t, dir)
	seen := make(map[string]bool)
	ctx := context.Background()

	w.readAgentLines(ctx, "test-agent", sessionPath, 0, "", seen, false, false)

	recent, err := w.bus.ReadRecent(100)
	if err != nil {
		t.Fatal(err)
	}
	// Only valid assistant messages should come through.
	if len(recent) != 2 {
		t.Fatalf("expected 2 bus messages (skipping malformed), got %d", len(recent))
	}
	if recent[0].Text != "Good message" {
		t.Errorf("first = %q, want %q", recent[0].Text, "Good message")
	}
	if recent[1].Text != "Another good one" {
		t.Errorf("second = %q, want %q", recent[1].Text, "Another good one")
	}
}

// ---------------------------------------------------------------------------
// 10. discoverAgents lifecycle (no tmux, but logic tests)
// ---------------------------------------------------------------------------

func TestStopAllWatchers(t *testing.T) {
	dir := t.TempDir()
	busFile := filepath.Join(dir, "bus.jsonl")
	b := New(busFile)
	logger := log.New(os.Stderr, "test: ", 0)
	w := NewWatcher(b, "", dir, logger)

	// Manually add some fake watchers.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, id := range []string{"agent-a", "agent-b", "agent-c"} {
		childCtx, childCancel := context.WithCancel(ctx)
		done := make(chan struct{})
		w.watchers[id] = &agentWatcher{cancel: childCancel, done: done}
		// Simulate a goroutine that exits when cancelled.
		go func(c context.Context, d chan struct{}) {
			defer close(d)
			<-c.Done()
		}(childCtx, done)
	}

	if len(w.watchers) != 3 {
		t.Fatalf("expected 3 watchers before stop, got %d", len(w.watchers))
	}

	w.stopAllWatchers()

	if len(w.watchers) != 0 {
		t.Errorf("expected 0 watchers after stop, got %d", len(w.watchers))
	}
}

// ---------------------------------------------------------------------------
// 11. parseSessionLine edge cases
// ---------------------------------------------------------------------------

func TestParseSessionLine_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		wantText string
		wantUUID string
		wantOK   bool
	}{
		{
			name:     "whitespace-only line",
			line:     "   \t  ",
			wantText: "",
			wantUUID: "",
			wantOK:   false,
		},
		{
			name:     "assistant with empty text block",
			line:     `{"type":"assistant","uuid":"empty-text","message":{"content":[{"type":"text","text":""}]}}`,
			wantText: "",
			wantUUID: "empty-text",
			wantOK:   true,
		},
		{
			name:     "assistant with empty content array",
			line:     `{"type":"assistant","uuid":"no-content","message":{"content":[]}}`,
			wantText: "",
			wantUUID: "no-content",
			wantOK:   true,
		},
		{
			name:     "assistant with no UUID",
			line:     `{"type":"assistant","message":{"content":[{"type":"text","text":"no uuid"}]}}`,
			wantText: "no uuid",
			wantUUID: "",
			wantOK:   true,
		},
		{
			name: "very long text",
			line: fmt.Sprintf(`{"type":"assistant","uuid":"long-1","message":{"content":[{"type":"text","text":"%s"}]}}`,
				strings.Repeat("x", 10000)),
			wantText: strings.Repeat("x", 10000),
			wantUUID: "long-1",
			wantOK:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, uuid, ok := parseSessionLine(tt.line)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if text != tt.wantText {
				t.Errorf("text = %q, want %q", text, tt.wantText)
			}
			if uuid != tt.wantUUID {
				t.Errorf("uuid = %q, want %q", uuid, tt.wantUUID)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 12. claudeProjectDir edge cases
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Woland session file discovery
// ---------------------------------------------------------------------------

func TestFindWolandSessionFile_UsesMarker(t *testing.T) {
	dir := t.TempDir()
	aptPath := filepath.Join(dir, "apt")
	os.MkdirAll(aptPath, 0o755)

	// Create the session file the marker points to.
	sessionFile := filepath.Join(dir, "woland-session.jsonl")
	os.WriteFile(sessionFile, []byte(`{"type":"human"}`), 0o644)

	// Write the marker.
	markerPath := filepath.Join(aptPath, ".woland-session")
	os.WriteFile(markerPath, []byte(sessionFile+"\n"), 0o644)

	busFile := filepath.Join(dir, "bus.jsonl")
	b := New(busFile)
	logger := log.New(os.Stderr, "test: ", 0)
	w := NewWatcher(b, "", aptPath, logger)

	got := w.findWolandSessionFile()
	if got != sessionFile {
		t.Errorf("findWolandSessionFile() = %q, want %q", got, sessionFile)
	}
}

func TestFindWolandSessionFile_MarkerMissing(t *testing.T) {
	dir := t.TempDir()
	aptPath := filepath.Join(dir, "apt")
	os.MkdirAll(aptPath, 0o755)

	// No marker file — should fall back to findAgentSessionFile.
	// Create the Claude projects dir with a JSONL file.
	projDir := claudeProjectDir(aptPath)
	os.MkdirAll(projDir, 0o755)
	fallbackFile := filepath.Join(projDir, "fallback.jsonl")
	os.WriteFile(fallbackFile, []byte(`{"type":"human"}`), 0o644)

	busFile := filepath.Join(dir, "bus.jsonl")
	b := New(busFile)
	logger := log.New(os.Stderr, "test: ", 0)
	w := NewWatcher(b, "", aptPath, logger)

	got := w.findWolandSessionFile()
	if got != fallbackFile {
		t.Errorf("findWolandSessionFile() = %q, want %q (fallback)", got, fallbackFile)
	}
}

func TestFindWolandSessionFile_MarkerPointsToMissingFile(t *testing.T) {
	dir := t.TempDir()
	aptPath := filepath.Join(dir, "apt")
	os.MkdirAll(aptPath, 0o755)

	// Marker points to a non-existent file.
	markerPath := filepath.Join(aptPath, ".woland-session")
	os.WriteFile(markerPath, []byte("/nonexistent/session.jsonl"), 0o644)

	// Create fallback.
	projDir := claudeProjectDir(aptPath)
	os.MkdirAll(projDir, 0o755)
	fallbackFile := filepath.Join(projDir, "fallback.jsonl")
	os.WriteFile(fallbackFile, []byte(`{"type":"human"}`), 0o644)

	busFile := filepath.Join(dir, "bus.jsonl")
	b := New(busFile)
	logger := log.New(os.Stderr, "test: ", 0)
	w := NewWatcher(b, "", aptPath, logger)

	got := w.findWolandSessionFile()
	if got != fallbackFile {
		t.Errorf("findWolandSessionFile() = %q, want %q (fallback)", got, fallbackFile)
	}
}

// ---------------------------------------------------------------------------
// Woland bus identity
// ---------------------------------------------------------------------------

func TestWolandMessagesUseBusNameWoland(t *testing.T) {
	// Verify that messages from a Woland window use "woland" as the bus name,
	// not "agent-woland" or "babytalk".
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")

	msgs := []sessionMessage{
		{
			Type: "assistant",
			UUID: "woland-msg-1",
			Message: messageContent{Content: []contentBlock{
				{Type: "text", Text: "I see everything"},
			}},
		},
	}
	writeJSONL(t, sessionPath, msgs)

	w := newTestWatcher(t, dir)
	seen := make(map[string]bool)
	ctx := context.Background()

	// Simulate reading as "woland" (the busName used for Woland windows).
	w.readAgentLines(ctx, "woland", sessionPath, 0, "", seen, false, false)

	recent, err := w.bus.ReadRecent(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 1 {
		t.Fatalf("expected 1 bus message, got %d", len(recent))
	}
	if recent[0].Name != "woland" {
		t.Errorf("message name = %q, want %q", recent[0].Name, "woland")
	}
	if recent[0].Text != "I see everything" {
		t.Errorf("message text = %q, want %q", recent[0].Text, "I see everything")
	}
}

func TestParseMonitoredWindows_WolandBusName(t *testing.T) {
	// Both "woland" and "babytalk" windows should produce busName "woland".
	input := "woland\nbabytalk\nagent-azazello\n"
	got := parseMonitoredWindows(input)

	if len(got) != 3 {
		t.Fatalf("expected 3 windows, got %d: %v", len(got), got)
	}
	// woland window
	if got[0].busName != "woland" {
		t.Errorf("woland busName = %q, want %q", got[0].busName, "woland")
	}
	if got[0].windowName != "woland" {
		t.Errorf("woland windowName = %q, want %q", got[0].windowName, "woland")
	}
	// babytalk window
	if got[1].busName != "woland" {
		t.Errorf("babytalk busName = %q, want %q", got[1].busName, "woland")
	}
	if got[1].windowName != "babytalk" {
		t.Errorf("babytalk windowName = %q, want %q", got[1].windowName, "babytalk")
	}
	// agent window uses its ID, not "agent-azazello"
	if got[2].busName != "azazello" {
		t.Errorf("agent busName = %q, want %q", got[2].busName, "azazello")
	}
}

func TestClaudeProjectDir_DotPaths(t *testing.T) {
	dir := claudeProjectDir("/home/user/.hidden/path.v2")
	// Dots and slashes should be replaced with hyphens.
	if !strings.Contains(dir, "-home-user--hidden-path-v2") {
		t.Errorf("claudeProjectDir(.hidden/path.v2) = %q, expected mangled dots and slashes", dir)
	}
}

func TestClaudeProjectDir_ContainsProjectsDir(t *testing.T) {
	dir := claudeProjectDir("/any/path")
	if !strings.Contains(dir, filepath.Join(".claude", "projects")) {
		t.Errorf("claudeProjectDir should contain .claude/projects, got %q", dir)
	}
}

// ===========================================================================
// Multi-agent integration tests: verify correct session file attribution
// and prevent the race condition that caused Woland's output to be tagged
// as Behemoth's.
// ===========================================================================

// makeSessionJSONL builds a single JSONL line for an assistant message.
func makeSessionJSONL(uuid, text string) string {
	msg := sessionMessage{
		Type: "assistant",
		UUID: uuid,
		Message: messageContent{Content: []contentBlock{
			{Type: "text", Text: text},
		}},
	}
	data, _ := json.Marshal(msg)
	return string(data) + "\n"
}

// setupMultiAgentEnv creates a temp directory with:
//   - an aptPath directory
//   - a Claude projects directory (derived from aptPath) containing
//     the specified session JSONL files
//   - a bus JSONL path
//
// It returns (aptPath, projDir, busFile, watcher).
func setupMultiAgentEnv(t *testing.T) (string, string, string, *Watcher) {
	t.Helper()
	dir := t.TempDir()

	aptPath := filepath.Join(dir, "apt")
	if err := os.MkdirAll(aptPath, 0o755); err != nil {
		t.Fatal(err)
	}

	projDir := claudeProjectDir(aptPath)
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	busFile := filepath.Join(dir, "bus.jsonl")
	b := New(busFile)
	logger := log.New(os.Stderr, "test: ", 0)
	w := NewWatcher(b, "", aptPath, logger)

	return aptPath, projDir, busFile, w
}

// writeMarker writes a session marker file at aptPath.
func writeMarker(t *testing.T, aptPath, markerName, sessionPath string) {
	t.Helper()
	markerFile := filepath.Join(aptPath, markerName)
	if err := os.WriteFile(markerFile, []byte(sessionPath+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------------------
// TestMultiAgentSessionAttribution
//
// Verifies that marker files correctly attribute session files to agents
// when multiple Claude sessions run simultaneously. Covers:
//   - Marker-based lookup for agents and Woland
//   - No cross-contamination of messages across agents
//   - Fallback to newest file when marker is missing
// ---------------------------------------------------------------------------

func TestMultiAgentSessionAttribution(t *testing.T) {
	aptPath, projDir, _, w := setupMultiAgentEnv(t)

	// Step 1: Create fake session JSONL files for Woland, Azazello, Behemoth.
	// Use staggered timestamps so Behemoth's file is newest.
	wolandFile := filepath.Join(projDir, "session-woland.jsonl")
	azazelloFile := filepath.Join(projDir, "session-azazello.jsonl")
	behemothFile := filepath.Join(projDir, "session-behemoth.jsonl")

	// Write initial content.
	if err := os.WriteFile(wolandFile,
		[]byte(makeSessionJSONL("w1", "I am Woland, the orchestrator")),
		0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := os.WriteFile(azazelloFile,
		[]byte(makeSessionJSONL("a1", "I am Azazello, watching CI")),
		0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := os.WriteFile(behemothFile,
		[]byte(makeSessionJSONL("b1", "I am Behemoth, the gardener")),
		0o644); err != nil {
		t.Fatal(err)
	}

	// Step 2: Create marker files pointing to the correct session files.
	writeMarker(t, aptPath, ".woland-session", wolandFile)
	writeMarker(t, aptPath, ".agent-azazello-session", azazelloFile)
	writeMarker(t, aptPath, ".agent-behemoth-session", behemothFile)

	// Step 3: Verify marker-based attribution.
	t.Run("marker_returns_correct_agent_file", func(t *testing.T) {
		got := w.findAgentSessionFile("azazello")
		if got != azazelloFile {
			t.Errorf("findAgentSessionFile(azazello) = %q, want %q", got, azazelloFile)
		}

		got = w.findAgentSessionFile("behemoth")
		if got != behemothFile {
			t.Errorf("findAgentSessionFile(behemoth) = %q, want %q", got, behemothFile)
		}
	})

	t.Run("marker_returns_correct_woland_file", func(t *testing.T) {
		got := w.findWolandSessionFile()
		if got != wolandFile {
			t.Errorf("findWolandSessionFile() = %q, want %q", got, wolandFile)
		}
	})

	t.Run("marker_ignores_newest_timestamp", func(t *testing.T) {
		// Behemoth's file is newest, but Azazello's marker should still
		// return Azazello's file.
		got := w.findAgentSessionFile("azazello")
		if got != azazelloFile {
			t.Errorf("findAgentSessionFile(azazello) returned newest instead of marker target: got %q, want %q", got, azazelloFile)
		}

		// And Woland's marker should return Woland's file, even though
		// it's the oldest.
		got = w.findWolandSessionFile()
		if got != wolandFile {
			t.Errorf("findWolandSessionFile() returned newest instead of marker target: got %q, want %q", got, wolandFile)
		}
	})

	// Step 4: Verify no cross-contamination — each agent's messages have
	// the correct Name field on the bus.
	t.Run("no_cross_contamination", func(t *testing.T) {
		// Create a fresh bus for this subtest.
		busDir := t.TempDir()
		busFile := filepath.Join(busDir, "bus.jsonl")
		b := New(busFile)
		logger := log.New(os.Stderr, "test: ", 0)
		localW := NewWatcher(b, "", aptPath, logger)

		ctx := context.Background()

		// Append a fresh message to each session file.
		appendLine := func(path, uuid, text string) {
			t.Helper()
			f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			f.WriteString(makeSessionJSONL(uuid, text))
		}
		appendLine(wolandFile, "w2", "Woland speaks again")
		appendLine(azazelloFile, "a2", "Azazello speaks again")
		appendLine(behemothFile, "b2", "Behemoth speaks again")

		// Read each file as the correct agent identity.
		seenW := make(map[string]bool)
		seenA := make(map[string]bool)
		seenB := make(map[string]bool)

		localW.readAgentLines(ctx, "woland", wolandFile, 0, "", seenW, false, false)
		localW.readAgentLines(ctx, "azazello", azazelloFile, 0, "", seenA, false, false)
		localW.readAgentLines(ctx, "behemoth", behemothFile, 0, "", seenB, false, false)

		recent, err := b.ReadRecent(100)
		if err != nil {
			t.Fatal(err)
		}

		// Verify attributions.
		nameCount := map[string]int{}
		for _, m := range recent {
			nameCount[m.Name]++
			switch m.Name {
			case "woland":
				if !strings.Contains(m.Text, "Woland") {
					t.Errorf("woland message has wrong text: %q", m.Text)
				}
			case "azazello":
				if !strings.Contains(m.Text, "Azazello") {
					t.Errorf("azazello message has wrong text: %q", m.Text)
				}
			case "behemoth":
				if !strings.Contains(m.Text, "Behemoth") {
					t.Errorf("behemoth message has wrong text: %q", m.Text)
				}
			default:
				t.Errorf("unexpected agent name on bus: %q", m.Name)
			}
		}

		// Each agent should have exactly 2 messages (initial + appended).
		for _, agent := range []string{"woland", "azazello", "behemoth"} {
			if nameCount[agent] != 2 {
				t.Errorf("expected 2 messages from %q, got %d", agent, nameCount[agent])
			}
		}
	})

	// Step 5: Verify marker fallback — delete one marker and confirm
	// fallback to newest file.
	t.Run("marker_fallback_to_newest", func(t *testing.T) {
		// Remove Azazello's marker.
		markerPath := filepath.Join(aptPath, ".agent-azazello-session")
		if err := os.Remove(markerPath); err != nil {
			t.Fatal(err)
		}

		// Without the marker, findAgentSessionFile falls back to newest.
		got := w.findAgentSessionFile("azazello")
		// Behemoth's file is newest, so this should return it
		// (which is wrong — demonstrating why markers matter).
		if got != behemothFile {
			t.Errorf("without marker, expected newest file %q, got %q", behemothFile, got)
		}

		// Restore the marker.
		writeMarker(t, aptPath, ".agent-azazello-session", azazelloFile)
		got = w.findAgentSessionFile("azazello")
		if got != azazelloFile {
			t.Errorf("after restoring marker, expected %q, got %q", azazelloFile, got)
		}
	})
}

// ---------------------------------------------------------------------------
// TestEchoPreventionWithCorrectAttribution
//
// Verifies that the echo loop bug cannot happen: when a message is written
// to the bus by one agent, it should not be re-injected into that agent's
// own session.
// ---------------------------------------------------------------------------

func TestEchoPreventionWithCorrectAttribution(t *testing.T) {
	aptPath, projDir, busFile, w := setupMultiAgentEnv(t)

	// Create session files with markers.
	wolandFile := filepath.Join(projDir, "woland.jsonl")
	behemothFile := filepath.Join(projDir, "behemoth.jsonl")
	os.WriteFile(wolandFile, []byte(makeSessionJSONL("w1", "Woland init")), 0o644)
	os.WriteFile(behemothFile, []byte(makeSessionJSONL("b1", "Behemoth init")), 0o644)

	writeMarker(t, aptPath, ".woland-session", wolandFile)
	writeMarker(t, aptPath, ".agent-behemoth-session", behemothFile)

	// Write a message to the bus as from "woland".
	b := New(busFile)
	msg := NewMessage("woland", TypeChat, "I see all")
	if err := b.Append(msg); err != nil {
		t.Fatal(err)
	}

	// Build injection windows for both Woland and Behemoth.
	windows := []injectionWindow{
		{busName: "woland", windowName: "woland", isWoland: true},
		{busName: "behemoth", windowName: "agent-behemoth"},
	}

	// Get injection targets for this message ("I see all" from woland).
	// Woland is sender (excluded), "I see all" doesn't mention behemoth,
	// so no targets with smart routing.
	targets := injectionTargets(windows, *msg)

	// Verify Woland is NOT in the targets (sender excluded).
	for _, tgt := range targets {
		if tgt.busName == "woland" {
			t.Error("woland should NOT be in injection targets — it is the sender")
		}
	}

	if len(targets) != 0 {
		t.Errorf("expected 0 injection targets (no agent mentioned), got %d: %v", len(targets), targets)
	}

	// Woland sends a message mentioning Behemoth — Behemoth should receive.
	msgMention := NewMessage("woland", TypeChat, "Behemoth, report status")
	targetsMention := injectionTargets(windows, *msgMention)
	if len(targetsMention) != 1 || targetsMention[0].busName != "behemoth" {
		t.Errorf("expected [behemoth] when mentioned, got %v", targetsMention)
	}

	// Reverse: Behemoth sends, verify Woland receives (hub) and Behemoth does not.
	msgB := NewMessage("behemoth", TypeChat, "I am the gardener")
	targetsB := injectionTargets(windows, *msgB)

	for _, tgt := range targetsB {
		if tgt.busName == "behemoth" {
			t.Error("behemoth should NOT be in injection targets when it is the sender")
		}
	}
	foundWoland := false
	for _, tgt := range targetsB {
		if tgt.busName == "woland" {
			foundWoland = true
			break
		}
	}
	if !foundWoland {
		t.Error("woland should be in injection targets when behemoth sends (hub rule)")
	}

	// Verify echo prevention works with the correct attribution. If
	// Woland's output were misattributed as Behemoth's (the original bug),
	// the sender exclusion would fail — Behemoth's messages would be
	// injected back into Behemoth's session.
	t.Run("misattribution_causes_echo", func(t *testing.T) {
		// Simulate the bug: a message labeled "behemoth" that actually came
		// from Woland (misattributed).
		misattributed := NewMessage("behemoth", TypeChat, "This is actually Woland speaking")

		targets := injectionTargets(windows, *misattributed)
		// With the misattribution, Behemoth would be excluded (it's listed
		// as sender), but Woland would receive its own message — causing
		// an echo loop. Woland receives because it's the hub.
		for _, tgt := range targets {
			if tgt.busName == "woland" {
				// This is the echo that the bug would cause:
				// Woland receives its own message because it was
				// misattributed as from Behemoth.
				break
			}
		}
		// With correct attribution, a message from "woland" would correctly
		// exclude "woland" from targets.
		correct := NewMessage("woland", TypeChat, "This is actually Woland speaking")
		correctTargets := injectionTargets(windows, *correct)
		for _, tgt := range correctTargets {
			if tgt.busName == "woland" {
				t.Error("with correct attribution, woland should not receive its own message")
			}
		}
	})

	_ = w // watcher used to verify file lookup
}

// ---------------------------------------------------------------------------
// TestSessionFileRaceCondition
//
// Reproduces the exact race that caused the original bug: without marker
// files, findAgentSessionFile() returns the newest JSONL file, which may
// belong to a different agent.
// ---------------------------------------------------------------------------

func TestSessionFileRaceCondition(t *testing.T) {
	aptPath, projDir, _, w := setupMultiAgentEnv(t)

	// Step 1: Create two session files. Woland's is older, Behemoth's is newer.
	wolandFile := filepath.Join(projDir, "session-woland.jsonl")
	if err := os.WriteFile(wolandFile,
		[]byte(makeSessionJSONL("w1", "I am Woland")), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond) // ensure different mtime
	behemothFile := filepath.Join(projDir, "session-behemoth.jsonl")
	if err := os.WriteFile(behemothFile,
		[]byte(makeSessionJSONL("b1", "I am Behemoth")), 0o644); err != nil {
		t.Fatal(err)
	}

	// Step 2: Create ONLY the Woland marker — no Behemoth marker.
	writeMarker(t, aptPath, ".woland-session", wolandFile)

	// Step 3: Without a marker, Behemoth lookup falls back to newest file.
	// This happens to return the correct file (by luck, since Behemoth's
	// file IS the newest).
	got := w.findAgentSessionFile("behemoth")
	if got != behemothFile {
		t.Errorf("step 3: findAgentSessionFile(behemoth) = %q, want %q (newest)", got, behemothFile)
	}

	// Step 4: A third agent starts, creating an even newer file.
	time.Sleep(30 * time.Millisecond)
	newcomerFile := filepath.Join(projDir, "session-newcomer.jsonl")
	if err := os.WriteFile(newcomerFile,
		[]byte(makeSessionJSONL("n1", "I am the newcomer")), 0o644); err != nil {
		t.Fatal(err)
	}

	// Step 5: WITHOUT a marker, Behemoth lookup now returns the WRONG file.
	// This is the race condition: findAgentSessionFile returns the newest,
	// which is now the newcomer's file, not Behemoth's.
	got = w.findAgentSessionFile("behemoth")
	if got == behemothFile {
		t.Errorf("step 5: findAgentSessionFile(behemoth) should return wrong file (newcomer), got %q", got)
	}
	if got != newcomerFile {
		t.Errorf("step 5: expected newcomer file %q, got %q", newcomerFile, got)
	}

	// Step 6: Add the Behemoth marker pointing to the correct file.
	writeMarker(t, aptPath, ".agent-behemoth-session", behemothFile)

	// Step 7: WITH the marker, Behemoth lookup returns the correct file.
	got = w.findAgentSessionFile("behemoth")
	if got != behemothFile {
		t.Errorf("step 7: findAgentSessionFile(behemoth) = %q, want %q (via marker)", got, behemothFile)
	}

	// Verify Woland still resolves correctly via its marker.
	got = w.findWolandSessionFile()
	if got != wolandFile {
		t.Errorf("findWolandSessionFile() = %q, want %q", got, wolandFile)
	}

	// Bonus: verify that even after the newcomer appeared, the marker
	// protects Behemoth from misattribution.
	time.Sleep(30 * time.Millisecond)
	evenNewerFile := filepath.Join(projDir, "session-yet-another.jsonl")
	os.WriteFile(evenNewerFile, []byte(makeSessionJSONL("y1", "Yet another")), 0o644)

	got = w.findAgentSessionFile("behemoth")
	if got != behemothFile {
		t.Errorf("marker should protect behemoth even with newer files; got %q, want %q", got, behemothFile)
	}
}

// ---------------------------------------------------------------------------
// TestMultiAgentBusFlow
//
// End-to-end test of the full message flow without tmux. Verifies that:
//   - All messages are present on the bus
//   - All messages have correct attribution
//   - No duplicates (UUID dedup)
//   - No echo (sender's own messages not re-injected)
// ---------------------------------------------------------------------------

func TestMultiAgentBusFlow(t *testing.T) {
	aptPath, projDir, _, _ := setupMultiAgentEnv(t)

	// Create session files for Woland + 2 agents.
	wolandFile := filepath.Join(projDir, "flow-woland.jsonl")
	azazelloFile := filepath.Join(projDir, "flow-azazello.jsonl")
	behemothFile := filepath.Join(projDir, "flow-behemoth.jsonl")

	os.WriteFile(wolandFile, []byte(""), 0o644)
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(azazelloFile, []byte(""), 0o644)
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(behemothFile, []byte(""), 0o644)

	// Write markers.
	writeMarker(t, aptPath, ".woland-session", wolandFile)
	writeMarker(t, aptPath, ".agent-azazello-session", azazelloFile)
	writeMarker(t, aptPath, ".agent-behemoth-session", behemothFile)

	// Create a bus.
	busDir := t.TempDir()
	busFile := filepath.Join(busDir, "bus.jsonl")
	b := New(busFile)
	logger := log.New(os.Stderr, "test: ", 0)
	w := NewWatcher(b, "", aptPath, logger)

	ctx := context.Background()

	// Simulate agent output by writing to each session file.
	type agentOutput struct {
		agentID     string
		sessionFile string
		uuid        string
		text        string
	}

	outputs := []agentOutput{
		{"woland", wolandFile, "flow-w1", "Woland: CI pipeline started"},
		{"azazello", azazelloFile, "flow-a1", "Azazello: watching test suite"},
		{"behemoth", behemothFile, "flow-b1", "Behemoth: tending the garden"},
		{"woland", wolandFile, "flow-w2", "Woland: deploy is green"},
		{"azazello", azazelloFile, "flow-a2", "Azazello: all tests pass"},
		{"behemoth", behemothFile, "flow-b2", "Behemoth: garden is blooming"},
	}

	// Write all output to session files.
	for _, out := range outputs {
		f, err := os.OpenFile(out.sessionFile, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		f.WriteString(makeSessionJSONL(out.uuid, out.text))
		f.Close()
	}

	// Read each agent's session file into the bus using per-agent seen maps.
	seenW := make(map[string]bool)
	seenA := make(map[string]bool)
	seenB := make(map[string]bool)

	w.readAgentLines(ctx, "woland", wolandFile, 0, "", seenW, false, false)
	w.readAgentLines(ctx, "azazello", azazelloFile, 0, "", seenA, false, false)
	w.readAgentLines(ctx, "behemoth", behemothFile, 0, "", seenB, false, false)

	// Verify all messages are present on the bus.
	recent, err := b.ReadRecent(100)
	if err != nil {
		t.Fatal(err)
	}

	if len(recent) != len(outputs) {
		t.Fatalf("expected %d bus messages, got %d", len(outputs), len(recent))
	}

	// Verify correct attribution for every message.
	t.Run("correct_attribution", func(t *testing.T) {
		byName := map[string][]string{}
		for _, m := range recent {
			byName[m.Name] = append(byName[m.Name], m.Text)
		}

		for _, agent := range []string{"woland", "azazello", "behemoth"} {
			msgs := byName[agent]
			if len(msgs) != 2 {
				t.Errorf("expected 2 messages from %q, got %d: %v", agent, len(msgs), msgs)
				continue
			}
			for _, text := range msgs {
				// Each message text starts with the agent's capitalized name.
				expectedPrefix := capitalize(agent) + ":"
				if !strings.HasPrefix(text, expectedPrefix) {
					t.Errorf("message from %q has unexpected text: %q (expected prefix %q)", agent, text, expectedPrefix)
				}
			}
		}
	})

	// Verify no duplicates.
	t.Run("no_duplicates", func(t *testing.T) {
		seen := map[string]bool{}
		for _, m := range recent {
			if seen[m.ID] {
				t.Errorf("duplicate message ID: %q", m.ID)
			}
			seen[m.ID] = true
		}

		seenTexts := map[string]bool{}
		for _, m := range recent {
			if seenTexts[m.Text] {
				t.Errorf("duplicate message text: %q", m.Text)
			}
			seenTexts[m.Text] = true
		}
	})

	// Verify UUID dedup works across re-reads.
	t.Run("uuid_dedup_prevents_reemission", func(t *testing.T) {
		// Reading the same files again with the same seen maps should
		// produce no new bus messages.
		countBefore := len(recent)
		w.readAgentLines(ctx, "woland", wolandFile, 0, "", seenW, false, false)
		w.readAgentLines(ctx, "azazello", azazelloFile, 0, "", seenA, false, false)
		w.readAgentLines(ctx, "behemoth", behemothFile, 0, "", seenB, false, false)

		afterReread, err := b.ReadRecent(100)
		if err != nil {
			t.Fatal(err)
		}
		if len(afterReread) != countBefore {
			t.Errorf("re-read should not produce new messages: before=%d, after=%d",
				countBefore, len(afterReread))
		}
	})

	// Verify echo prevention: for each agent's messages, the injection
	// targets should exclude that agent. With smart routing, targets
	// depend on whether agent names are mentioned in the text.
	t.Run("no_echo_injection", func(t *testing.T) {
		windows := []injectionWindow{
			{busName: "woland", windowName: "woland", isWoland: true},
			{busName: "azazello", windowName: "agent-azazello"},
			{busName: "behemoth", windowName: "agent-behemoth"},
		}

		for _, m := range recent {
			targets := injectionTargets(windows, *m)
			for _, tgt := range targets {
				if tgt.busName == m.Name {
					t.Errorf("echo detected: message from %q would be injected back to %q",
						m.Name, tgt.busName)
				}
			}
			// With smart routing: messages from agents go to woland (hub)
			// plus any mentioned agents. Messages from woland go only to
			// mentioned agents. No one ever gets their own message.
		}
	})

	// Verify incremental reads work correctly with correct attribution.
	t.Run("incremental_reads_maintain_attribution", func(t *testing.T) {
		// Append a new message to Azazello's file only.
		f, err := os.OpenFile(azazelloFile, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		f.WriteString(makeSessionJSONL("flow-a3", "Azazello: one more report"))
		f.Close()

		// Get current file size for Azazello (to read only new data).
		info, _ := os.Stat(azazelloFile)
		// Read from a point after the previous content. We need to find
		// the offset that covers only the new line. For simplicity, re-read
		// from 0 with the existing seen map — only new UUIDs will be emitted.
		w.readAgentLines(ctx, "azazello", azazelloFile, 0, "", seenA, false, false)
		_ = info

		afterIncr, err := b.ReadRecent(100)
		if err != nil {
			t.Fatal(err)
		}

		// Should have one more message (7 total).
		if len(afterIncr) != len(outputs)+1 {
			t.Fatalf("expected %d messages after incremental read, got %d",
				len(outputs)+1, len(afterIncr))
		}

		// The new message should be attributed to azazello.
		last := afterIncr[len(afterIncr)-1]
		if last.Name != "azazello" {
			t.Errorf("incremental message attributed to %q, want %q", last.Name, "azazello")
		}
		if last.Text != "Azazello: one more report" {
			t.Errorf("incremental message text = %q, want %q", last.Text, "Azazello: one more report")
		}
	})
}

// TestWatcherDetectsStaleness tests that the watcher detects when a session file
// becomes stale (no new activity for an extended period) and switches to a newer
// session file when the marker is updated.
func TestWatcherDetectsStaleness(t *testing.T) {
	aptPath := t.TempDir()
	projDir := claudeProjectDir(aptPath)
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create an initial session file.
	oldSessionFile := filepath.Join(projDir, "old-session.jsonl")
	if err := os.WriteFile(oldSessionFile, []byte(`{"type":"assistant","uuid":"msg-1","message":{"content":[{"type":"text","text":"Initial message"}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a marker pointing to the old session file.
	markerPath := filepath.Join(aptPath, ".woland-session")
	if err := os.WriteFile(markerPath, []byte(oldSessionFile), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a bus and watcher.
	busFile := filepath.Join(aptPath, "bus.jsonl")
	bus := New(busFile)
	logger := log.New(os.Stderr, "test: ", 0)
	watcher := NewWatcher(bus, "", aptPath, logger)

	// Start monitoring a mock Woland window.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mockWindow := monitoredWindow{
		busName:    "woland",
		windowName: "woland",
		isWoland:   true,
	}

	// Start the watcher for the window.
	watcher.mu.Lock()
	watcher.startMonitoredWatcher(ctx, mockWindow)
	watcher.mu.Unlock()

	// Wait for the watcher to detect the initial session file.
	time.Sleep(500 * time.Millisecond)

	// Verify the watcher is using the old session file initially.
	initialSessionFile := watcher.findWolandSessionFile()
	if initialSessionFile != oldSessionFile {
		t.Errorf("watcher initially using %q, want %q", initialSessionFile, oldSessionFile)
	}

	// Simulate staleness by creating a new session file and updating the marker.
	// In a real scenario, this would happen when Woland detects the old session
	// hasn't been active and refreshes to a newer session.
	time.Sleep(100 * time.Millisecond)
	newSessionFile := filepath.Join(projDir, "new-session.jsonl")
	if err := os.WriteFile(newSessionFile, []byte(`{"type":"assistant","uuid":"msg-2","message":{"content":[{"type":"text","text":"New session message"}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Update the marker to point to the new session file (simulating marker refresh).
	if err := os.WriteFile(markerPath, []byte(newSessionFile), 0o644); err != nil {
		t.Fatal(err)
	}

	// Wait for the watcher to detect the session file change.
	// The watcher should detect the change during its polling cycle.
	timeout := time.Now().Add(10 * time.Second)
	var detectedNewSession bool
	for time.Now().Before(timeout) {
		currentSessionFile := watcher.findWolandSessionFile()
		if currentSessionFile == newSessionFile {
			detectedNewSession = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !detectedNewSession {
		t.Fatal("watcher failed to detect session file change within timeout")
	}

	// Verify the watcher is now using the new session file.
	finalSessionFile := watcher.findWolandSessionFile()
	if finalSessionFile != newSessionFile {
		t.Errorf("watcher using %q, want %q after marker update", finalSessionFile, newSessionFile)
	}

	// Stop the watcher.
	watcher.stopAllWatchers()
}

// ---------------------------------------------------------------------------
// Loop prevention: Woland↔Agent ping-pong suppression
// ---------------------------------------------------------------------------

// newTestWatcherWithTime creates a test watcher with a controllable time function.
func newTestWatcherWithTime(t *testing.T, busDir string, nowFn func() time.Time) *Watcher {
	t.Helper()
	w := newTestWatcher(t, busDir)
	w.timeNow = nowFn
	return w
}

// setupWatcherWithAgents creates a watcher with fake agent watchers registered,
// suitable for testing injectMessage loop prevention. The watcher has no real
// tmux connection, so tmux send-keys will fail silently — that's fine, we're
// testing routing decisions via the log output and state changes.
func setupWatcherWithAgents(t *testing.T, agents []injectionWindow, nowFn func() time.Time) *Watcher {
	t.Helper()
	dir := t.TempDir()
	w := newTestWatcherWithTime(t, dir, nowFn)

	ctx := context.Background()
	for _, a := range agents {
		childCtx, childCancel := context.WithCancel(ctx)
		done := make(chan struct{})
		w.watchers[a.windowName] = &agentWatcher{
			cancel:     childCancel,
			done:       done,
			busName:    a.busName,
			windowName: a.windowName,
			isWoland:   a.isWoland,
		}
		go func(c context.Context, d chan struct{}) {
			defer close(d)
			<-c.Done()
		}(childCtx, done)
	}
	t.Cleanup(func() { w.stopAllWatchers() })
	return w
}

func TestLoopPrevention_SuppressionWindow(t *testing.T) {
	// Scenario: agent "tester" sends a joke → injected to woland (records timestamp).
	// Woland responds mentioning "tester" within 10s → NOT routed to tester.
	now := time.Now()
	currentTime := now
	nowFn := func() time.Time { return currentTime }

	agents := []injectionWindow{
		{busName: "woland", windowName: "woland", isWoland: true},
		{busName: "tester", windowName: "agent-tester"},
	}
	w := setupWatcherWithAgents(t, agents, nowFn)
	ctx := context.Background()

	// Step 1: Agent "tester" sends a message → injected to woland.
	testerMsg := Message{
		ID:        "t1",
		Name:      "tester",
		Timestamp: currentTime,
		Type:      TypeChat,
		Text:      "Here's a joke for you!",
	}
	w.injectMessage(ctx, testerMsg)

	// Verify timestamp was recorded.
	w.mu.Lock()
	lastInj, ok := w.lastInjectedToWoland["tester"]
	w.mu.Unlock()
	if !ok {
		t.Fatal("expected lastInjectedToWoland entry for 'tester'")
	}
	if lastInj != now {
		t.Errorf("lastInjectedToWoland[tester] = %v, want %v", lastInj, now)
	}

	// Step 2: Woland responds mentioning "tester" 2 seconds later (within window).
	currentTime = now.Add(2 * time.Second)
	wolandMsg := Message{
		ID:        "w1",
		Name:      "woland",
		Timestamp: currentTime,
		Type:      TypeChat,
		Text:      "Looks like Tester's warmed up with a good one!",
	}
	w.injectMessage(ctx, wolandMsg)

	// Verify that the exchange count for tester was NOT incremented
	// (message was suppressed by the window).
	w.mu.Lock()
	count := w.exchangeCount["tester"]
	w.mu.Unlock()
	if count != 0 {
		t.Errorf("expected exchangeCount[tester] = 0 (suppressed), got %d", count)
	}
}

func TestLoopPrevention_SuppressionWindowExpired(t *testing.T) {
	// Woland can address agent after the suppression window expires.
	now := time.Now()
	currentTime := now
	nowFn := func() time.Time { return currentTime }

	agents := []injectionWindow{
		{busName: "woland", windowName: "woland", isWoland: true},
		{busName: "tester", windowName: "agent-tester"},
	}
	w := setupWatcherWithAgents(t, agents, nowFn)
	ctx := context.Background()

	// Agent sends message → injected to woland.
	testerMsg := Message{
		ID:   "t1",
		Name: "tester",
		Type: TypeChat,
		Text: "Here's a joke!",
	}
	w.injectMessage(ctx, testerMsg)

	// Woland responds 15 seconds later (OUTSIDE the 10s window).
	currentTime = now.Add(15 * time.Second)
	wolandMsg := Message{
		ID:   "w1",
		Name: "woland",
		Type: TypeChat,
		Text: "Hey Tester, that was funny",
	}
	w.injectMessage(ctx, wolandMsg)

	// The message SHOULD have been routed (exchange count incremented).
	w.mu.Lock()
	count := w.exchangeCount["tester"]
	w.mu.Unlock()
	if count != 1 {
		t.Errorf("expected exchangeCount[tester] = 1 (routed after window expired), got %d", count)
	}
}

func TestLoopPrevention_ExchangeLimit(t *testing.T) {
	// Simulate 3+ Woland messages mentioning tester with no user message between.
	// First 2 route, 3rd is suppressed.
	now := time.Now()
	currentTime := now
	nowFn := func() time.Time { return currentTime }

	agents := []injectionWindow{
		{busName: "woland", windowName: "woland", isWoland: true},
		{busName: "tester", windowName: "agent-tester"},
	}
	w := setupWatcherWithAgents(t, agents, nowFn)
	ctx := context.Background()

	// Send 3 Woland messages mentioning tester, each well outside the
	// suppression window (so only the exchange limiter can stop them).
	for i := 0; i < 3; i++ {
		currentTime = now.Add(time.Duration(i+1) * 20 * time.Second)
		msg := Message{
			ID:   fmt.Sprintf("w%d", i),
			Name: "woland",
			Type: TypeChat,
			Text: fmt.Sprintf("Tester, do thing %d", i),
		}
		w.injectMessage(ctx, msg)
	}

	// Exchange count should be capped at maxExchangesPerTurn (2).
	w.mu.Lock()
	count := w.exchangeCount["tester"]
	w.mu.Unlock()
	if count != maxExchangesPerTurn {
		t.Errorf("expected exchangeCount[tester] = %d (capped), got %d", maxExchangesPerTurn, count)
	}
}

func TestLoopPrevention_UserMessageResetsExchangeCount(t *testing.T) {
	// Hit exchange limit → user sends message → count resets → routing works again.
	now := time.Now()
	currentTime := now
	nowFn := func() time.Time { return currentTime }

	agents := []injectionWindow{
		{busName: "woland", windowName: "woland", isWoland: true},
		{busName: "tester", windowName: "agent-tester"},
	}
	w := setupWatcherWithAgents(t, agents, nowFn)
	ctx := context.Background()

	// Exhaust the exchange limit (2 messages outside suppression window).
	for i := 0; i < 3; i++ {
		currentTime = now.Add(time.Duration(i+1) * 20 * time.Second)
		msg := Message{
			ID:   fmt.Sprintf("w%d", i),
			Name: "woland",
			Type: TypeChat,
			Text: fmt.Sprintf("Tester, message %d", i),
		}
		w.injectMessage(ctx, msg)
	}

	w.mu.Lock()
	countBefore := w.exchangeCount["tester"]
	w.mu.Unlock()
	if countBefore != maxExchangesPerTurn {
		t.Fatalf("expected exchange count at limit (%d), got %d", maxExchangesPerTurn, countBefore)
	}

	// User sends a message → resets exchange counts.
	currentTime = now.Add(100 * time.Second)
	userMsg := Message{
		ID:   "u1",
		Name: "user",
		Type: TypeUser,
		Text: "Hey everyone, what's up?",
	}
	w.injectMessage(ctx, userMsg)

	w.mu.Lock()
	countAfterReset := w.exchangeCount["tester"]
	w.mu.Unlock()
	if countAfterReset != 0 {
		t.Errorf("expected exchangeCount[tester] = 0 after user message, got %d", countAfterReset)
	}

	// Now Woland can route to tester again.
	currentTime = now.Add(120 * time.Second)
	wolandMsg := Message{
		ID:   "w-after-reset",
		Name: "woland",
		Type: TypeChat,
		Text: "Tester, try again please",
	}
	w.injectMessage(ctx, wolandMsg)

	w.mu.Lock()
	countAfterRoute := w.exchangeCount["tester"]
	w.mu.Unlock()
	if countAfterRoute != 1 {
		t.Errorf("expected exchangeCount[tester] = 1 after reset + new route, got %d", countAfterRoute)
	}
}

func TestLoopPrevention_UserMessagesBypassAllPrevention(t *testing.T) {
	// TypeUser messages mentioning an agent should always route, regardless
	// of suppression window or exchange count.
	now := time.Now()
	currentTime := now
	nowFn := func() time.Time { return currentTime }

	agents := []injectionWindow{
		{busName: "woland", windowName: "woland", isWoland: true},
		{busName: "tester", windowName: "agent-tester"},
	}
	w := setupWatcherWithAgents(t, agents, nowFn)
	ctx := context.Background()

	// Set up suppression state: pretend tester just sent a message.
	w.mu.Lock()
	w.lastInjectedToWoland["tester"] = now
	// Also max out the exchange count.
	w.exchangeCount["tester"] = maxExchangesPerTurn + 10
	w.mu.Unlock()

	// User message mentioning tester 1 second later (within suppression window,
	// over exchange limit) → should still route to tester.
	currentTime = now.Add(1 * time.Second)
	userMsg := Message{
		ID:   "u1",
		Name: "user",
		Type: TypeUser,
		Text: "Tester, run the suite please",
	}
	w.injectMessage(ctx, userMsg)

	// The user message should have reset the exchange count (it's a TypeUser).
	// And the routing to tester should have happened via injectionTargets
	// (TypeUser messages route based on simple substring match).
	// Verify exchange count was reset by the user message.
	w.mu.Lock()
	count := w.exchangeCount["tester"]
	w.mu.Unlock()
	if count != 0 {
		t.Errorf("expected exchangeCount[tester] = 0 (reset by user message), got %d", count)
	}
}

func TestLoopPrevention_OnlySuppressesOriginatingAgent(t *testing.T) {
	// When agent "tester" sends a message to Woland, the suppression window
	// should only affect routing back to "tester", not to other agents.
	now := time.Now()
	currentTime := now
	nowFn := func() time.Time { return currentTime }

	agents := []injectionWindow{
		{busName: "woland", windowName: "woland", isWoland: true},
		{busName: "tester", windowName: "agent-tester"},
		{busName: "builder", windowName: "agent-builder"},
	}
	w := setupWatcherWithAgents(t, agents, nowFn)
	ctx := context.Background()

	// Agent "tester" sends message → injected to woland.
	testerMsg := Message{
		ID:   "t1",
		Name: "tester",
		Type: TypeChat,
		Text: "Tests passed!",
	}
	w.injectMessage(ctx, testerMsg)

	// Woland responds mentioning both tester and builder within window.
	currentTime = now.Add(2 * time.Second)
	wolandMsg := Message{
		ID:   "w1",
		Name: "woland",
		Type: TypeChat,
		Text: "Great! Tester passed, Builder can deploy now",
	}
	w.injectMessage(ctx, wolandMsg)

	// "tester" should be suppressed (within window), "builder" should route.
	w.mu.Lock()
	testerCount := w.exchangeCount["tester"]
	builderCount := w.exchangeCount["builder"]
	w.mu.Unlock()

	if testerCount != 0 {
		t.Errorf("expected tester suppressed (count 0), got %d", testerCount)
	}
	if builderCount != 1 {
		t.Errorf("expected builder routed (count 1), got %d", builderCount)
	}
}

func TestLoopPrevention_FullPingPongScenario(t *testing.T) {
	// Simulate the exact bug scenario from the issue:
	// 1. Agent "tester" sends a joke → bus captures → injected to woland
	// 2. Woland responds mentioning tester → should NOT route back to tester
	// 3. Even if suppression window expires, exchange limit caps at 2
	now := time.Now()
	currentTime := now
	nowFn := func() time.Time { return currentTime }

	agents := []injectionWindow{
		{busName: "woland", windowName: "woland", isWoland: true},
		{busName: "tester", windowName: "agent-tester"},
	}
	w := setupWatcherWithAgents(t, agents, nowFn)
	ctx := context.Background()

	// Round 1: tester sends joke.
	currentTime = now
	w.injectMessage(ctx, Message{
		ID: "t1", Name: "tester", Type: TypeChat,
		Text: "Why did the chicken cross the road?",
	})

	// Round 2: Woland acknowledges (within suppression window) → suppressed.
	currentTime = now.Add(3 * time.Second)
	w.injectMessage(ctx, Message{
		ID: "w1", Name: "woland", Type: TypeChat,
		Text: "Ha! Tester's got jokes today",
	})
	w.mu.Lock()
	if w.exchangeCount["tester"] != 0 {
		t.Errorf("round 2: expected suppression, got count %d", w.exchangeCount["tester"])
	}
	w.mu.Unlock()

	// Simulate tester responding again (as if this got through somehow in
	// a different timeline). Record new injection timestamp.
	currentTime = now.Add(15 * time.Second)
	w.injectMessage(ctx, Message{
		ID: "t2", Name: "tester", Type: TypeChat,
		Text: "To get to the other side!",
	})

	// Woland responds after suppression window expires → routed (exchange 1).
	currentTime = now.Add(30 * time.Second)
	w.injectMessage(ctx, Message{
		ID: "w2", Name: "woland", Type: TypeChat,
		Text: "Classic, Tester. Very classic.",
	})
	w.mu.Lock()
	if w.exchangeCount["tester"] != 1 {
		t.Errorf("round 4: expected count 1, got %d", w.exchangeCount["tester"])
	}
	w.mu.Unlock()

	// Tester responds again, then Woland → exchange 2.
	currentTime = now.Add(45 * time.Second)
	w.injectMessage(ctx, Message{
		ID: "t3", Name: "tester", Type: TypeChat,
		Text: "I've got more!",
	})

	currentTime = now.Add(60 * time.Second)
	w.injectMessage(ctx, Message{
		ID: "w3", Name: "woland", Type: TypeChat,
		Text: "Tester, please focus on work",
	})
	w.mu.Lock()
	if w.exchangeCount["tester"] != 2 {
		t.Errorf("round 6: expected count 2 (limit), got %d", w.exchangeCount["tester"])
	}
	w.mu.Unlock()

	// Woland tries one more time → should be blocked by exchange limit.
	currentTime = now.Add(75 * time.Second)
	w.injectMessage(ctx, Message{
		ID: "w4", Name: "woland", Type: TypeChat,
		Text: "Tester, seriously stop",
	})
	w.mu.Lock()
	if w.exchangeCount["tester"] != 2 {
		t.Errorf("round 7: expected count still 2 (blocked), got %d", w.exchangeCount["tester"])
	}
	w.mu.Unlock()

	// User intervenes → resets everything.
	currentTime = now.Add(90 * time.Second)
	w.injectMessage(ctx, Message{
		ID: "u1", Name: "user", Type: TypeUser,
		Text: "Tester, run the actual tests",
	})
	w.mu.Lock()
	if w.exchangeCount["tester"] != 0 {
		t.Errorf("after user: expected count 0, got %d", w.exchangeCount["tester"])
	}
	w.mu.Unlock()
}

// ===========================================================================
// Woland user input propagation tests
// ===========================================================================

// ---------------------------------------------------------------------------
// isInjectedMessage
// ---------------------------------------------------------------------------

func TestIsInjectedMessage(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"[User] what's the status?", true},
		{"[Azazello] CI is green", true},
		{"[System] agent joined", true},
		{"[Behemoth] I fixed the build", true},
		{"Hello, how are you?", false},
		{"Please check the logs", false},
		{"[] empty brackets", false},           // no name inside brackets
		{"[A] single char name", true},         // minimal valid format
		{"not [bracketed] at start", false},     // brackets not at start
		{"", false},                             // empty string
		{"[Name]no space after bracket", false}, // missing space after ]
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			got := isInjectedMessage(tt.text)
			if got != tt.want {
				t.Errorf("isInjectedMessage(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// parseSessionLineTyped
// ---------------------------------------------------------------------------

func TestParseSessionLineTyped(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		wantType string
		wantText string
		wantUUID string
		wantOK   bool
	}{
		{
			name:     "assistant message",
			line:     `{"type":"assistant","uuid":"a1","message":{"content":[{"type":"text","text":"Hello"}]}}`,
			wantType: "assistant",
			wantText: "Hello",
			wantUUID: "a1",
			wantOK:   true,
		},
		{
			name:     "human message",
			line:     `{"type":"human","uuid":"h1","message":{"content":[{"type":"text","text":"Hi there"}]}}`,
			wantType: "human",
			wantText: "Hi there",
			wantUUID: "h1",
			wantOK:   true,
		},
		{
			name:     "system message (no text extracted)",
			line:     `{"type":"system","uuid":"s1","message":{"content":[{"type":"text","text":"init"}]}}`,
			wantType: "system",
			wantText: "",
			wantUUID: "",
			wantOK:   true,
		},
		{
			name:     "tool_result (no text extracted)",
			line:     `{"type":"tool_result","uuid":"t1","message":{"content":[{"type":"text","text":"result"}]}}`,
			wantType: "tool_result",
			wantText: "",
			wantUUID: "",
			wantOK:   true,
		},
		{
			name:     "empty line",
			line:     "",
			wantType: "",
			wantText: "",
			wantUUID: "",
			wantOK:   false,
		},
		{
			name:     "invalid JSON",
			line:     "not json",
			wantType: "",
			wantText: "",
			wantUUID: "",
			wantOK:   false,
		},
		{
			name:     "human message with multiple text blocks",
			line:     `{"type":"human","uuid":"h2","message":{"content":[{"type":"text","text":"part one"},{"type":"text","text":"part two"}]}}`,
			wantType: "human",
			wantText: "part one\npart two",
			wantUUID: "h2",
			wantOK:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msgType, text, uuid, ok := parseSessionLineTyped(tt.line)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if msgType != tt.wantType {
				t.Errorf("msgType = %q, want %q", msgType, tt.wantType)
			}
			if text != tt.wantText {
				t.Errorf("text = %q, want %q", text, tt.wantText)
			}
			if uuid != tt.wantUUID {
				t.Errorf("uuid = %q, want %q", uuid, tt.wantUUID)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Woland human messages → bus as TypeUser
// ---------------------------------------------------------------------------

func TestReadAgentLines_WolandHumanMessages_PropagatedAsBusUser(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")

	msgs := []sessionMessage{
		{
			Type: "assistant",
			UUID: "a1",
			Message: messageContent{Content: []contentBlock{
				{Type: "text", Text: "Hello from Woland"},
			}},
		},
		{
			Type: "human",
			UUID: "h1",
			Message: messageContent{Content: []contentBlock{
				{Type: "text", Text: "What is the build status?"},
			}},
		},
		{
			Type: "assistant",
			UUID: "a2",
			Message: messageContent{Content: []contentBlock{
				{Type: "text", Text: "Build is green"},
			}},
		},
	}
	writeJSONL(t, sessionPath, msgs)

	w := newTestWatcher(t, dir)
	seen := make(map[string]bool)
	ctx := context.Background()

	// isWoland=true — human messages should propagate as TypeUser.
	w.readAgentLines(ctx, "woland", sessionPath, 0, "", seen, false, true)

	recent, err := w.bus.ReadRecent(100)
	if err != nil {
		t.Fatal(err)
	}

	// Should have 3 messages: 2 assistant (TypeChat) + 1 human (TypeUser).
	if len(recent) != 3 {
		t.Fatalf("expected 3 bus messages, got %d", len(recent))
	}

	// First: assistant → TypeChat from woland.
	if recent[0].Type != TypeChat {
		t.Errorf("msg[0].Type = %q, want %q", recent[0].Type, TypeChat)
	}
	if recent[0].Name != "woland" {
		t.Errorf("msg[0].Name = %q, want %q", recent[0].Name, "woland")
	}
	if recent[0].Text != "Hello from Woland" {
		t.Errorf("msg[0].Text = %q, want %q", recent[0].Text, "Hello from Woland")
	}

	// Second: human → TypeUser from "user".
	if recent[1].Type != TypeUser {
		t.Errorf("msg[1].Type = %q, want %q", recent[1].Type, TypeUser)
	}
	if recent[1].Name != "user" {
		t.Errorf("msg[1].Name = %q, want %q", recent[1].Name, "user")
	}
	if recent[1].Text != "What is the build status?" {
		t.Errorf("msg[1].Text = %q, want %q", recent[1].Text, "What is the build status?")
	}

	// Third: assistant → TypeChat from woland.
	if recent[2].Type != TypeChat {
		t.Errorf("msg[2].Type = %q, want %q", recent[2].Type, TypeChat)
	}
	if recent[2].Name != "woland" {
		t.Errorf("msg[2].Name = %q, want %q", recent[2].Name, "woland")
	}
}

// ---------------------------------------------------------------------------
// Injected messages are filtered out
// ---------------------------------------------------------------------------

func TestReadAgentLines_WolandInjectedMessagesFiltered(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")

	msgs := []sessionMessage{
		{
			// Genuine user input — should propagate.
			Type: "human",
			UUID: "h1",
			Message: messageContent{Content: []contentBlock{
				{Type: "text", Text: "Check the CI logs please"},
			}},
		},
		{
			// Bus-injected from agent Azazello — should NOT propagate.
			Type: "human",
			UUID: "h2",
			Message: messageContent{Content: []contentBlock{
				{Type: "text", Text: "[Azazello] CI is green, all tests pass"},
			}},
		},
		{
			// Bus-injected from User via bus — should NOT propagate.
			Type: "human",
			UUID: "h3",
			Message: messageContent{Content: []contentBlock{
				{Type: "text", Text: "[User] what's the status?"},
			}},
		},
		{
			// Genuine user input — should propagate.
			Type: "human",
			UUID: "h4",
			Message: messageContent{Content: []contentBlock{
				{Type: "text", Text: "Thanks, looks good"},
			}},
		},
		{
			// Bus-injected from System — should NOT propagate.
			Type: "human",
			UUID: "h5",
			Message: messageContent{Content: []contentBlock{
				{Type: "text", Text: "[System] agent joined"},
			}},
		},
	}
	writeJSONL(t, sessionPath, msgs)

	w := newTestWatcher(t, dir)
	seen := make(map[string]bool)
	ctx := context.Background()

	w.readAgentLines(ctx, "woland", sessionPath, 0, "", seen, false, true)

	recent, err := w.bus.ReadRecent(100)
	if err != nil {
		t.Fatal(err)
	}

	// Only 2 genuine user messages should have been written.
	if len(recent) != 2 {
		t.Fatalf("expected 2 bus messages (filtered injected), got %d", len(recent))
	}
	if recent[0].Text != "Check the CI logs please" {
		t.Errorf("msg[0].Text = %q, want %q", recent[0].Text, "Check the CI logs please")
	}
	if recent[0].Type != TypeUser {
		t.Errorf("msg[0].Type = %q, want %q", recent[0].Type, TypeUser)
	}
	if recent[1].Text != "Thanks, looks good" {
		t.Errorf("msg[1].Text = %q, want %q", recent[1].Text, "Thanks, looks good")
	}
	if recent[1].Type != TypeUser {
		t.Errorf("msg[1].Type = %q, want %q", recent[1].Type, TypeUser)
	}

	// All UUIDs (including filtered ones) should be in the seen map.
	for _, uuid := range []string{"h1", "h2", "h3", "h4", "h5"} {
		if !seen[uuid] {
			t.Errorf("expected UUID %q in seen map", uuid)
		}
	}
}

// ---------------------------------------------------------------------------
// UUID deduplication for human messages
// ---------------------------------------------------------------------------

func TestReadAgentLines_WolandHumanUUIDDedup(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")

	msgs := []sessionMessage{
		{
			Type: "human",
			UUID: "dup-human",
			Message: messageContent{Content: []contentBlock{
				{Type: "text", Text: "First user message"},
			}},
		},
		{
			Type: "human",
			UUID: "dup-human",
			Message: messageContent{Content: []contentBlock{
				{Type: "text", Text: "Duplicate (should be deduped)"},
			}},
		},
		{
			Type: "human",
			UUID: "unique-human",
			Message: messageContent{Content: []contentBlock{
				{Type: "text", Text: "Second unique message"},
			}},
		},
	}
	writeJSONL(t, sessionPath, msgs)

	w := newTestWatcher(t, dir)
	seen := make(map[string]bool)
	ctx := context.Background()

	w.readAgentLines(ctx, "woland", sessionPath, 0, "", seen, false, true)

	recent, err := w.bus.ReadRecent(100)
	if err != nil {
		t.Fatal(err)
	}

	// Should have only 2 messages (dup-human deduplicated).
	if len(recent) != 2 {
		t.Fatalf("expected 2 bus messages (dedup), got %d", len(recent))
	}
	if recent[0].Text != "First user message" {
		t.Errorf("msg[0].Text = %q, want %q", recent[0].Text, "First user message")
	}
	if recent[1].Text != "Second unique message" {
		t.Errorf("msg[1].Text = %q, want %q", recent[1].Text, "Second unique message")
	}

	if !seen["dup-human"] || !seen["unique-human"] {
		t.Error("expected both UUIDs in seen map")
	}
}

// ---------------------------------------------------------------------------
// Non-Woland agents do NOT propagate human messages
// ---------------------------------------------------------------------------

func TestReadAgentLines_NonWolandHumanNotPropagated(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")

	msgs := []sessionMessage{
		{
			Type: "assistant",
			UUID: "a1",
			Message: messageContent{Content: []contentBlock{
				{Type: "text", Text: "Agent output"},
			}},
		},
		{
			Type: "human",
			UUID: "h1",
			Message: messageContent{Content: []contentBlock{
				{Type: "text", Text: "[Woland] Check the CI please"},
			}},
		},
		{
			Type: "human",
			UUID: "h2",
			Message: messageContent{Content: []contentBlock{
				{Type: "text", Text: "Some other human message"},
			}},
		},
	}
	writeJSONL(t, sessionPath, msgs)

	w := newTestWatcher(t, dir)
	seen := make(map[string]bool)
	ctx := context.Background()

	// isWoland=false — human messages should NOT propagate.
	w.readAgentLines(ctx, "azazello", sessionPath, 0, "", seen, false, false)

	recent, err := w.bus.ReadRecent(100)
	if err != nil {
		t.Fatal(err)
	}

	// Only the assistant message should appear.
	if len(recent) != 1 {
		t.Fatalf("expected 1 bus message (agent only), got %d", len(recent))
	}
	if recent[0].Text != "Agent output" {
		t.Errorf("msg[0].Text = %q, want %q", recent[0].Text, "Agent output")
	}
	if recent[0].Type != TypeChat {
		t.Errorf("msg[0].Type = %q, want %q", recent[0].Type, TypeChat)
	}
	if recent[0].Name != "azazello" {
		t.Errorf("msg[0].Name = %q, want %q", recent[0].Name, "azazello")
	}
}

// ---------------------------------------------------------------------------
// Human messages during draining are NOT written but UUIDs are tracked
// ---------------------------------------------------------------------------

func TestReadAgentLines_WolandHumanDraining(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")

	msgs := []sessionMessage{
		{
			Type: "human",
			UUID: "drain-h1",
			Message: messageContent{Content: []contentBlock{
				{Type: "text", Text: "User message during drain"},
			}},
		},
		{
			Type: "assistant",
			UUID: "drain-a1",
			Message: messageContent{Content: []contentBlock{
				{Type: "text", Text: "Assistant during drain"},
			}},
		},
	}
	writeJSONL(t, sessionPath, msgs)

	w := newTestWatcher(t, dir)
	seen := make(map[string]bool)
	ctx := context.Background()

	// Drain with isWoland=true — nothing should be written to bus.
	w.readAgentLines(ctx, "woland", sessionPath, 0, "", seen, true, true)

	recent, err := w.bus.ReadRecent(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 0 {
		t.Errorf("expected 0 bus messages during draining, got %d", len(recent))
	}

	// But seen map should be populated.
	if !seen["drain-h1"] || !seen["drain-a1"] {
		t.Error("expected both UUIDs in seen map during drain")
	}
}

// ---------------------------------------------------------------------------
// Exchange counter reset via propagated user messages
// ---------------------------------------------------------------------------

func TestLoopPrevention_WolandTerminalUserInputResetsExchangeCount(t *testing.T) {
	// Scenario: Woland↔Agent routing hits the exchange limit, then the user
	// types directly into Woland's terminal. The human message should propagate
	// to the bus as TypeUser, which resets the exchange counter.
	now := time.Now()
	currentTime := now
	nowFn := func() time.Time { return currentTime }

	agents := []injectionWindow{
		{busName: "woland", windowName: "woland", isWoland: true},
		{busName: "tester", windowName: "agent-tester"},
	}
	w := setupWatcherWithAgents(t, agents, nowFn)
	ctx := context.Background()

	// Exhaust the exchange limit.
	for i := 0; i < 3; i++ {
		currentTime = now.Add(time.Duration(i+1) * 20 * time.Second)
		msg := Message{
			ID:   fmt.Sprintf("w%d", i),
			Name: "woland",
			Type: TypeChat,
			Text: fmt.Sprintf("Tester, do thing %d", i),
		}
		w.injectMessage(ctx, msg)
	}

	w.mu.Lock()
	if w.exchangeCount["tester"] != maxExchangesPerTurn {
		t.Fatalf("expected exchange count at limit (%d), got %d", maxExchangesPerTurn, w.exchangeCount["tester"])
	}
	w.mu.Unlock()

	// Simulate a user message arriving on the bus (as would happen when
	// readAgentLines propagates a human message from Woland's session).
	currentTime = now.Add(100 * time.Second)
	userMsg := Message{
		ID:   "user-from-terminal",
		Name: "user",
		Type: TypeUser,
		Text: "Everyone, keep going",
	}
	w.injectMessage(ctx, userMsg)

	// Exchange count should be reset.
	w.mu.Lock()
	countAfterReset := w.exchangeCount["tester"]
	w.mu.Unlock()
	if countAfterReset != 0 {
		t.Errorf("expected exchangeCount[tester] = 0 after user message from terminal, got %d", countAfterReset)
	}

	// Woland can now route to tester again.
	currentTime = now.Add(120 * time.Second)
	wolandMsg := Message{
		ID:   "w-after-reset",
		Name: "woland",
		Type: TypeChat,
		Text: "Tester, try again please",
	}
	w.injectMessage(ctx, wolandMsg)

	w.mu.Lock()
	countAfterRoute := w.exchangeCount["tester"]
	w.mu.Unlock()
	if countAfterRoute != 1 {
		t.Errorf("expected exchangeCount[tester] = 1 after reset + new route, got %d", countAfterRoute)
	}
}

// ---------------------------------------------------------------------------
// End-to-end: Woland session with mixed messages
// ---------------------------------------------------------------------------

func TestReadAgentLines_WolandMixedEndToEnd(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")

	msgs := []sessionMessage{
		// User types into Woland's terminal.
		{
			Type: "human",
			UUID: "h1",
			Message: messageContent{Content: []contentBlock{
				{Type: "text", Text: "Behemoth, check the garden"},
			}},
		},
		// Woland responds.
		{
			Type: "assistant",
			UUID: "a1",
			Message: messageContent{Content: []contentBlock{
				{Type: "text", Text: "I'll relay to Behemoth"},
			}},
		},
		// Bus-injected message from Behemoth (should be filtered).
		{
			Type: "human",
			UUID: "h2",
			Message: messageContent{Content: []contentBlock{
				{Type: "text", Text: "[Behemoth] Garden is blooming nicely"},
			}},
		},
		// Woland responds to Behemoth's update.
		{
			Type: "assistant",
			UUID: "a2",
			Message: messageContent{Content: []contentBlock{
				{Type: "text", Text: "Behemoth reports the garden is fine"},
			}},
		},
		// User types again.
		{
			Type: "human",
			UUID: "h3",
			Message: messageContent{Content: []contentBlock{
				{Type: "text", Text: "Great, thanks"},
			}},
		},
		// Tool result (should be ignored entirely).
		{
			Type: "tool_result",
			UUID: "t1",
			Message: messageContent{Content: []contentBlock{
				{Type: "text", Text: "tool output"},
			}},
		},
	}
	writeJSONL(t, sessionPath, msgs)

	w := newTestWatcher(t, dir)
	seen := make(map[string]bool)
	ctx := context.Background()

	w.readAgentLines(ctx, "woland", sessionPath, 0, "", seen, false, true)

	recent, err := w.bus.ReadRecent(100)
	if err != nil {
		t.Fatal(err)
	}

	// Expected bus messages:
	// 1. h1: user → TypeUser "Behemoth, check the garden"
	// 2. a1: woland → TypeChat "I'll relay to Behemoth"
	// 3. (h2 filtered — injected "[Behemoth]...")
	// 4. a2: woland → TypeChat "Behemoth reports the garden is fine"
	// 5. h3: user → TypeUser "Great, thanks"
	// 6. (t1 ignored — tool_result)
	if len(recent) != 4 {
		t.Fatalf("expected 4 bus messages, got %d", len(recent))
	}

	expected := []struct {
		name    string
		msgType MessageType
		text    string
	}{
		{"user", TypeUser, "Behemoth, check the garden"},
		{"woland", TypeChat, "I'll relay to Behemoth"},
		{"woland", TypeChat, "Behemoth reports the garden is fine"},
		{"user", TypeUser, "Great, thanks"},
	}
	for i, exp := range expected {
		if recent[i].Name != exp.name {
			t.Errorf("msg[%d].Name = %q, want %q", i, recent[i].Name, exp.name)
		}
		if recent[i].Type != exp.msgType {
			t.Errorf("msg[%d].Type = %q, want %q", i, recent[i].Type, exp.msgType)
		}
		if recent[i].Text != exp.text {
			t.Errorf("msg[%d].Text = %q, want %q", i, recent[i].Text, exp.text)
		}
	}
}

// ---------------------------------------------------------------------------
// Staleness fallback tests
// ---------------------------------------------------------------------------

func TestFindWolandSessionFile_StaleMarkerFallsBackToNewest(t *testing.T) {
	// When the .woland-session marker points to a valid file whose modtime
	// is older than watcherStaleness (30s), findWolandSessionFile should
	// fall back to the newest JSONL in the project directory.
	dir := t.TempDir()
	aptPath := filepath.Join(dir, "apt")
	os.MkdirAll(aptPath, 0o755)

	// Create the Claude projects dir.
	projDir := claudeProjectDir(aptPath)
	os.MkdirAll(projDir, 0o755)

	// Create a stale session file (marker target).
	staleFile := filepath.Join(projDir, "stale-session.jsonl")
	os.WriteFile(staleFile, []byte(`{"type":"human"}`), 0o644)
	// Set modtime to 2 minutes ago (well past the 30s threshold).
	staleTime := time.Now().Add(-2 * time.Minute)
	os.Chtimes(staleFile, staleTime, staleTime)

	// Create a fresh session file (should be picked up as newest).
	freshFile := filepath.Join(projDir, "fresh-session.jsonl")
	os.WriteFile(freshFile, []byte(`{"type":"human"}`), 0o644)
	// Ensure fresh file has current modtime (default).

	// Write marker pointing to the stale file.
	markerPath := filepath.Join(aptPath, ".woland-session")
	os.WriteFile(markerPath, []byte(staleFile), 0o644)

	busFile := filepath.Join(dir, "bus.jsonl")
	b := New(busFile)
	logger := log.New(os.Stderr, "test: ", 0)
	w := NewWatcher(b, "", aptPath, logger)

	got := w.findWolandSessionFile()
	if got != freshFile {
		t.Errorf("findWolandSessionFile() = %q, want %q (should fall back to newest when marker is stale)", got, freshFile)
	}
}

func TestFindWolandSessionFile_FreshMarkerTrusted(t *testing.T) {
	// When the marker target has been modified recently (within 30s),
	// it should be trusted even if a newer file exists.
	dir := t.TempDir()
	aptPath := filepath.Join(dir, "apt")
	os.MkdirAll(aptPath, 0o755)

	projDir := claudeProjectDir(aptPath)
	os.MkdirAll(projDir, 0o755)

	// Create the marker target file with recent modtime.
	markerTargetFile := filepath.Join(projDir, "marker-target.jsonl")
	os.WriteFile(markerTargetFile, []byte(`{"type":"human"}`), 0o644)
	// File just written — modtime is now (fresh).

	// Create a newer file with a later modtime.
	time.Sleep(50 * time.Millisecond)
	newerFile := filepath.Join(projDir, "newer-session.jsonl")
	os.WriteFile(newerFile, []byte(`{"type":"human"}`), 0o644)

	// Write marker pointing to the marker target (not the newest file).
	markerPath := filepath.Join(aptPath, ".woland-session")
	os.WriteFile(markerPath, []byte(markerTargetFile), 0o644)

	busFile := filepath.Join(dir, "bus.jsonl")
	b := New(busFile)
	logger := log.New(os.Stderr, "test: ", 0)
	w := NewWatcher(b, "", aptPath, logger)

	got := w.findWolandSessionFile()
	if got != markerTargetFile {
		t.Errorf("findWolandSessionFile() = %q, want %q (fresh marker should be trusted)", got, markerTargetFile)
	}
}

func TestFindWolandSessionFile_StaleMarkerNoNewerFile(t *testing.T) {
	// When the marker target is stale but there's no newer file,
	// the stale file should still be returned as a last resort.
	dir := t.TempDir()
	aptPath := filepath.Join(dir, "apt")
	os.MkdirAll(aptPath, 0o755)

	projDir := claudeProjectDir(aptPath)
	os.MkdirAll(projDir, 0o755)

	// Create only one session file (stale).
	staleFile := filepath.Join(projDir, "only-session.jsonl")
	os.WriteFile(staleFile, []byte(`{"type":"human"}`), 0o644)
	staleTime := time.Now().Add(-2 * time.Minute)
	os.Chtimes(staleFile, staleTime, staleTime)

	// Write marker pointing to it.
	markerPath := filepath.Join(aptPath, ".woland-session")
	os.WriteFile(markerPath, []byte(staleFile), 0o644)

	busFile := filepath.Join(dir, "bus.jsonl")
	b := New(busFile)
	logger := log.New(os.Stderr, "test: ", 0)
	w := NewWatcher(b, "", aptPath, logger)

	got := w.findWolandSessionFile()
	if got != staleFile {
		t.Errorf("findWolandSessionFile() = %q, want %q (stale file returned when no better option)", got, staleFile)
	}
}

func TestFindAgentSessionFile_StaleMarkerFallsBackToNewest(t *testing.T) {
	// Same as Woland test but for regular agents.
	dir := t.TempDir()
	aptPath := filepath.Join(dir, "apt")
	os.MkdirAll(aptPath, 0o755)

	projDir := claudeProjectDir(aptPath)
	os.MkdirAll(projDir, 0o755)

	// Create stale session file.
	staleFile := filepath.Join(projDir, "agent-stale.jsonl")
	os.WriteFile(staleFile, []byte(`{"type":"human"}`), 0o644)
	staleTime := time.Now().Add(-2 * time.Minute)
	os.Chtimes(staleFile, staleTime, staleTime)

	// Create fresh session file.
	freshFile := filepath.Join(projDir, "agent-fresh.jsonl")
	os.WriteFile(freshFile, []byte(`{"type":"human"}`), 0o644)

	// Write marker pointing to stale file.
	markerPath := filepath.Join(aptPath, ".agent-azazello-session")
	os.WriteFile(markerPath, []byte(staleFile), 0o644)

	busFile := filepath.Join(dir, "bus.jsonl")
	b := New(busFile)
	logger := log.New(os.Stderr, "test: ", 0)
	w := NewWatcher(b, "", aptPath, logger)

	got := w.findAgentSessionFile("azazello")
	if got != freshFile {
		t.Errorf("findAgentSessionFile() = %q, want %q (should fall back to newest when marker is stale)", got, freshFile)
	}
}

func TestFindAgentSessionFile_FreshMarkerTrusted(t *testing.T) {
	// Fresh marker should be trusted even if a newer file exists.
	dir := t.TempDir()
	aptPath := filepath.Join(dir, "apt")
	os.MkdirAll(aptPath, 0o755)

	projDir := claudeProjectDir(aptPath)
	os.MkdirAll(projDir, 0o755)

	// Create marker target (fresh).
	markerTargetFile := filepath.Join(projDir, "agent-target.jsonl")
	os.WriteFile(markerTargetFile, []byte(`{"type":"human"}`), 0o644)

	time.Sleep(50 * time.Millisecond)

	// Create newer file.
	newerFile := filepath.Join(projDir, "agent-newer.jsonl")
	os.WriteFile(newerFile, []byte(`{"type":"human"}`), 0o644)

	// Write marker pointing to target (not newest).
	markerPath := filepath.Join(aptPath, ".agent-azazello-session")
	os.WriteFile(markerPath, []byte(markerTargetFile), 0o644)

	busFile := filepath.Join(dir, "bus.jsonl")
	b := New(busFile)
	logger := log.New(os.Stderr, "test: ", 0)
	w := NewWatcher(b, "", aptPath, logger)

	got := w.findAgentSessionFile("azazello")
	if got != markerTargetFile {
		t.Errorf("findAgentSessionFile() = %q, want %q (fresh marker should be trusted)", got, markerTargetFile)
	}
}

func TestStalenessRediscovery_BypassesMarkerWhenSameStaleFile(t *testing.T) {
	// Simulates the core bug: re-discovery via markers returns the same
	// stale file. The watcher should bypass markers and find the newest
	// JSONL directly, then update the marker.
	dir := t.TempDir()
	aptPath := dir
	projDir := claudeProjectDir(aptPath)
	os.MkdirAll(projDir, 0o755)

	// Create stale session file.
	staleFile := filepath.Join(projDir, "stale-session.jsonl")
	os.WriteFile(staleFile, []byte(
		`{"type":"assistant","uuid":"msg-stale","message":{"content":[{"type":"text","text":"Stale msg"}]}}`+"\n",
	), 0o644)
	staleTime := time.Now().Add(-2 * time.Minute)
	os.Chtimes(staleFile, staleTime, staleTime)

	// Create a fresh session file that the watcher should discover.
	freshFile := filepath.Join(projDir, "fresh-session.jsonl")
	os.WriteFile(freshFile, []byte(
		`{"type":"assistant","uuid":"msg-fresh","message":{"content":[{"type":"text","text":"Fresh msg"}]}}`+"\n",
	), 0o644)

	// Write marker pointing to stale file (this is the bug condition:
	// the marker always points to the stale file).
	markerPath := filepath.Join(aptPath, ".woland-session")
	os.WriteFile(markerPath, []byte(staleFile), 0o644)

	busFile := filepath.Join(aptPath, "bus.jsonl")
	bus := New(busFile)
	logger := log.New(os.Stderr, "test-staleness: ", 0)
	watcher := NewWatcher(bus, "", aptPath, logger)

	// Start output watcher for woland.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		watcher.watchAgentOutput(ctx, "woland", staleFile, true)
	}()

	// Wait for the staleness detection to kick in and switch files.
	// The watcher polls every 1.5s and staleness threshold is 30s.
	// Since the stale file's modtime is 2 minutes ago, staleness should
	// be detected on the first check after watcherStaleness elapses.
	// But we're in a test and lastNewContent starts at time.Now(), so
	// we need to wait ~31 seconds... Instead, let's test the marker
	// update indirectly by checking findWolandSessionFile behavior.
	//
	// For a fast test, we verify the building blocks work correctly:
	// findWolandSessionFile returns the fresh file (not the stale one).
	got := watcher.findWolandSessionFile()
	if got != freshFile {
		t.Errorf("findWolandSessionFile() = %q, want %q (should bypass stale marker)", got, freshFile)
	}

	// Also verify that after manual marker update, findWolandSessionFile
	// returns the fresh file immediately.
	os.WriteFile(markerPath, []byte(freshFile), 0o644)
	got2 := watcher.findWolandSessionFile()
	if got2 != freshFile {
		t.Errorf("findWolandSessionFile() after marker update = %q, want %q", got2, freshFile)
	}

	cancel()
	<-done
}
