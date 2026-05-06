package standing

import (
	"strings"
	"testing"
)

func TestValidate_Valid(t *testing.T) {
	agents := []Agent{
		{ID: "azazello", Prompt: "Watch CI."},
		{ID: "behemoth", Prompt: "Guard the code."},
		{ID: "koroviev-2", Prompt: "Review PRs."},
	}

	if err := Validate(agents); err != nil {
		t.Errorf("Validate() unexpected error = %v", err)
	}
}

func TestValidate_EmptySlice(t *testing.T) {
	if err := Validate([]Agent{}); err != nil {
		t.Errorf("Validate() unexpected error for empty slice = %v", err)
	}
}

func TestValidate_DuplicateIDs(t *testing.T) {
	agents := []Agent{
		{ID: "azazello", Prompt: "Watch CI."},
		{ID: "behemoth", Prompt: "Guard the code."},
		{ID: "azazello", Prompt: "Duplicate."},
	}

	err := Validate(agents)
	if err == nil {
		t.Fatal("Validate() expected error for duplicate IDs")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("error should mention 'duplicate', got: %v", err)
	}
	if !strings.Contains(err.Error(), "azazello") {
		t.Errorf("error should mention the duplicate ID, got: %v", err)
	}
}

func TestValidate_EmptyID(t *testing.T) {
	agents := []Agent{
		{ID: "", Prompt: "Watch CI."},
	}

	err := Validate(agents)
	if err == nil {
		t.Fatal("Validate() expected error for empty ID")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error should mention 'empty', got: %v", err)
	}
}

func TestValidate_EmptyPrompt(t *testing.T) {
	agents := []Agent{
		{ID: "azazello", Prompt: ""},
	}

	err := Validate(agents)
	if err == nil {
		t.Fatal("Validate() expected error for empty prompt")
	}
	if !strings.Contains(err.Error(), "prompt") {
		t.Errorf("error should mention 'prompt', got: %v", err)
	}
}

func TestValidate_InvalidKebabCase(t *testing.T) {
	tests := []struct {
		name string
		id   string
	}{
		{"uppercase", "Azazello"},
		{"spaces", "my agent"},
		{"underscores", "my_agent"},
		{"leading hyphen", "-agent"},
		{"trailing hyphen", "agent-"},
		{"double hyphen", "my--agent"},
		{"special chars", "agent@1"},
		{"camelCase", "myAgent"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			agents := []Agent{
				{ID: tc.id, Prompt: "Some prompt."},
			}

			err := Validate(agents)
			if err == nil {
				t.Errorf("Validate() expected error for invalid ID %q", tc.id)
			}
			if err != nil && !strings.Contains(err.Error(), "kebab-case") {
				t.Errorf("error should mention 'kebab-case', got: %v", err)
			}
		})
	}
}

func TestValidate_ValidKebabCase(t *testing.T) {
	tests := []struct {
		name string
		id   string
	}{
		{"simple", "agent"},
		{"with hyphen", "ci-watcher"},
		{"multiple hyphens", "my-cool-agent"},
		{"with numbers", "agent-1"},
		{"numbers only", "123"},
		{"mixed", "agent-2b"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			agents := []Agent{
				{ID: tc.id, Prompt: "Some prompt."},
			}

			if err := Validate(agents); err != nil {
				t.Errorf("Validate() unexpected error for valid ID %q: %v", tc.id, err)
			}
		})
	}
}

func TestValidate_MultipleErrors_ReportsFirst(t *testing.T) {
	// Multiple problems — we just need to report at least the first.
	agents := []Agent{
		{ID: "", Prompt: ""},
	}

	err := Validate(agents)
	if err == nil {
		t.Fatal("Validate() expected error")
	}
	// Empty ID is checked before empty prompt, so we should get that error.
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("expected empty ID error first, got: %v", err)
	}
}

func TestValidate_ValidSchedule(t *testing.T) {
	tests := []struct {
		name     string
		schedule string
	}{
		{"empty", ""},
		{"on_event", "on_event"},
		{"every 30s", "every 30s"},
		{"every 5m", "every 5m"},
		{"every 2h", "every 2h"},
		{"every 1h30m", "every 1h30m"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			agents := []Agent{
				{ID: "test-agent", Prompt: "Test prompt", Schedule: tc.schedule},
			}

			if err := Validate(agents); err != nil {
				t.Errorf("Validate() unexpected error for valid schedule %q: %v", tc.schedule, err)
			}
		})
	}
}

func TestValidate_InvalidSchedule(t *testing.T) {
	tests := []struct {
		name     string
		schedule string
	}{
		{"invalid format", "invalid"},
		{"cron format", "0 9 * * 1-5"},
		{"missing every", "5m"},
		{"invalid duration", "every invalid"},
		{"too short", "every 15s"},
		{"negative duration", "every -5m"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			agents := []Agent{
				{ID: "test-agent", Prompt: "Test prompt", Schedule: tc.schedule},
			}

			err := Validate(agents)
			if err == nil {
				t.Errorf("Validate() expected error for invalid schedule %q", tc.schedule)
			}
			if err != nil && !strings.Contains(err.Error(), "schedule") {
				t.Errorf("error should mention 'schedule', got: %v", err)
			}
		})
	}
}

func TestValidate_Effort_AcceptsValidLevels(t *testing.T) {
	for _, level := range []string{"", "low", "medium", "high", "xhigh", "max"} {
		t.Run(level, func(t *testing.T) {
			agents := []Agent{
				{ID: "agent-x", Prompt: "do work", Effort: level},
			}
			if err := Validate(agents); err != nil {
				t.Errorf("Validate() unexpected error for valid effort %q: %v", level, err)
			}
		})
	}
}

func TestValidate_Effort_RejectsInvalidLevel(t *testing.T) {
	agents := []Agent{
		{ID: "azazello", Prompt: "do work", Effort: "ultra"},
	}
	err := Validate(agents)
	if err == nil {
		t.Fatal("Validate() expected error for invalid effort 'ultra'")
	}
	if !strings.Contains(err.Error(), "ultra") {
		t.Errorf("error should mention 'ultra', got: %v", err)
	}
	if !strings.Contains(err.Error(), "agents.yaml") {
		t.Errorf("error should mention 'agents.yaml', got: %v", err)
	}
	if !strings.Contains(err.Error(), "azazello") {
		t.Errorf("error should mention the agent id, got: %v", err)
	}
}
