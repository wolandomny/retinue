package standing

import (
	"fmt"
	"strings"
	"time"
)

// MinScheduleInterval is the minimum allowed interval for scheduled
// heartbeats. Any duration below this floor is clamped up to prevent
// excessive load.
const MinScheduleInterval = 30 * time.Second

// ParseSchedule parses the agent's schedule field and returns the
// heartbeat interval and whether scheduling is active. Supported formats:
//
//   - "" or "on_event" — no heartbeat; agent only responds to messages.
//   - "every <duration>" — e.g. "every 5m", "every 2h", "every 30s".
//
// Durations below MinScheduleInterval are clamped up to that floor.
func ParseSchedule(schedule string) (interval time.Duration, active bool, err error) {
	schedule = strings.TrimSpace(schedule)

	if schedule == "" || schedule == "on_event" {
		return 0, false, nil
	}

	if !strings.HasPrefix(schedule, "every ") {
		return 0, false, fmt.Errorf("invalid schedule format: %q (expected \"on_event\" or \"every <duration>\")", schedule)
	}

	durStr := strings.TrimPrefix(schedule, "every ")
	durStr = strings.TrimSpace(durStr)
	if durStr == "" {
		return 0, false, fmt.Errorf("invalid schedule format: %q (missing duration after \"every\")", schedule)
	}

	d, err := time.ParseDuration(durStr)
	if err != nil {
		return 0, false, fmt.Errorf("invalid schedule duration %q: %w", durStr, err)
	}

	if d <= 0 {
		return 0, false, fmt.Errorf("schedule duration must be positive, got %v", d)
	}

	// Clamp to minimum interval.
	if d < MinScheduleInterval {
		d = MinScheduleInterval
	}

	return d, true, nil
}
