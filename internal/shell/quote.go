// Package shell provides utilities for constructing shell-safe
// command strings.
package shell

import "strings"

// Quote wraps s in single quotes, escaping embedded single quotes
// using the '\” idiom. The result is safe to embed in a POSIX shell
// command string.
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// Join quotes each argument and joins them with spaces, producing a
// shell-safe command fragment.
func Join(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = Quote(a)
	}
	return strings.Join(parts, " ")
}
