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
//
// The arrow directive may appear on any line of the text — Woland often
// produces explanatory prose before the routing line. We scan all lines
// and use the FIRST one that starts with "→ " (after trimming leading
// whitespace on that line). Text before the arrow line is discarded;
// everything after it (including subsequent arrow lines) becomes the
// routed message body.
func parseArrowRouting(text string) ([]string, string, bool) {
	lines := strings.Split(text, "\n")

	// Find the first line starting with "→ " (ignoring leading whitespace).
	arrowIdx := -1
	for i, line := range lines {
		trimmedLine := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmedLine, "→ ") {
			arrowIdx = i
			break
		}
	}
	if arrowIdx < 0 {
		return nil, "", false
	}

	arrowLine := strings.TrimLeft(lines[arrowIdx], " \t")

	// Find the colon separator on the arrow line.
	colonIdx := strings.IndexByte(arrowLine, ':')
	if colonIdx < 0 {
		return nil, "", false
	}

	// Extract agent names between "→ " and ":".
	namesPart := arrowLine[len("→ "):colonIdx]

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

	// Build the message text: trimmed text after colon on the arrow line,
	// plus any subsequent lines preserved exactly.
	arrowLineMsg := strings.TrimSpace(arrowLine[colonIdx+1:])

	remaining := lines[arrowIdx+1:]
	if len(remaining) > 0 {
		return recipients, arrowLineMsg + "\n" + strings.Join(remaining, "\n"), true
	}

	return recipients, arrowLineMsg, true
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
