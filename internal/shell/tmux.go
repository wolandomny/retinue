package shell

import "strings"

// EscapeTmux escapes a message string for use with tmux send-keys.
// It handles special characters that tmux might interpret.
func EscapeTmux(s string) string {
	// Replace newlines with spaces — send-keys treats Enter as a key literal,
	// so we collapse multi-line input into a single line.
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", "")

	// Escape backslashes first (before adding more).
	s = strings.ReplaceAll(s, `\`, `\\`)

	// Escape semicolons — tmux uses ; as a command separator.
	s = strings.ReplaceAll(s, ";", `\;`)

	// Escape dollar signs to prevent shell variable expansion.
	s = strings.ReplaceAll(s, "$", `\$`)

	// Escape backticks.
	s = strings.ReplaceAll(s, "`", "\\`")

	return s
}
