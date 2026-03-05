package cli

import (
	"strings"
	"testing"
)

func TestBuildWolandPrompt_ContainsKeySections(t *testing.T) {
	prompt := buildWolandPrompt(
		"/tmp/test-apartment",
		"name: test\nrepos:\n  api: /tmp/api\nmodel: claude-opus-4-6\nmax_workers: 4\n",
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
		{"help config hint", "retinue help config"},
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

func TestBuildBabytalkPrompt_ContainsKeySections(t *testing.T) {
	prompt := buildBabytalkPrompt(
		"/tmp/test-apartment",
		"name: test\nrepos:\n  web: /tmp/web\nmodel: claude-opus-4-6\nmax_workers: 4\n",
		"tasks:\n  - id: task-1\n    status: pending\n",
	)

	checks := []struct {
		name     string
		contains string
	}{
		{"persona", "You are Woland"},
		{"builder context", "not a software engineer"},
		{"apartment path", "/tmp/test-apartment"},
		{"config included", "name: test"},
		{"tasks included", "task-1"},
		{"tasks.yaml path", "/tmp/test-apartment/tasks.yaml"},
		{"retry default", "--retry"},
		{"merge review default", "merge --review"},
		{"quality standards", "Quality Standards"},
		{"linting check", "eslint"},
		{"explain decisions", "Explaining Decisions"},
		{"validation emphasis", "non-negotiable"},
		{"help config hint", "retinue help config"},
	}

	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if !strings.Contains(prompt, c.contains) {
				t.Errorf("babytalk prompt missing %q (%s)", c.contains, c.name)
			}
		})
	}
}

func TestBuildBabytalkPrompt_DiffersFromTalk(t *testing.T) {
	args := []string{
		"/tmp/apt",
		"name: test\n",
		"tasks: []\n",
	}
	talk := buildWolandPrompt(args[0], args[1], args[2])
	baby := buildBabytalkPrompt(args[0], args[1], args[2])

	if talk == baby {
		t.Error("babytalk prompt should differ from talk prompt")
	}

	// Babytalk should have quality-focused content that talk doesn't.
	if !strings.Contains(baby, "Quality Standards") {
		t.Error("babytalk should contain Quality Standards section")
	}
	if strings.Contains(talk, "Quality Standards") {
		t.Error("talk prompt should NOT contain Quality Standards section")
	}
}
