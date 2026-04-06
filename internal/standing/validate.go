package standing

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// kebabCaseRe matches valid kebab-case identifiers: lowercase
// alphanumeric characters separated by single hyphens.
var kebabCaseRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Validate checks a slice of agents for common configuration errors:
//   - No duplicate IDs
//   - All IDs are non-empty
//   - All prompts are non-empty
//   - IDs are valid kebab-case (lowercase, hyphens, alphanumeric)
func Validate(agents []Agent) error {
	seen := make(map[string]bool, len(agents))

	for i, a := range agents {
		if a.ID == "" {
			return fmt.Errorf("agent[%d]: id must not be empty", i)
		}

		if !kebabCaseRe.MatchString(a.ID) {
			return fmt.Errorf("agent[%d]: id %q is not valid kebab-case (lowercase alphanumeric and hyphens)", i, a.ID)
		}

		if seen[a.ID] {
			return fmt.Errorf("agent[%d]: duplicate id %q", i, a.ID)
		}
		seen[a.ID] = true

		if a.Prompt == "" {
			return fmt.Errorf("agent[%d] (%s): prompt must not be empty", i, a.ID)
		}

		if err := validateSchedule(a.Schedule); err != nil {
			return fmt.Errorf("agent[%d] (%s): %w", i, a.ID, err)
		}
	}

	return nil
}

// validateSchedule validates a schedule field value.
// Valid values are:
// - Empty string (default: on_event)
// - "on_event" (explicit on_event)
// - "every <duration>" where duration is a valid Go duration (minimum 30s)
func validateSchedule(schedule string) error {
	// Empty string or "on_event" are valid
	if schedule == "" || schedule == "on_event" {
		return nil
	}

	// Check for "every <duration>" format
	if strings.HasPrefix(schedule, "every ") {
		durationStr := strings.TrimPrefix(schedule, "every ")
		duration, err := time.ParseDuration(durationStr)
		if err != nil {
			return fmt.Errorf("invalid schedule duration %q: %w", durationStr, err)
		}
		if duration < 30*time.Second {
			return fmt.Errorf("schedule duration %q is too short (minimum 30s)", durationStr)
		}
		return nil
	}

	return fmt.Errorf("invalid schedule %q: must be empty, \"on_event\", or \"every <duration>\" (e.g., \"every 5m\")", schedule)
}
