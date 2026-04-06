# Retinue

A multi-agent orchestration CLI that coordinates multiple Claude Code agents to work on tasks in parallel across repositories.

Retinue manages task dependencies as a DAG, dispatches ready tasks to Claude Code agents in isolated git worktrees, and tracks progress through a structured workflow.

## Installation

Requires [Claude Code](https://docs.anthropic.com/en/docs/claude-code) and [tmux](https://github.com/tmux/tmux).

### Homebrew (recommended)

```bash
brew tap wolandomny/retinue
brew install retinue
```

Upgrade to the latest version:

```bash
brew upgrade retinue
```

### From source

Requires [Go 1.25+](https://go.dev/dl/).

```bash
make install    # builds the binary and installs to $(go env GOPATH)/bin
```

Make sure `$(go env GOPATH)/bin` is in your `PATH`. If you haven't already, add this to your shell profile (`~/.zshrc`, `~/.bashrc`, etc.):

```bash
export PATH="$PATH:$(go env GOPATH)/bin"
```

Then reload your shell (`source ~/.zshrc`) or open a new terminal.

### Verify

```bash
retinue --help
```

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

### Multiple GitHub accounts

If you have multiple apartments that push to repos under different GitHub accounts, you'll want per-repo credential configuration so git uses the right account automatically.

First, make sure both accounts are authenticated with the GitHub CLI:

```bash
gh auth login    # login to your first account
gh auth login    # login to your second account
```

Then create a credential helper script in each repo's `.git/` directory. For example, for a repo owned by `myorg`:

```bash
cat > repos/my-repo/.git/credential-helper.sh << 'EOF'
#!/bin/sh
if [ "$1" = "get" ]; then
    echo "protocol=https"
    echo "host=github.com"
    echo "username=myorg"
    echo "password=$(gh auth token --user myorg)"
fi
EOF
chmod +x repos/my-repo/.git/credential-helper.sh
```

Then configure git to use it, with an empty `helper =` line first to clear any global credential helpers:

```bash
git -C repos/my-repo config credential.https://github.com.helper ''
git -C repos/my-repo config --add credential.https://github.com.helper \
  '!repos/my-repo/.git/credential-helper.sh'
```

This persists in the repo's `.git/config` and works regardless of which `gh auth` account is currently active. Without this, git falls back to whichever account `gh auth switch` was last set to, which breaks when you're running multiple apartments concurrently.

The script lives inside `.git/` so it's not tracked by version control.

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

There's also a guided variant for non-engineers:

```bash
retinue woland babytalk
```

Babytalk uses the same planning workflow but with a system prompt tuned for non-engineer builders. It provides more architectural explanation, learning-focused guidance, and always dispatches with `--retry --review` for safer execution.

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

Merges happen sequentially (one at a time) to avoid rebase races.

Flags:

- `--task <id>` — merge a single task by ID.
- `--review` — run a lightweight AI review of the diff against the original task prompt before merging. If the review rejects the work, the task goes back to pending with the feedback appended to its prompt.

### The all-in-one loop

`retinue run` combines dispatch and merge into a single loop. It dispatches ready tasks, waits for them to finish, merges completed branches, and repeats until everything is done or failed.

```bash
retinue run
```

This is typically what Woland calls under the hood. Flags:

- `--retry` — automatically retry failed tasks. Each retry spawns a Claude call to analyze the failure and rewrite the task prompt before re-dispatching.
- `--max-retries N` — maximum retry rounds (default: 2). Used with `--retry`.
- `--review` — run an AI review of each task's diff before merging. If the review rejects the work, the task goes back to pending with feedback appended to its prompt.

At the end of a run, retinue runs the validation command on each repo against the combined state of all merged work. If validation fails, it uses git bisect to identify which task's merge broke things, reverts that task's commits, and marks it as failed. This means a single bad task doesn't poison the entire run.

On startup, `retinue run` also auto-recovers stuck tasks from a previous crashed run. If the orchestrator died (laptop sleep, terminal closed), tasks that were `in_progress` with dead workers are detected and recovered — either marked done if the work was complete, or reset for re-dispatch.

### Recovering stuck tasks

If the orchestrator process dies mid-run (laptop sleeps, terminal closes, OOM), tasks can get stuck in `in_progress` with no live worker. The `reset` command detects and recovers these:

```bash
retinue reset           # dry run — show stuck tasks and what would happen
retinue reset --all     # recover all stuck in_progress tasks
retinue reset --task <id>  # recover a specific task
retinue reset --failed  # also reset failed tasks to pending
retinue reset --stale 1h  # only touch tasks stuck longer than 1 hour
retinue reset --force   # reset even if the tmux window is still alive
```

For stuck tasks with work on their branch, retinue uses an AI assessment to determine if the task actually completed before the orchestrator died. Complete work gets marked done (ready for merge), incomplete work gets marked failed (with context for retry), and broken work gets fully reset.

You don't usually need to run this manually — `retinue run` does it automatically at startup.

### Task lifecycle

Tasks move through: `pending` → `in_progress` → `done` → `merged` (or `failed`).

Check status at any time:

```bash
retinue status
```

The status table shows each task's ID, status, repo, token usage, and description. The TOKENS column displays input/output token counts and cost per task. Aggregate totals are printed at the bottom.

## Options

- `-w, --workspace <path>` — point to an apartment directory (default: current directory)

## Tasks schema

The `tasks.yaml` file defines all work items. Each task has these fields:

```yaml
tasks:
  - id: add-auth            # unique identifier
    description: Add JWT authentication to the API  # human-readable summary
    repo: api               # repository key (must match retinue.yaml)
    depends_on:              # task IDs that must complete first
      - setup-db
    status: pending          # pending | in_progress | done | merged | failed
    prompt: |                # detailed instructions for the worker agent
      Add JWT middleware to the Echo server.
      Use the golang-jwt/jwt/v5 library.
    artifacts:               # files this task will create or modify
      - internal/auth/jwt.go
      - internal/middleware/auth.go
    meta:                    # optional key-value metadata
      priority: high
```

The `branch` and `base_branch` fields are optional — retinue auto-generates the branch as `retinue/<task-id>` and defaults the base to `main`. The `result`, `error`, `started_at`, and `finished_at` fields are managed by retinue during execution.

For the full config and task schema reference, run:

```bash
retinue help config
```

## Telegram integration

Retinue can route Woland's communication through Telegram, so you can step away from your terminal and stay in the loop from your phone.

### Setup

```bash
retinue telegram setup
```

The interactive setup walks you through:

1. Creating a bot via [@BotFather](https://t.me/BotFather) in Telegram.
2. Pasting your bot token — the CLI validates it against the Telegram API.
3. Sending a message to your new bot so the CLI can detect your chat ID.
4. Saving the token and `chat_id` to `retinue.yaml`.

After setup, add the bot token to your shell profile:

```bash
export RETINUE_TELEGRAM_TOKEN="<your-bot-token>"
```

### Phone mode

When you're talking to Woland and need to step away, just say "stepping away" or type `/phone`. Woland switches to Telegram — sending responses and asking questions through your bot instead of the terminal.

When you're back at your desk, say "back" or type `/desk` (in Telegram or the terminal) and Woland switches back to the terminal session.

Outside of phone mode, Woland won't message you on Telegram unless background work has been running for a while and there's something important to report.
