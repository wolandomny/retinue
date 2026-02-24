# Retinue

A multi-agent orchestration CLI that coordinates multiple Claude Code agents to work on tasks in parallel across repositories.

Retinue manages task dependencies as a DAG, dispatches ready tasks to Claude Code agents in isolated git worktrees, and tracks progress through a structured workflow.

## Installation

```bash
make build      # builds to bin/retinue
make install    # installs to $GOPATH/bin
```

Requires Go 1.25+ and [Claude Code](https://docs.anthropic.com/en/docs/claude-code) installed.

## Usage

### Initialize a workspace

```bash
retinue --workspace ./my-project init --name my-project --repo api=./path/to/api --repo web=./path/to/web
```

This creates a workspace directory with a `retinue.yaml` config and `tasks.yaml` for tracking work.

### Add tasks

Define tasks in the workspace's `tasks.yaml` with descriptions, repo assignments, and dependencies between them.

### Dispatch work

```bash
retinue --workspace ./my-project dispatch            # run all ready tasks
retinue --workspace ./my-project dispatch --task t1   # run a specific task
```

Each task runs in its own git worktree branch (`retinue/<task-id>`), keeping work isolated.

### Check status

```bash
retinue --workspace ./my-project status
```

Tasks move through: `pending` → `in_progress` → `done` → `review` → `merged` (or `failed`).
