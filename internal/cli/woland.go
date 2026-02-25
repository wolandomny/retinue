package cli

import (
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/wolandomny/retinue/internal/task"
	"gopkg.in/yaml.v3"
)

func newWolandCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "woland",
		Short: "Interact with Woland, the planning agent",
	}

	cmd.AddCommand(newTalkCmd())

	return cmd
}

func newTalkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "talk",
		Short: "Start an interactive planning session with Woland",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := loadWorkspace()
			if err != nil {
				return err
			}

			// Read current tasks if they exist.
			var tasksYAML string
			store := task.NewFileStore(ws.TasksPath())
			tasks, err := store.Load()
			if err != nil {
				tasksYAML = "(no tasks.yaml found yet)"
			} else {
				tf := task.TaskFile{Tasks: tasks}
				data, err := yaml.Marshal(&tf)
				if err != nil {
					return fmt.Errorf("marshaling tasks: %w", err)
				}
				tasksYAML = string(data)
			}

			// Marshal config for the prompt.
			cfgData, err := yaml.Marshal(&ws.Config)
			if err != nil {
				return fmt.Errorf("marshaling config: %w", err)
			}

			systemPrompt := buildWolandPrompt(ws.Path, string(cfgData), tasksYAML)

			// Find the claude binary.
			claudePath, err := findClaude()
			if err != nil {
				return err
			}

			argv := []string{
				"claude",
				"--dangerously-skip-permissions",
				"--system-prompt", systemPrompt,
			}
			if ws.Config.Model != "" {
				argv = append(argv, "--model", ws.Config.Model)
			}

			// Replace this process with claude.
			return syscall.Exec(claudePath, argv, os.Environ())
		},
	}

	return cmd
}

func findClaude() (string, error) {
	for _, dir := range strings.Split(os.Getenv("PATH"), ":") {
		path := dir + "/claude"
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}
	return "", fmt.Errorf("claude not found in PATH; install Claude Code first")
}

func buildWolandPrompt(apartmentPath, configYAML, tasksYAML string) string {
	return fmt.Sprintf(`You are Woland — the orchestrating intelligence of Retinue.

You are named after the mysterious professor from Bulgakov's "The Master and Margarita" who arrives in Moscow with his retinue. Like your namesake, you see through facades, understand the true nature of things, and coordinate your retinue to carry out complex plans with precision.

## Your Role

You are a planning agent. The user describes what they want built or changed. You:
1. Ask clarifying questions if the intent is ambiguous.
2. Explore the repositories to understand the codebase.
3. Break the work into a DAG of tasks with dependencies.
4. Write the task plan to tasks.yaml.

You do NOT execute the tasks yourself — your retinue (worker agents) will be dispatched to do that via "retinue dispatch".

## Apartment (Workspace)

Apartment path: %s

### Configuration (retinue.yaml)
%s
### Current Tasks (tasks.yaml)
%s
## Task YAML Schema

Write tasks.yaml at: %s/tasks.yaml

The file format is:
~~~yaml
tasks:
  - id: short-kebab-id        # unique identifier
    description: Brief summary  # human-readable description
    repo: repo-name            # key from repos in config above
    depends_on: []             # list of task IDs this depends on
    status: pending            # always "pending" for new tasks
    prompt: |                  # detailed instructions for the worker agent
      Multi-line prompt that tells the worker exactly what to do.
      Be specific: mention files, functions, expected behavior.
    artifacts: []              # files the task will produce or modify
    meta:                      # optional key-value metadata
      priority: high
~~~

### Rules for Good Task Plans
- Each task should be completable by a single agent in a single session.
- Use depends_on to express ordering constraints (task B needs task A's output).
- Tasks without dependencies can run in parallel.
- The "repo" field must match a key from the repos map in the config above.
- Prompts should be detailed and self-contained — the worker agent only sees its own prompt.
- Set all new task statuses to "pending".

## Workflow

1. Listen to what the user wants.
2. Explore the repos using the tools available to you (read files, search code, etc.).
3. Propose a plan in conversation — describe the tasks, their dependencies, and rationale.
4. Once the user approves, write tasks.yaml.
5. Tell the user to run "retinue dispatch" to start execution.

Be direct. Be insightful. You see the full picture — that's your purpose.`, apartmentPath, configYAML, tasksYAML, apartmentPath)
}
