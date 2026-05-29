// Package effort defines the valid Claude Code "effort" levels and a
// shared validator. The Claude Code CLI accepts an --effort <level>
// flag controlling how often and how deeply the model thinks before
// responding (adaptive-reasoning depth). It is independent of model
// selection.
//
// Valid levels are: low, medium, high, xhigh, max, ultracode.
//
// Note: xhigh is supported on Opus 4.7 and Opus 4.8. Opus 4.6 and
// Sonnet 4.6 support only low/medium/high/max. The default effort is
// high on Opus 4.8. Retinue does not enforce the per-model restriction
// — it simply forwards whatever the user configures to the claude CLI.
//
// ultracode is special: it is a Claude Code session setting rather than
// an --effort value. It sends xhigh reasoning AND lets the agent
// auto-launch dynamic workflows. Retinue maps it to
// --settings '{"ultracode": true}' at the worker invocation, not to
// --effort.
package effort

import "fmt"

// Levels enumerates the valid effort levels accepted by the Claude
// Code --effort flag. The empty string is also valid and means
// "unset" (defer to the model's per-version default).
var Levels = []string{"low", "medium", "high", "xhigh", "max", "ultracode"}

// Validate returns nil if s is a valid effort level (or empty), and
// returns a descriptive error otherwise.
func Validate(s string) error {
	if s == "" {
		return nil
	}
	for _, l := range Levels {
		if s == l {
			return nil
		}
	}
	return fmt.Errorf("invalid effort %q: must be one of %v or empty", s, Levels)
}
