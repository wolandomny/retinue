# Standing Agents & Group Chat — Feature Spec

## What It Is

A live group chat between the user, Woland (the orchestrating AI), and any number of standing agents — each a persistent Claude Code session with a specific mandate. Think of it as a team chat where each member is an AI specialist that can read code, run commands, and act on their repos.

## The Cast

- **User** — the human, typing in Woland's terminal (or via Telegram when mobile)
- **Woland** — the hub, sees everything, coordinates everyone
- **Standing agents** — long-lived specialists defined in `agents.yaml`. Examples:
  - **Azazello** — CI watcher that monitors builds and alerts on failures
  - **Behemoth** — codebase gardener that tracks tech debt, runs linters, tidies up
  - **Tester** — a joke teller (the test agent)
  - Any user-defined agent with a custom prompt and mandate

## User Experience

### 1. Define an agent

User writes an entry in `agents.yaml`:
```yaml
agents:
  - id: azazello
    name: Azazello
    role: CI Watcher
    repos: [retinue]
    prompt: |
      You watch CI. When a build fails, investigate the logs,
      identify the root cause, and report to Woland with a fix suggestion.
    enabled: true
```

### 2. Start agents

```
$ retinue agent start azazello
Bus watcher started.
Agent "azazello" (Azazello) started.
```

The agent launches as a Claude Code session in a tmux window. The bus watcher starts automatically to bridge communication. The agent receives its system prompt and begins working according to its mandate.

### 3. Live conversation

From this point, the user talks naturally to Woland, who routes to agents with explicit markers:

```
> Hey, how's the CI looking?

→ azazello: What's the current CI status? Any recent failures?

[Azazello] CI is green across the board. Last 5 runs all passed.
The flaky test in auth_test.go hasn't fired in 3 days.

CI is looking clean — Azazello confirms all green, no flaky tests
recently.

> Nice. Any tech debt worth tackling?

→ behemoth: What's the current tech debt situation? Any low-hanging
fruit worth cleaning up?

[Behemoth] Found 3 TODO comments older than 6 months in the
handler package. Also, the retry logic in client.go is duplicated
in 3 places — worth extracting to a helper.

Behemoth found some cleanup opportunities — old TODOs and
duplicated retry logic. Want me to plan tasks for those?
```

User talks to Woland naturally. Woland routes to agents using → convention. Agents respond to Woland, who relays back to the user.

### 4. Routing rules

- **User → Woland**: All user messages go to Woland only
- **Woland → agents**: Woland explicitly routes using → convention
- **Agent → Woland**: Agent messages always go to Woland (the hub)
- **Agent → Agent**: No direct routes. Agent A asks Woland to relay.
- **Agent → User**: Via Woland. Agent responds to Woland, user sees Woland's terminal output.
- **No echoes**: Sender never receives its own message

### 5. Natural addressing

The user still talks naturally to Woland ("ask Azazello about CI"). Woland is the routing intelligence — it interprets user intent and explicitly routes to agents using the → marker:

- User: "Hey, how's the CI looking?" → Woland routes: "→ azazello: What's the current CI status? Any recent failures?"
- User: "Any tech debt worth tackling?" → Woland routes: "→ behemoth: What's the current tech debt situation? Any low-hanging fruit?"
- No special syntax needed from the user
- Woland handles the translation from natural language to explicit routing

### 6. Phone bridge

User steps away from the terminal:
```
> stepping away

Phone bridge active. You can close the terminal — I'll be on Telegram.
```

The conversation continues over Telegram. User messages arrive via the bot. Woland and agents keep working. When the user returns ("back"), the phone bridge shuts down.

### 7. Agent lifecycle

- `retinue agent start <id>` — launch an agent
- `retinue agent stop <id>` — kill an agent
- `retinue agent list` — see who's running
- Multiple agents can run simultaneously
- Bus watcher auto-starts with the first agent, auto-stops with the last

### 8. Agent capabilities

Each agent is a full Claude Code session. They can:
- Read and search code in their assigned repos
- Run shell commands
- Make code changes
- Respond to Woland's requests
- Act autonomously according to their mandate (e.g., watching CI on a schedule via heartbeat messages)

## What Should NOT Happen

- **No echoes**: An agent should never parrot back a message it received
- **No loops**: Agent A responds → Woland acknowledges → routed back to A → A responds → infinite loop
- **No phantom routing**: A message discussing agent A in passing (e.g., "the tester agent has a bug") should not be treated as a message TO agent A
- **No startup noise**: Messages shouldn't arrive before an agent is ready to process them
- **No message storms**: The system should gracefully handle rapid message exchange without flooding

## The Core Tension

The feature wants **natural language addressing** (just say the name) but needs **precise routing** (only deliver to the intended recipient). These goals conflict. Natural language is ambiguous — mentioning a name in conversation is not the same as addressing that agent.

The new design resolves this tension by separating concerns: the watcher is a dumb pipe with explicit To-based routing, while Woland is the routing intelligence that bridges natural language and precise delivery. Users speak naturally to Woland, who translates intent into explicit routing using the → convention. No heuristic matching needed.
