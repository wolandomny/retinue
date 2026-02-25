package cli

import (
	"strings"
	"testing"
)

func TestBuildWolandPrompt_ContainsKeySections(t *testing.T) {
	prompt := buildWolandPrompt(
		"/tmp/test-apartment",
		"name: test\nrepos:\n  api: /tmp/api\nmodel: claude-sonnet-4-6\nmax_workers: 4\n",
		"tasks:\n  - id: task-1\n    status: pending\n",
	)

	checks := []struct {
		name     string
		contains string
	}{
		{"persona", "You are Woland"},
		{"apartment path", "/tmp/test-apartment"},
		{"config included", "name: test"},
		{"repos in config", "api: /tmp/api"},
		{"tasks included", "task-1"},
		{"tasks.yaml path", "/tmp/test-apartment/tasks.yaml"},
		{"schema id field", "id: short-kebab-id"},
		{"schema depends_on", "depends_on:"},
		{"schema prompt field", "prompt: |"},
		{"dispatch instruction", "retinue dispatch"},
	}

	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if !strings.Contains(prompt, c.contains) {
				t.Errorf("prompt missing %q (%s)", c.contains, c.name)
			}
		})
	}
}

func TestBuildWolandPrompt_NoTasksMessage(t *testing.T) {
	prompt := buildWolandPrompt("/tmp/apt", "name: x\n", "(no tasks.yaml found yet)")

	if !strings.Contains(prompt, "(no tasks.yaml found yet)") {
		t.Error("prompt should include no-tasks message when tasks are empty")
	}
}
