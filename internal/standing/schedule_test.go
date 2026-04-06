package standing

import (
	"testing"
	"time"
)

func TestParseSchedule(t *testing.T) {
	tests := []struct {
		name         string
		schedule     string
		wantInterval time.Duration
		wantActive   bool
		wantErr      bool
	}{
		{
			name:       "on_event returns no schedule",
			schedule:   "on_event",
			wantActive: false,
		},
		{
			name:         "every 5m returns 5 minutes",
			schedule:     "every 5m",
			wantInterval: 5 * time.Minute,
			wantActive:   true,
		},
		{
			name:         "every 2h returns 2 hours",
			schedule:     "every 2h",
			wantInterval: 2 * time.Hour,
			wantActive:   true,
		},
		{
			name:         "every 30s returns 30 seconds",
			schedule:     "every 30s",
			wantInterval: 30 * time.Second,
			wantActive:   true,
		},
		{
			name:         "every 10s clamped to 30 seconds minimum",
			schedule:     "every 10s",
			wantInterval: 30 * time.Second,
			wantActive:   true,
		},
		{
			name:       "empty string returns no schedule",
			schedule:   "",
			wantActive: false,
		},
		{
			name:    "invalid format returns error",
			schedule: "cron * * * * *",
			wantErr: true,
		},
		{
			name:    "every with invalid duration returns error",
			schedule: "every notaduration",
			wantErr: true,
		},
		{
			name:    "every with missing duration returns error",
			schedule: "every ",
			wantErr: true,
		},
		{
			name:       "whitespace-only treated as empty",
			schedule:   "   ",
			wantActive: false,
		},
		{
			name:         "every 1m returns 1 minute",
			schedule:     "every 1m",
			wantInterval: 1 * time.Minute,
			wantActive:   true,
		},
		{
			name:    "negative duration returns error",
			schedule: "every -5m",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			interval, active, err := ParseSchedule(tt.schedule)

			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseSchedule(%q) expected error, got nil", tt.schedule)
				}
				return
			}

			if err != nil {
				t.Errorf("ParseSchedule(%q) unexpected error: %v", tt.schedule, err)
				return
			}

			if active != tt.wantActive {
				t.Errorf("ParseSchedule(%q) active = %v, want %v", tt.schedule, active, tt.wantActive)
			}

			if interval != tt.wantInterval {
				t.Errorf("ParseSchedule(%q) interval = %v, want %v", tt.schedule, interval, tt.wantInterval)
			}
		})
	}
}
