package bus

import (
	"strings"
	"testing"
	"time"
)

func TestFormatForTelegram_ChatMessage(t *testing.T) {
	msg := Message{
		ID:        "abc123",
		Name:      "azazello",
		Timestamp: time.Date(2025, 1, 1, 12, 30, 0, 0, time.UTC),
		Type:      TypeChat,
		Text:      "CI is failing on test_auth_flow.",
	}
	got := FormatForTelegram(msg)
	want := "**Azazello**: CI is failing on test_auth_flow."
	if got != want {
		t.Errorf("FormatForTelegram(chat) = %q, want %q", got, want)
	}
}

func TestFormatForTelegram_ActionMessage(t *testing.T) {
	msg := Message{
		ID:        "def456",
		Name:      "azazello",
		Timestamp: time.Date(2025, 1, 1, 12, 30, 0, 0, time.UTC),
		Type:      TypeAction,
		Text:      "I'm going to fix the auth test.",
	}
	got := FormatForTelegram(msg)
	want := "**Azazello** [action]: I'm going to fix the auth test."
	if got != want {
		t.Errorf("FormatForTelegram(action) = %q, want %q", got, want)
	}
}

func TestFormatForTelegram_ResultMessage(t *testing.T) {
	msg := Message{
		ID:        "ghi789",
		Name:      "azazello",
		Timestamp: time.Date(2025, 1, 1, 12, 30, 0, 0, time.UTC),
		Type:      TypeResult,
		Text:      "Fixed — PR #42 opened.",
	}
	got := FormatForTelegram(msg)
	want := "**Azazello** [result]: Fixed — PR #42 opened."
	if got != want {
		t.Errorf("FormatForTelegram(result) = %q, want %q", got, want)
	}
}

func TestFormatForTelegram_SystemMessage(t *testing.T) {
	msg := Message{
		ID:        "jkl012",
		Name:      "system",
		Timestamp: time.Date(2025, 1, 1, 12, 30, 0, 0, time.UTC),
		Type:      TypeSystem,
		Text:      "Behemoth has joined",
	}
	got := FormatForTelegram(msg)
	want := "_Behemoth has joined_"
	if got != want {
		t.Errorf("FormatForTelegram(system) = %q, want %q", got, want)
	}
}

func TestFormatForTelegram_UserMessageNotEchoed(t *testing.T) {
	// The adapter skips user messages, but FormatForTelegram should still
	// format them if called directly (the filtering happens in Run()).
	msg := Message{
		ID:        "mno345",
		Name:      "user",
		Timestamp: time.Date(2025, 1, 1, 12, 30, 0, 0, time.UTC),
		Type:      TypeUser,
		Text:      "what's the status?",
	}
	// FormatForTelegram returns a formatted string for any message type;
	// the skip logic is in the Run() loop, not the formatter.
	got := FormatForTelegram(msg)
	if got == "" {
		t.Error("FormatForTelegram(user) returned empty string")
	}
}

func TestIsTelegramKillWord(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"back", true},
		{"Back", true},
		{"BACK", true},
		{"/desk", true},
		{"/Desk", true},
		{"at my desk", true},
		{"At My Desk", true},
		{"i'm back", true},
		{"I'm back", true},
		{"im back", true},
		{"Im Back", true},
		{"  back  ", true},  // trimmed
		{"hello", false},
		{"go back", false},  // not exact match
		{"backing", false},  // not exact match
		{"", false},
		{"desk", false},     // missing slash
		{"at my desk!", false}, // trailing punctuation
	}

	for _, tt := range tests {
		got := IsTelegramKillWord(tt.input)
		if got != tt.want {
			t.Errorf("IsTelegramKillWord(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestFormatForTelegram_CapitalizesAgentName(t *testing.T) {
	msg := Message{
		ID:        "cap123",
		Name:      "behemoth",
		Timestamp: time.Date(2025, 1, 1, 12, 30, 0, 0, time.UTC),
		Type:      TypeChat,
		Text:      "Done.",
	}
	got := FormatForTelegram(msg)
	want := "**Behemoth**: Done."
	if got != want {
		t.Errorf("FormatForTelegram with lowercase agent = %q, want %q", got, want)
	}
}

func TestFormatForTelegram_SystemMessageFromField(t *testing.T) {
	// System messages use italic format regardless of the From field.
	msg := Message{
		ID:        "sys123",
		Name:      "system",
		Timestamp: time.Date(2025, 1, 1, 12, 30, 0, 0, time.UTC),
		Type:      TypeSystem,
		Text:      "Koroviev has left",
	}
	got := FormatForTelegram(msg)
	want := "_Koroviev has left_"
	if got != want {
		t.Errorf("FormatForTelegram(system leave) = %q, want %q", got, want)
	}
}

// ShouldSkipForTelegram extracts the filtering logic from the Run() loop:
// user messages should not be echoed back to Telegram.
func ShouldSkipForTelegram(msg *Message) bool {
	return msg.Type == TypeUser
}

func TestShouldSkipForTelegram_UserMessages(t *testing.T) {
	tests := []struct {
		name string
		msg  *Message
		want bool
	}{
		{
			name: "user message is skipped",
			msg:  &Message{Type: TypeUser, Name: "user", Text: "hello"},
			want: true,
		},
		{
			name: "chat message is not skipped",
			msg:  &Message{Type: TypeChat, Name: "azazello", Text: "hello"},
			want: false,
		},
		{
			name: "action message is not skipped",
			msg:  &Message{Type: TypeAction, Name: "azazello", Text: "fixing CI"},
			want: false,
		},
		{
			name: "result message is not skipped",
			msg:  &Message{Type: TypeResult, Name: "azazello", Text: "done"},
			want: false,
		},
		{
			name: "system message is not skipped",
			msg:  &Message{Type: TypeSystem, Name: "system", Text: "agent joined"},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ShouldSkipForTelegram(tt.msg)
			if got != tt.want {
				t.Errorf("ShouldSkipForTelegram(%q) = %v, want %v", tt.msg.Type, got, tt.want)
			}
		})
	}
}

func TestFormatForTelegram_EmptyText(t *testing.T) {
	msg := Message{
		ID:        "empty1",
		Name:      "azazello",
		Timestamp: time.Date(2025, 1, 1, 12, 30, 0, 0, time.UTC),
		Type:      TypeChat,
		Text:      "",
	}
	got := FormatForTelegram(msg)
	// Should still produce a valid format with empty text.
	if !strings.HasPrefix(got, "**Azazello**:") {
		t.Errorf("FormatForTelegram(empty text) = %q, expected prefix '**Azazello**:'", got)
	}
}

func TestFormatForTelegram_SpecialCharacters(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{"markdown bold", "This has **bold** text"},
		{"markdown italic", "This has _italic_ text"},
		{"markdown code", "This has `code` text"},
		{"angle brackets", "Check <this> out"},
		{"ampersand", "A & B"},
		{"backticks", "Run ```go test```"},
		{"newlines", "Line 1\nLine 2\nLine 3"},
		{"unicode", "This has emoji \U0001F680 and accents \u00E9\u00E8\u00EA"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := Message{
				ID:        "special-" + tt.name,
				Name:      "azazello",
				Timestamp: time.Date(2025, 1, 1, 12, 30, 0, 0, time.UTC),
				Type:      TypeChat,
				Text:      tt.text,
			}
			got := FormatForTelegram(msg)
			// Should contain the original text (not escape or mangle it).
			if !strings.Contains(got, tt.text) {
				t.Errorf("FormatForTelegram with %s: result %q does not contain original text %q", tt.name, got, tt.text)
			}
			// Should have the agent name prefix.
			if !strings.HasPrefix(got, "**Azazello**:") {
				t.Errorf("FormatForTelegram with %s: result %q missing name prefix", tt.name, got)
			}
		})
	}
}

func TestFormatForTelegram_EmptyName(t *testing.T) {
	msg := Message{
		ID:        "noname1",
		Name:      "",
		Timestamp: time.Date(2025, 1, 1, 12, 30, 0, 0, time.UTC),
		Type:      TypeChat,
		Text:      "anonymous message",
	}
	got := FormatForTelegram(msg)
	// capitalize("") returns "", so we get "****:"
	if !strings.Contains(got, "anonymous message") {
		t.Errorf("FormatForTelegram with empty name should still contain text, got: %q", got)
	}
}

func TestFormatForTelegram_SystemWithSpecialChars(t *testing.T) {
	msg := Message{
		ID:        "sys-special",
		Name:      "system",
		Timestamp: time.Date(2025, 1, 1, 12, 30, 0, 0, time.UTC),
		Type:      TypeSystem,
		Text:      "Agent _azazello_ has joined",
	}
	got := FormatForTelegram(msg)
	want := "_Agent _azazello_ has joined_"
	if got != want {
		t.Errorf("FormatForTelegram(system with underscores) = %q, want %q", got, want)
	}
}
