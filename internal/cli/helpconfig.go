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
  effort            string    Adaptive-reasoning depth for agents (see below).
                              One of: low, medium, high, xhigh, max. Empty = unset.
                              Note: xhigh is Opus 4.7 only; the 4.6 line accepts
                              low/medium/high/max.
  max_workers       int       Maximum concurrent worker agents
  track_costs       bool      Track token usage and costs per task (default: false)

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
  effort: high
  max_workers: 10
  validate:
    backend: "go build ./... && go test ./..."
    frontend: "npm ci && npm run lint && npm test"

Effort resolution:

  Effort controls Claude Code's adaptive-reasoning depth (the --effort
  flag). It is independent of model selection.

  Valid values:
    low      Minimal reasoning. Fastest, cheapest. Use for trivial work.
    medium   Balanced default for typical coding tasks.
    high     Deeper deliberation. Use for architectural work.
    xhigh    Extra-high. Opus 4.7 only — the 4.6 line rejects this value.
    max      Maximum reasoning budget. Use for synthesis-heavy work.
    ""       Unset — defer to the model's per-version default.

  Where it can be set:
    - Workspace (retinue.yaml)         applies to all agents
    - Standing agent (agents.yaml)     overrides workspace per agent
    - Task (tasks.yaml)                overrides workspace per worker

  Resolution order (first match wins):
    1. task.effort (or agent.effort)
    2. workspace.effort
    3. unset → Claude Code's per-model default

  Recommendation:
    Leave unset for typical work. Bump to "high" or "max" for tasks you'd
    want a senior engineer to slow down on. Drop to "low" when speed
    matters more than depth.

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
  model           string      Claude model override (falls back to workspace model)
  effort          string      Effort level override (low|medium|high|xhigh|max).
                              Falls back to workspace effort, then unset. xhigh is
                              Opus 4.7 only.
  depends_on      []string    Task IDs this depends on (for ordering)
  status          string      Task status (see below)
  prompt          string      Detailed instructions for the worker agent
  artifacts       []string    Files the task will create or modify
  result          string      Worker output (set by system)
  error           string      Error message if failed (set by system)
  started_at      timestamp   When dispatch started (set by system)
  finished_at     timestamp   When task completed (set by system)
  meta            map         Arbitrary key-value metadata
  skip_validate   bool        Skip per-task validation before merge (default: false)

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
      # model: claude-opus-4-6      # optional override
      effort: high                  # optional — architectural work, slow down
      depends_on: []
      status: pending
      prompt: |
        Add JWT authentication middleware to the API server.
        ...detailed instructions...
      artifacts:
        - internal/auth/jwt.go
        - internal/auth/jwt_test.go

═══════════════════════════════════════════
agents.yaml — Standing Agent Definitions
═══════════════════════════════════════════

Standing agents are long-lived, user-defined agents that run alongside
Woland. Unlike ephemeral task workers, standing agents persist with
ongoing mandates — they watch, guard, and maintain the codebase.

Top-level structure:

  agents:
    - id: ...
      ...

Agent fields:

  id              string      (required) Unique kebab-case identifier for the agent.
                              Must be lowercase alphanumeric with hyphens. Used in CLI
                              commands and file naming.
  name            string      (required) Human-readable display name shown in agent
                              list and group chat messages.
  role            string      (optional) Brief role description (e.g., "CI Watcher",
                              "Notion Watcher"). Shown in agent list output.
  repos           []string    (optional) List of repo keys from retinue.yaml that
                              this agent accesses. Included in the agent's system
                              prompt for context.
  schedule        string      (optional) Controls when the agent receives heartbeat
                              triggers. Values:
                              - "on_event" or empty — Agent only responds to explicit
                                messages. This is the default.
                              - "every <duration>" — Agent receives a "[Heartbeat]
                                Scheduled check" message at the specified interval.
                                Duration uses Go syntax: "30s", "5m", "2h".
                                Minimum interval is 30 seconds.
                              Examples: "every 5m", "every 2h", "every 30s"
  model           string      (optional) Claude model override for this agent.
                              Falls back to the workspace-level model from
                              retinue.yaml.
  effort          string      (optional) Effort level override for this agent.
                              One of: low, medium, high, xhigh, max.
                              Falls back to the workspace-level effort, then to
                              the model's default. xhigh is Opus 4.7 only.
  prompt          string      (required) The agent's mandate. Detailed instructions
                              defining what the agent does, what it watches for, and
                              how it responds. For scheduled agents, include
                              instructions for handling heartbeat messages.
  enabled         bool        (optional, default: false) Must be true before the
                              agent can be started with "retinue agent start".

The enabled field:

  Agents default to enabled: false. An agent must have enabled: true
  before it can be started with retinue agent start. This lets you
  define agents in agents.yaml without activating them immediately.

Agent commands:

  retinue agent list          Show all defined agents and running status
  retinue agent start <id>    Start a standing agent in a tmux window
  retinue agent stop <id>     Stop a running standing agent

Example agents.yaml:

  agents:
    - id: azazello
      name: Azazello
      role: CI Watcher
      repos: [backend, frontend]
      schedule: "on_event"
      model: claude-sonnet-4-20250514
      effort: medium
      prompt: |
        You are Azazello, the enforcer. Watch CI pipelines for failures.
        When a build or test fails:
        1. Analyze the failure logs to identify the root cause.
        2. If the fix is obvious and localized, fix it directly.
        3. If the failure is complex or architectural, escalate by
           creating a task in tasks.yaml with a detailed description.
        Focus on the repos listed above. Do not make speculative changes.
      enabled: true

    - id: behemoth
      name: Behemoth
      role: Codebase Gardener
      repos: [backend]
      schedule: "every 2h"
      effort: high
      prompt: |
        You are Behemoth, the scholarly cat. You run on a 2-hour schedule
        to periodically review the codebase for quality issues.

        When you receive a "[Heartbeat] Scheduled check" message:
        1. Review the codebase for:
           - Dead code and unused imports
           - Missing or outdated tests
           - Documentation gaps
           - TODO/FIXME comments that should be resolved
        2. Report findings as a summary
        3. For clear improvements, submit them as tasks

        Do not refactor working code without cause. Focus on the repos
        listed above.
      enabled: false
`

func newHelpConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show configuration reference for retinue.yaml, tasks.yaml, and agents.yaml",
		Long:  "Print the complete schema reference for retinue.yaml (apartment config), tasks.yaml (task definitions), and agents.yaml (standing agent definitions).",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprint(cmd.OutOrStdout(), configReference)
		},
	}

	return cmd
}
