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

## Configuration

The `retinue.yaml` file configures your apartment:

```yaml
name: my-project
github_account: myorg
repos:
  api: repos/api
  web: repos/web
model: claude-opus-4-6
max_workers: 20
validate:
  api: "go build ./... && go test ./..."
  web: "npm run build && npm test"
```

The `github_account` field tells retinue which GitHub account to use for git operations.

The `validate` field is optional but recommended. It maps repo names to shell commands that Hella runs before merging each task branch. If validation fails, the task is marked "failed" and the branch is not merged.

## Talking to Woland

Woland is the planning agent — your interface for describing what you want built. He breaks work into tasks, dispatches them, and monitors progress.

```bash
retinue woland talk
```

This opens (or reattaches to) an interactive Claude Code session inside tmux. Describe what you want and Woland will:

1. Explore your repos to understand the codebase.
2. Propose a plan — tasks, dependencies, and rationale.
3. Write the plan to `tasks.yaml` once you approve.
4. Dispatch workers with `retinue dispatch --all`.
5. Merge completed branches with `retinue merge`.
6. Report results back to you.

Woland persists across disconnects. If you detach from tmux or close your terminal, `retinue woland talk` reattaches to the same session.

## How work gets done

### The tmux server

All retinue processes run on a shared tmux server with the socket name `retinue-<apartment-name>`. Within that server, there is one tmux session named `retinue`. Woland runs in a window named `woland`, and each worker runs in a window named after its task ID.

You can see everything that's running:

```bash
tmux -L retinue-my-project list-windows -t retinue
```

Worker windows auto-close when the task completes successfully. Failed task windows stay open so you can attach and inspect.

### Dispatching tasks

When Woland (or you) runs `retinue dispatch --all`:

- Each ready task (pending, dependencies resolved) gets its own Claude Code agent.
- Each agent runs in an isolated git worktree on branch `retinue/<task-id>`.
- Up to `max_workers` tasks run concurrently.
- As tasks finish, newly unblocked tasks are dispatched automatically.
- Dispatch exits when all tasks are done or failed.

You can also dispatch a single task:

```bash
retinue dispatch --task my-task-id
```

Flags:

- `--all` — dispatch all ready tasks and keep going until everything is done or failed.
- `--task <id>` — dispatch a single task by ID.
- `--retry` — automatically retry failed tasks. This doesn't just append the error and re-run — it spawns a Claude call to analyze the failure and rewrite the task prompt.
- `--max-retries N` — maximum retry rounds (default: 2). Used with `--retry`.

If independent tasks list overlapping artifacts, dispatch warns but does not block parallel execution. Use `depends_on` when ordering actually matters; for the rest, merge-time rebase with conflict resolution handles overlapping changes.

### Watchdog

Dispatch includes a watchdog that monitors worker log files while tasks are running. It polls every 30 seconds and catches two failure modes:

- **Stalled workers** — no output for 10 minutes. The watchdog kills the window and fails the task, capturing the last 3 lines of output as diagnostic context.
- **Looping workers** — 20+ identical consecutive log lines. Same treatment: kill and fail with the repeated line included.

This prevents stuck agents from burning tokens indefinitely.

### Watching workers

List active windows:

```bash
retinue ls
```

This shows all tmux windows in the apartment with their status (planning, in_progress, active).

Attach to a running worker:

```bash
retinue attach my-task-id
```

Detach with `Ctrl-b d` to leave it running.

Worker logs are also written to the `logs/` directory in your apartment.

### Merging completed work (Hella)

After tasks complete (status "done"), `retinue merge` lands their branches:

```bash
retinue merge
```

For each done task, Hella:

1. Runs the validation command (if configured in `validate`).
2. Rebases the task branch onto the base branch (main).
3. If there are rebase conflicts, spawns a Claude agent to resolve them (up to 5 attempts per task).
4. Fast-forward merges into the base branch (guaranteed — no merge commits).
5. Cleans up the worktree and branch.

If validation or merge fails, the task is marked "failed" with the error output.

Flags:

- `--task <id>` — merge a single task by ID.
- `--review` — run a lightweight AI review of the diff against the original task prompt before merging. If the review rejects the work, the task goes back to pending with the feedback appended to its prompt.

### Task lifecycle

Tasks move through: `pending` → `in_progress` → `done` → `merged` (or `failed`).

Check status at any time:

```bash
retinue status
```

The status table shows each task's ID, status, repo, token usage, and description. The TOKENS column displays input/output token counts and cost per task. Aggregate totals are printed at the bottom.

## Options

- `-w, --workspace <path>` — point to an apartment directory (default: current directory)
