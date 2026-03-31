package bus

import (
	"testing"
	"time"
)

func TestFormatForTelegram_ChatMessage(t *testing.T) {
	msg := Message{
		ID:        "abc123",
		From:      "azazello",
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
		From:      "azazello",
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
		From:      "azazello",
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
		From:      "system",
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
		From:      "user",
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
		From:      "behemoth",
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
		From:      "system",
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
