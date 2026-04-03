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
			name:     "non-assistant message",
			line:     `{"type":"human","uuid":"def-456","message":{"content":[{"type":"text","text":"User input"}]}}`,
			wantText: "",
			wantUUID: "",
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
	offset, partial := w.readAgentLines(ctx, "test-agent", sessionPath, 0, "", seen, false)

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

	w.readAgentLines(ctx, "test-agent", sessionPath, 0, "", seen, false)

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
	w.readAgentLines(ctx, "test-agent", sessionPath, 0, "", seen, true)

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

	offset, partial := w.readAgentLines(ctx, "test-agent", sessionPath, 0, "", seen, false)

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
	_, partial2 := w.readAgentLines(ctx, "test-agent", sessionPath, offset, partial, seen, false)

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

	offset, _ := w.readAgentLines(ctx, "test-agent", sessionPath, 0, "", seen, false)

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
	_, _ = w.readAgentLines(ctx, "test-agent", sessionPath, offset, "", seen, false)

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

	offset, partial := w.readAgentLines(ctx, "test-agent", sessionPath, 0, "", seen, false)

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

	offset, partial := w.readAgentLines(ctx, "test-agent", sessionPath, 0, "", seen, false)

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
			name: "chat from agent excludes sender",
			msg: Message{
				Name: "azazello",
				Type: TypeChat,
				Text: "hello",
			},
			want: []injectionWindow{
				{busName: "behemoth", windowName: "agent-behemoth"},
				{busName: "koroviev", windowName: "agent-koroviev"},
			},
			wantLen: 2,
		},
		{
			name: "chat from non-agent includes all",
			msg: Message{
				Name: "user",
				Type: TypeUser,
				Text: "status?",
			},
			want: []injectionWindow{
				{busName: "azazello", windowName: "agent-azazello"},
				{busName: "behemoth", windowName: "agent-behemoth"},
				{busName: "koroviev", windowName: "agent-koroviev"},
			},
			wantLen: 3,
		},
		{
			name: "system message returns nil",
			msg: Message{
				Name: "system",
				Type: TypeSystem,
				Text: "agent joined",
			},
			want:    nil,
			wantLen: 0,
		},
		{
			name: "action from agent excludes sender",
			msg: Message{
				Name: "behemoth",
				Type: TypeAction,
				Text: "fixing CI",
			},
			want: []injectionWindow{
				{busName: "azazello", windowName: "agent-azazello"},
				{busName: "koroviev", windowName: "agent-koroviev"},
			},
			wantLen: 2,
		},
		{
			name: "result from agent excludes sender",
			msg: Message{
				Name: "koroviev",
				Type: TypeResult,
				Text: "PR opened",
			},
			want: []injectionWindow{
				{busName: "azazello", windowName: "agent-azazello"},
				{busName: "behemoth", windowName: "agent-behemoth"},
			},
			wantLen: 2,
		},
		{
			name: "message from unknown sender includes all",
			msg: Message{
				Name: "stranger",
				Type: TypeChat,
				Text: "who am I?",
			},
			want: []injectionWindow{
				{busName: "azazello", windowName: "agent-azazello"},
				{busName: "behemoth", windowName: "agent-behemoth"},
				{busName: "koroviev", windowName: "agent-koroviev"},
			},
			wantLen: 3,
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
	windows := []injectionWindow{
		{busName: "azazello", windowName: "agent-azazello"},
	}
	msg := Message{Name: "user", Type: TypeUser, Text: "hello"}
	got := injectionTargets(windows, msg)
	if len(got) != 1 || got[0].busName != "azazello" {
		t.Errorf("expected [{azazello agent-azazello}], got %v", got)
	}
}

func TestInjectionTargets_WolandExcludedWhenSender(t *testing.T) {
	windows := []injectionWindow{
		{busName: "azazello", windowName: "agent-azazello"},
		{busName: "woland", windowName: "woland"},
		{busName: "behemoth", windowName: "agent-behemoth"},
	}
	msg := Message{Name: "woland", Type: TypeChat, Text: "I see everything"}
	got := injectionTargets(windows, msg)
	if len(got) != 2 {
		t.Fatalf("expected 2 targets (woland excluded), got %d: %v", len(got), got)
	}
	for _, w := range got {
		if w.busName == "woland" {
			t.Error("woland should be excluded when it is the sender")
		}
	}
}

func TestInjectionTargets_WolandIncludedWhenNotSender(t *testing.T) {
	windows := []injectionWindow{
		{busName: "azazello", windowName: "agent-azazello"},
		{busName: "woland", windowName: "woland"},
	}
	msg := Message{Name: "azazello", Type: TypeChat, Text: "reporting in"}
	got := injectionTargets(windows, msg)
	if len(got) != 1 || got[0].busName != "woland" {
		t.Errorf("expected woland as sole target, got %v", got)
	}
}

func TestInjectionTargets_BabytalkExcludedWhenWolandSends(t *testing.T) {
	// "babytalk" window has busName "woland", so it should be excluded
	// when the sender is "woland".
	windows := []injectionWindow{
		{busName: "azazello", windowName: "agent-azazello"},
		{busName: "woland", windowName: "babytalk"},
	}
	msg := Message{Name: "woland", Type: TypeChat, Text: "I see everything"}
	got := injectionTargets(windows, msg)
	if len(got) != 1 || got[0].busName != "azazello" {
		t.Errorf("expected only azazello, got %v", got)
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
	offset, partial := w.readAgentLines(ctx, "test-agent", sessionPath, 0, "", seen, false)

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
	offset, partial = w.readAgentLines(ctx, "test-agent", sessionPath, offset, partial, seen, false)
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

	w.readAgentLines(ctx, "test-agent", sessionPath, 0, "", seen, false)

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

	w.readAgentLines(ctx, "test-agent", sessionPath, 0, "", seen, false)

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
	w.readAgentLines(ctx, "woland", sessionPath, 0, "", seen, false)

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
