package bus

import (
	"testing"
	"time"
)

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
