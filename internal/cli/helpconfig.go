package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

const configReference = `retinue configuration reference

═══════════════════════════════════════════
retinue.yaml — Apartment Configuration
═══════════════════════════════════════════

Top-level fields:

  name              string    Apartment display name
  github_account    string    GitHub account for git operations
  model             string    Claude model for agents (e.g. "claude-opus-4-6")
  max_workers       int       Maximum concurrent worker agents

  repos             map       Repository configurations (see below)
  validate          map       Repo name → shell command run before merge

Repos — two formats supported:

  # Simple format (path only):
  repos:
    my-repo: repos/my-repo

  # Full format (all options):
  repos:
    my-repo:
      path: repos/my-repo
      base_branch: develop           # branch to merge into (default: "main")
      commit_style: conventional     # commit message style for workers

  commit_style values:
    "conventional"  →  Conventional Commits (feat:, fix:, refactor:, etc.)
    any string      →  passed verbatim as commit instructions to workers
    (omit)          →  no commit style enforcement

Validate — shell commands run before merging each task branch:

  validate:
    my-repo: "go build ./... && go test ./..."
    web-app: "npm run lint && npm run build && npm test"

Full example:

  name: my-project
  github_account: myorg
  repos:
    backend:
      path: repos/backend
      base_branch: develop
      commit_style: conventional
    frontend: repos/frontend
  model: claude-opus-4-6
  max_workers: 10
  validate:
    backend: "go build ./... && go test ./..."
    frontend: "npm ci && npm run lint && npm test"

═══════════════════════════════════════════
tasks.yaml — Task Definitions
═══════════════════════════════════════════

Top-level structure:

  tasks:
    - id: ...
      ...

Task fields:

  id              string      Unique kebab-case identifier
  description     string      Brief human-readable summary
  repo            string      Key from repos map in retinue.yaml
  branch          string      Git branch name (auto-generated if omitted)
  base_branch     string      Branch to merge into (overrides repo config)
  depends_on      []string    Task IDs this depends on (for ordering)
  status          string      Task status (see below)
  prompt          string      Detailed instructions for the worker agent
  artifacts       []string    Files the task will create or modify
  result          string      Worker output (set by system)
  error           string      Error message if failed (set by system)
  started_at      timestamp   When dispatch started (set by system)
  finished_at     timestamp   When task completed (set by system)
  meta            map         Arbitrary key-value metadata

Status values:

  pending       Ready to be dispatched (or waiting on dependencies)
  in_progress   Currently being worked on by an agent
  done          Agent finished, ready for merge
  merged        Successfully merged into base branch
  failed        Agent failed or validation failed

Base branch resolution order:

  1. Task base_branch field (if set)
  2. Repo config base_branch (if set)
  3. "main" (default)

Example task:

  tasks:
    - id: add-auth
      description: Add JWT authentication middleware
      repo: backend
      base_branch: develop
      depends_on: []
      status: pending
      prompt: |
        Add JWT authentication middleware to the API server.
        ...detailed instructions...
      artifacts:
        - internal/auth/jwt.go
        - internal/auth/jwt_test.go
`

func newHelpConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show configuration reference for retinue.yaml and tasks.yaml",
		Long:  "Print the complete schema reference for retinue.yaml (apartment config) and tasks.yaml (task definitions).",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprint(cmd.OutOrStdout(), configReference)
		},
	}

	return cmd
}
