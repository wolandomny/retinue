package bus

import (
	"encoding/json"
	"testing"
)

func TestNewMessageGeneratesUniqueIDs(t *testing.T) {
	m1 := NewMessage("agent-a", TypeChat, "hello")
	m2 := NewMessage("agent-b", TypeChat, "world")

	if m1.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if m2.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if m1.ID == m2.ID {
		t.Fatalf("expected unique IDs, got %q twice", m1.ID)
	}

	// ID should be 32 hex chars (16 bytes).
	if len(m1.ID) != 32 {
		t.Fatalf("expected 32-char hex ID, got %d chars: %q", len(m1.ID), m1.ID)
	}
}

func TestNewMessageFields(t *testing.T) {
	m := NewMessage("azazello", TypeAction, "deploying hotfix")

	if m.Name != "azazello" {
		t.Fatalf("expected Name=%q, got %q", "azazello", m.Name)
	}
	if m.Type != TypeAction {
		t.Fatalf("expected Type=%q, got %q", TypeAction, m.Type)
	}
	if m.Text != "deploying hotfix" {
		t.Fatalf("expected Text=%q, got %q", "deploying hotfix", m.Text)
	}
	if m.Timestamp.IsZero() {
		t.Fatal("expected non-zero timestamp")
	}
}

func TestJSONRoundTrip(t *testing.T) {
	original := NewMessage("user", TypeUser, "what's the status?")
	original.Meta = map[string]interface{}{"channel": "telegram"}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded Message
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.ID != original.ID {
		t.Fatalf("ID mismatch: %q vs %q", decoded.ID, original.ID)
	}
	if decoded.Name != original.Name {
		t.Fatalf("Name mismatch: %q vs %q", decoded.Name, original.Name)
	}
	if decoded.Type != original.Type {
		t.Fatalf("Type mismatch: %q vs %q", decoded.Type, original.Type)
	}
	if decoded.Text != original.Text {
		t.Fatalf("Text mismatch: %q vs %q", decoded.Text, original.Text)
	}
	if !decoded.Timestamp.Equal(original.Timestamp) {
		t.Fatalf("Timestamp mismatch: %v vs %v", decoded.Timestamp, original.Timestamp)
	}
	if decoded.Meta["channel"] != "telegram" {
		t.Fatalf("Meta mismatch: got %v", decoded.Meta)
	}
}

func TestMessageTypeStringValues(t *testing.T) {
	tests := []struct {
		mt   MessageType
		want string
	}{
		{TypeChat, "chat"},
		{TypeAction, "action"},
		{TypeResult, "result"},
		{TypeUser, "user"},
		{TypeSystem, "system"},
	}

	for _, tt := range tests {
		if string(tt.mt) != tt.want {
			t.Errorf("expected %q, got %q", tt.want, string(tt.mt))
		}
	}
}

func TestMetaOmittedWhenNil(t *testing.T) {
	m := NewMessage("agent", TypeChat, "hello")
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Meta should be omitted from JSON when nil.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	if _, ok := raw["meta"]; ok {
		t.Fatal("expected meta to be omitted from JSON when nil")
	}
}

func TestGenerateID(t *testing.T) {
	id1 := generateID()
	id2 := generateID()

	if id1 == id2 {
		t.Error("generateID should produce unique IDs")
	}

	if len(id1) != 32 {
		t.Errorf("Expected ID length 32, got %d", len(id1))
	}

	// Should be hex
	for _, r := range id1 {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			t.Errorf("ID should be hex, got character %q in %q", r, id1)
		}
	}
}
