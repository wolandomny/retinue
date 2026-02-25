# Retinue

A multi-agent orchestration CLI that coordinates multiple Claude Code agents to work on tasks in parallel across repositories.

Retinue manages task dependencies as a DAG, dispatches ready tasks to Claude Code agents in isolated git worktrees, and tracks progress through a structured workflow.

## Installation

```bash
make build      # builds to bin/retinue
make install    # installs to $GOPATH/bin
```

Requires Go 1.25+ and [Claude Code](https://docs.anthropic.com/en/docs/claude-code) installed.

## Quick start

```bash
retinue init my-project
cd my-project
retinue add repo github.com/org/api
retinue add repo github.com/org/web
```

This creates an apartment (workspace) with a `retinue.yaml` config and `tasks.yaml` for tracking work, then clones your repos into it.

You can also initialize in the current directory:

```bash
mkdir my-project && cd my-project
retinue init
retinue add repo github.com/org/api
```

## Usage

### Add tasks

Define tasks in the apartment's `tasks.yaml` with descriptions, repo assignments, and dependencies between them.

### Dispatch work

```bash
retinue dispatch            # run all ready tasks
retinue dispatch --task t1  # run a specific task
```

Each task runs in its own git worktree branch (`retinue/<task-id>`), keeping work isolated.

### Check status

```bash
retinue status
```

Tasks move through: `pending` → `in_progress` → `done` → `review` → `merged` (or `failed`).

### Options

- `-w, --workspace <path>` — point to an apartment directory (default: current directory)
