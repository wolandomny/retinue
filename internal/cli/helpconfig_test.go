package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestHelpConfigOutput(t *testing.T) {
	cmd := newHelpConfigCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.Execute()

	output := buf.String()

	checks := []struct {
		name     string
		contains string
	}{
		{"retinue.yaml header", "retinue.yaml"},
		{"tasks.yaml header", "tasks.yaml"},
		{"name field", "name"},
		{"github_account", "github_account"},
		{"model", "model"},
		{"max_workers", "max_workers"},
		{"repos", "repos"},
		{"validate", "validate"},
		{"base_branch", "base_branch"},
		{"commit_style", "commit_style"},
		{"conventional", "conventional"},
		{"task id", "id"},
		{"depends_on", "depends_on"},
		{"status values", "pending"},
		{"artifacts", "artifacts"},
		{"prompt field", "prompt"},
		{"resolution order", "resolution order"},
		{"agents.yaml header", "agents.yaml"},
		{"agent id field", "id              string      Unique kebab-case identifier"},
		{"agent enabled field", "enabled         bool"},
		{"agent schedule values", "on_event"},
		{"agent schedule cron", "Cron expression"},
		{"schedule not implemented", "not yet implemented"},
		{"agent commands", "retinue agent list"},
		{"example azazello", "azazello"},
		{"example behemoth", "behemoth"},
	}

	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if !strings.Contains(output, c.contains) {
				t.Errorf("help config output missing %q (%s)", c.contains, c.name)
			}
		})
	}
}

func TestHelpConfigRegistered(t *testing.T) {
	root := newRootCmd()

	// Find the help command.
	helpCmd, _, _ := root.Find([]string{"help"})
	if helpCmd == nil {
		t.Fatal("help command not found")
	}

	// Check that config subcommand exists under help.
	found := false
	for _, sub := range helpCmd.Commands() {
		if sub.Name() == "config" {
			found = true
			break
		}
	}
	if !found {
		t.Error("'config' subcommand not found under 'help'")
	}
}
