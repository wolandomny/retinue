package bus

import (
	"testing"
)

// ---------------------------------------------------------------------------
// firstLineOf
// ---------------------------------------------------------------------------

func TestFirstLineOf_SingleLine(t *testing.T) {
	got := firstLineOf("hello world")
	if got != "hello world" {
		t.Errorf("firstLineOf single line = %q, want %q", got, "hello world")
	}
}

func TestFirstLineOf_MultiLine(t *testing.T) {
	got := firstLineOf("first\nsecond\nthird")
	if got != "first" {
		t.Errorf("firstLineOf multi-line = %q, want %q", got, "first")
	}
}

func TestFirstLineOf_Empty(t *testing.T) {
	got := firstLineOf("")
	if got != "" {
		t.Errorf("firstLineOf empty = %q, want %q", got, "")
	}
}

func TestFirstLineOf_TrailingNewline(t *testing.T) {
	got := firstLineOf("hello\n")
	if got != "hello" {
		t.Errorf("firstLineOf trailing newline = %q, want %q", got, "hello")
	}
}

// ---------------------------------------------------------------------------
// parseArrowRouting
// ---------------------------------------------------------------------------

func TestParseArrowRouting_SingleRecipient(t *testing.T) {
	recipients, text, ok := parseArrowRouting("→ azazello: Check the CI logs")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if len(recipients) != 1 || recipients[0] != "azazello" {
		t.Errorf("recipients = %v, want [azazello]", recipients)
	}
	if text != "Check the CI logs" {
		t.Errorf("text = %q, want %q", text, "Check the CI logs")
	}
}

func TestParseArrowRouting_MultipleRecipients(t *testing.T) {
	recipients, text, ok := parseArrowRouting("→ azazello, behemoth: Coordinate on this")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if len(recipients) != 2 || recipients[0] != "azazello" || recipients[1] != "behemoth" {
		t.Errorf("recipients = %v, want [azazello behemoth]", recipients)
	}
	if text != "Coordinate on this" {
		t.Errorf("text = %q, want %q", text, "Coordinate on this")
	}
}

func TestParseArrowRouting_MultiLineMessage(t *testing.T) {
	input := "→ azazello: First line\nSecond line\nThird line"
	recipients, text, ok := parseArrowRouting(input)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if len(recipients) != 1 || recipients[0] != "azazello" {
		t.Errorf("recipients = %v, want [azazello]", recipients)
	}
	want := "First line\nSecond line\nThird line"
	if text != want {
		t.Errorf("text = %q, want %q", text, want)
	}
}

func TestParseArrowRouting_NoArrow(t *testing.T) {
	recipients, text, ok := parseArrowRouting("Regular message without arrow")
	if ok {
		t.Errorf("expected ok=false, got recipients=%v, text=%q", recipients, text)
	}
	if recipients != nil {
		t.Errorf("expected nil recipients, got %v", recipients)
	}
	if text != "" {
		t.Errorf("expected empty text, got %q", text)
	}
}

func TestParseArrowRouting_NoColon(t *testing.T) {
	recipients, text, ok := parseArrowRouting("→ azazello")
	if ok {
		t.Errorf("expected ok=false for no colon, got recipients=%v, text=%q", recipients, text)
	}
}

func TestParseArrowRouting_EmptyAgentName(t *testing.T) {
	recipients, text, ok := parseArrowRouting("→ : no agent name")
	if ok {
		t.Errorf("expected ok=false for empty agent, got recipients=%v, text=%q", recipients, text)
	}
}

func TestParseArrowRouting_WhitespaceAroundNames(t *testing.T) {
	recipients, text, ok := parseArrowRouting("→  azazello ,  behemoth : message")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if len(recipients) != 2 || recipients[0] != "azazello" || recipients[1] != "behemoth" {
		t.Errorf("recipients = %v, want [azazello behemoth]", recipients)
	}
	if text != "message" {
		t.Errorf("text = %q, want %q", text, "message")
	}
}

func TestParseArrowRouting_RecipientsLowercased(t *testing.T) {
	recipients, _, ok := parseArrowRouting("→ Azazello, BEHEMOTH: msg")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if recipients[0] != "azazello" || recipients[1] != "behemoth" {
		t.Errorf("recipients = %v, want lowercased", recipients)
	}
}

func TestParseArrowRouting_EmptyTextAfterColon(t *testing.T) {
	recipients, text, ok := parseArrowRouting("→ azazello:")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if len(recipients) != 1 || recipients[0] != "azazello" {
		t.Errorf("recipients = %v, want [azazello]", recipients)
	}
	if text != "" {
		t.Errorf("text = %q, want empty", text)
	}
}

func TestParseArrowRouting_TrailingSpacesInName(t *testing.T) {
	recipients, text, ok := parseArrowRouting("→ azazello : some text")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if len(recipients) != 1 || recipients[0] != "azazello" {
		t.Errorf("recipients = %v, want [azazello]", recipients)
	}
	if text != "some text" {
		t.Errorf("text = %q, want %q", text, "some text")
	}
}

// ---------------------------------------------------------------------------
// routeMessage
// ---------------------------------------------------------------------------

func makeWindows() []injectionWindow {
	return []injectionWindow{
		{busName: "woland", windowName: "woland", isWoland: true},
		{busName: "woland", windowName: "babytalk", isWoland: true},
		{busName: "azazello", windowName: "agent-azazello", isWoland: false},
		{busName: "behemoth", windowName: "agent-behemoth", isWoland: false},
	}
}

func TestRouteMessage_SystemMessages(t *testing.T) {
	windows := makeWindows()
	msg := Message{Name: "system", Type: TypeSystem, Text: "agent joined"}
	targets := routeMessage(windows, msg)
	if len(targets) != 0 {
		t.Errorf("system messages should return nil, got %d targets", len(targets))
	}
}

func TestRouteMessage_SenderEchoPrevention(t *testing.T) {
	windows := makeWindows()
	msg := Message{Name: "azazello", Type: TypeChat, Text: "hello", To: []string{"behemoth"}}
	targets := routeMessage(windows, msg)
	for _, w := range targets {
		if w.busName == "azazello" {
			t.Error("sender should not receive own message")
		}
	}
}

func TestRouteMessage_WolandAlwaysReceives(t *testing.T) {
	windows := makeWindows()
	msg := Message{Name: "azazello", Type: TypeChat, Text: "hello"}
	targets := routeMessage(windows, msg)
	wolandCount := 0
	for _, w := range targets {
		if w.isWoland {
			wolandCount++
		}
	}
	if wolandCount != 2 {
		t.Errorf("Woland windows should receive all non-system messages, got %d", wolandCount)
	}
}

func TestRouteMessage_WolandAlwaysReceivesEvenWithEmptyTo(t *testing.T) {
	windows := makeWindows()
	msg := Message{Name: "azazello", Type: TypeChat, Text: "thinking aloud"}
	targets := routeMessage(windows, msg)
	wolandFound := false
	for _, w := range targets {
		if w.isWoland {
			wolandFound = true
			break
		}
	}
	if !wolandFound {
		t.Error("Woland should receive message even with nil To")
	}
}

func TestRouteMessage_WolandExcludedWhenSender(t *testing.T) {
	windows := makeWindows()
	msg := Message{Name: "woland", Type: TypeChat, Text: "directing", To: []string{"azazello"}}
	targets := routeMessage(windows, msg)
	for _, w := range targets {
		if w.busName == "woland" {
			t.Error("Woland should not receive own message (echo prevention)")
		}
	}
}

func TestRouteMessage_AgentReceivesWhenInTo(t *testing.T) {
	windows := makeWindows()
	msg := Message{Name: "woland", Type: TypeChat, Text: "check logs", To: []string{"azazello"}}
	targets := routeMessage(windows, msg)
	found := false
	for _, w := range targets {
		if w.busName == "azazello" {
			found = true
			break
		}
	}
	if !found {
		t.Error("azazello should receive message when in To")
	}
}

func TestRouteMessage_AgentNotReceivedWhenNotInTo(t *testing.T) {
	windows := makeWindows()
	msg := Message{Name: "woland", Type: TypeChat, Text: "check logs", To: []string{"azazello"}}
	targets := routeMessage(windows, msg)
	for _, w := range targets {
		if w.busName == "behemoth" {
			t.Error("behemoth should NOT receive message when not in To")
		}
	}
}

func TestRouteMessage_AgentNotReceivedWhenToEmpty(t *testing.T) {
	windows := makeWindows()
	msg := Message{Name: "woland", Type: TypeChat, Text: "thinking aloud"}
	targets := routeMessage(windows, msg)
	for _, w := range targets {
		if !w.isWoland && w.busName != "woland" {
			t.Errorf("agent %q should not receive message with nil To", w.busName)
		}
	}
}

func TestRouteMessage_MultipleAgentsInTo(t *testing.T) {
	windows := makeWindows()
	msg := Message{Name: "woland", Type: TypeChat, Text: "coordinate", To: []string{"azazello", "behemoth"}}
	targets := routeMessage(windows, msg)
	found := make(map[string]bool)
	for _, w := range targets {
		found[w.busName] = true
	}
	if !found["azazello"] {
		t.Error("azazello should receive message when in To")
	}
	if !found["behemoth"] {
		t.Error("behemoth should receive message when in To")
	}
}

func TestRouteMessage_CaseInsensiveToMatching(t *testing.T) {
	windows := makeWindows()
	msg := Message{Name: "woland", Type: TypeChat, Text: "check it", To: []string{"Azazello"}}
	targets := routeMessage(windows, msg)
	found := false
	for _, w := range targets {
		if w.busName == "azazello" {
			found = true
			break
		}
	}
	if !found {
		t.Error("azazello should match To:[\"Azazello\"] (case-insensitive)")
	}
}

func TestRouteMessage_UserToWoland(t *testing.T) {
	windows := makeWindows()
	msg := Message{Name: "user", Type: TypeUser, Text: "hey woland", To: []string{"woland"}}
	targets := routeMessage(windows, msg)
	// Only Woland windows should receive (user is not a window, agents not in To).
	for _, w := range targets {
		if !w.isWoland {
			t.Errorf("non-Woland window %q should not receive user→woland message", w.busName)
		}
	}
	wolandCount := 0
	for _, w := range targets {
		if w.isWoland {
			wolandCount++
		}
	}
	if wolandCount != 2 {
		t.Errorf("expected 2 Woland windows, got %d", wolandCount)
	}
}

func TestRouteMessage_BabytalkFollowsWolandRules(t *testing.T) {
	windows := []injectionWindow{
		{busName: "woland", windowName: "babytalk", isWoland: true},
		{busName: "azazello", windowName: "agent-azazello", isWoland: false},
	}
	msg := Message{Name: "azazello", Type: TypeChat, Text: "status update"}
	targets := routeMessage(windows, msg)
	found := false
	for _, w := range targets {
		if w.windowName == "babytalk" && w.isWoland {
			found = true
			break
		}
	}
	if !found {
		t.Error("babytalk (isWoland=true) should receive messages like Woland")
	}
}
