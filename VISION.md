# Retinue — Vision

## Concept

You describe what you want built. Woland figures out how to do it, breaks it into tasks, and dispatches his retinue to carry out the work across your repositories.

## Theming (The Master and Margarita)

- **Woland** — the orchestrating agent. You talk to Woland, describe your goals, and he produces a plan: a DAG of tasks with dependencies, assigned to repos. He coordinates, re-plans when things fail, and keeps the big picture.
- **The Retinue** — the worker agents. Woland dispatches members of his retinue to execute tasks in isolated worktrees. They report back; Woland decides what's next. Candidate names for workers: Behemoth, Koroviev, Azazello, Hella — or simply numbered retinue members.
- **Apartment** (Apartment No. 50, Woland's headquarters on Sadovaya) — a workspace. The base of operations where Woland and his retinue work. An Apartment contains one or more repositories and a task graph. CLI accepts `--apartment` or `--apt`. (Replaces the current generic "workspace" concept.)

## Workflow

```
you  ──talk to──▶  Woland  ──dispatches──▶  Retinue (worker agents)
                     │                            │
                     ▼                            ▼
               task DAG + plan             isolated worktrees
                     │                            │
                     └──── reviews results ◀──────┘
```

1. **You talk to Woland.** Describe what you want in natural language. Woland asks clarifying questions if needed.
2. **Woland plans.** He explores the repos, understands the codebase, and produces a task DAG — what to do, in what order, in which repo.
3. **Woland dispatches.** Ready tasks are handed to retinue workers, each in its own git worktree/branch. Multiple workers run in parallel up to a configurable limit.
4. **Workers execute.** Each worker is a Claude Code agent scoped to a single task and repo. It writes code, runs tests, and reports results.
5. **Woland reviews.** As tasks complete, Woland checks results, unblocks downstream tasks, re-plans if something fails, and dispatches the next wave.
6. **You review.** Once Woland is satisfied (or at any point), you review branches, approve PRs, and merge.

## What exists today (Phase 1)

- CLI skeleton: `init`, `dispatch` (single task), `status`
- Task model with DAG validation, dependency resolution, YAML persistence
- Agent runner that invokes Claude Code
- Git worktree manager (built but not wired into dispatch)

## What's needed

- **Woland agent** — a conversational planning agent that takes your intent and produces a task DAG. This is the core missing piece.
- **Parallel dispatch** — use the worker pool / MaxWorkers config that's already stubbed.
- **Wire in worktrees** — dispatch should create worktrees so tasks run in isolation.
- **Feedback loop** — Woland reviews completed tasks and dispatches the next wave.
- **Rename workspace → apartment** throughout the codebase. CLI flag: `--apartment` / `--apt`.
- **CLI for the conversation** — something like `retinue talk` or `retinue woland` that starts the planning session.
