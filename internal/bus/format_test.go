package bus

import (
	"strings"
	"testing"
	"time"
)

func fixedTime() time.Time {
	return time.Date(2026, 3, 15, 14, 30, 45, 0, time.Local)
}

func TestFormatMessageChat(t *testing.T) {
	msg := Message{
		ID:        "abc123",
		From:      "azazello",
		Timestamp: fixedTime(),
		Type:      TypeChat,
		Text:      "CI is green",
	}

	got := FormatMessage(msg)
	expected := "[14:30:45] Azazello: CI is green"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestFormatMessageUser(t *testing.T) {
	msg := Message{
		ID:        "def456",
		From:      "user",
		Timestamp: fixedTime(),
		Type:      TypeUser,
		Text:      "what's the status?",
	}

	got := FormatMessage(msg)
	expected := "[14:30:45] You: what's the status?"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestFormatMessageSystem(t *testing.T) {
	msg := Message{
		ID:        "ghi789",
		From:      "system",
		Timestamp: fixedTime(),
		Type:      TypeSystem,
		Text:      "Azazello has joined",
	}

	got := FormatMessage(msg)
	expected := "[14:30:45] --- Azazello has joined ---"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestFormatMessageAction(t *testing.T) {
	msg := Message{
		ID:        "jkl012",
		From:      "koroviev",
		Timestamp: fixedTime(),
		Type:      TypeAction,
		Text:      "deploying hotfix",
	}

	got := FormatMessage(msg)
	expected := "[14:30:45] Koroviev: deploying hotfix"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestFormatMessageResult(t *testing.T) {
	msg := Message{
		ID:        "mno345",
		From:      "behemoth",
		Timestamp: fixedTime(),
		Type:      TypeResult,
		Text:      "deploy succeeded",
	}

	got := FormatMessage(msg)
	expected := "[14:30:45] Behemoth: deploy succeeded"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestFormatForInjectionAgent(t *testing.T) {
	msg := Message{
		ID:   "abc123",
		From: "azazello",
		Type: TypeChat,
		Text: "CI is failing on test_auth_flow.",
	}

	got := FormatForInjection(msg)
	expected := "[Azazello] CI is failing on test_auth_flow."
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestFormatForInjectionUser(t *testing.T) {
	msg := Message{
		ID:   "def456",
		From: "user",
		Type: TypeUser,
		Text: "what's the status?",
	}

	got := FormatForInjection(msg)
	expected := "[User] what's the status?"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestFormatForInjectionSystem(t *testing.T) {
	msg := Message{
		ID:   "ghi789",
		From: "system",
		Type: TypeSystem,
		Text: "Azazello has joined",
	}

	got := FormatForInjection(msg)
	expected := "[System] Azazello has joined"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestFormatMessageContainsTimestamp(t *testing.T) {
	msg := NewMessage("agent", TypeChat, "test")
	got := FormatMessage(msg)

	// Should contain a timestamp in HH:MM:SS format.
	if !strings.Contains(got, ":") {
		t.Fatalf("expected timestamp in output, got %q", got)
	}
}
