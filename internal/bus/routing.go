package bus

import "strings"

// firstLineOf returns the text before the first newline, or the full
// text if there are no newlines.
func firstLineOf(text string) string {
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		return text[:i]
	}
	return text
}

// parseArrowRouting extracts explicit routing from Woland's output.
// Format: "→ agent1, agent2: message text"
// Returns (recipients, stripped text, ok).
// Recipients are lowercased. If the format doesn't match, returns (nil, "", false).
func parseArrowRouting(text string) ([]string, string, bool) {
	// Strip leading blank lines — Claude often starts with whitespace.
	trimmed := strings.TrimLeft(text, " \t\n\r")
	first := firstLineOf(trimmed)

	if !strings.HasPrefix(first, "→ ") {
		return nil, "", false
	}

	// Find the colon separator on the first line.
	colonIdx := strings.IndexByte(first, ':')
	if colonIdx < 0 {
		return nil, "", false
	}

	// Extract agent names between "→ " and ":".
	namesPart := first[len("→ "):colonIdx]

	// Parse comma-separated names.
	var recipients []string
	for _, name := range strings.Split(namesPart, ",") {
		name = strings.TrimSpace(name)
		if name != "" {
			recipients = append(recipients, strings.ToLower(name))
		}
	}
	if len(recipients) == 0 {
		return nil, "", false
	}

	// Build the message text: trimmed text after colon on first line,
	// plus any subsequent lines preserved exactly.
	firstLineMsg := strings.TrimSpace(first[colonIdx+1:])

	var msgText string
	if i := strings.IndexByte(trimmed, '\n'); i >= 0 {
		// There are subsequent lines — append them.
		msgText = firstLineMsg + trimmed[i:]
	} else {
		msgText = firstLineMsg
	}

	return recipients, msgText, true
}

// routeMessage returns the subset of windows that should receive msg.
// It implements explicit To-based routing:
//   - System messages are never routed.
//   - The sender never receives its own message.
//   - Woland (isWoland=true) always receives all non-system messages.
//   - All other windows only receive messages where their busName
//     appears in msg.To (case-insensitive).
func routeMessage(windows []injectionWindow, msg Message) []injectionWindow {
	if msg.Type == TypeSystem {
		return nil
	}

	var targets []injectionWindow
	for _, w := range windows {
		if w.busName == msg.Name {
			continue
		}
		if w.isWoland {
			targets = append(targets, w)
			continue
		}
		for _, to := range msg.To {
			if strings.EqualFold(w.busName, to) {
				targets = append(targets, w)
				break
			}
		}
	}
	return targets
}
