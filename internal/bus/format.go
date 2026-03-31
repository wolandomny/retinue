package bus

import (
	"fmt"
	"strings"
)

// FormatMessage formats a bus message for terminal display.
//
// Format: [HH:MM:SS] Name: text
//   - "You" for name="user", "System" for name="system", otherwise capitalize the agent ID
//   - System messages use a distinct format: [HH:MM:SS] --- text ---
func FormatMessage(msg *Message) string {
	ts := msg.Timestamp.Local().Format("15:04:05")

	if msg.Type == TypeSystem {
		return fmt.Sprintf("[%s] --- %s ---", ts, msg.Text)
	}

	name := displayName(msg.Name)
	return fmt.Sprintf("[%s] %s: %s", ts, name, msg.Text)
}

// FormatForInjection formats a bus message for injection into agent tmux
// sessions.
//
// Format: [Name] text — e.g. [Azazello] CI is failing on test_auth_flow.
//   - For user messages: [User] what's the status?
//   - System messages use a brief format: [System] text
func FormatForInjection(msg *Message) string {
	name := injectionName(msg.Name)
	return fmt.Sprintf("[%s] %s", name, msg.Text)
}

// displayName returns the human-readable name for terminal display.
func displayName(name string) string {
	switch name {
	case "user":
		return "You"
	case "system":
		return "System"
	default:
		return capitalize(name)
	}
}

// injectionName returns the name used in tmux injection format.
func injectionName(name string) string {
	switch name {
	case "user":
		return "User"
	case "system":
		return "System"
	default:
		return capitalize(name)
	}
}

// capitalize returns the string with its first letter uppercased.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
