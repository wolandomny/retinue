# Group Chat Routing Redesign — Ultimate Design

## Problem Statement

The current bus watcher routes messages by scanning Woland's visible text output for agent name substrings. This causes echo loops, phantom routing, and requires layers of fragile prevention hacks (suppression windows, exchange limiters) that interact poorly and don't actually work.

The consolidated proposal (prefix matching + speaker tracking + `To` field with fallback) was reviewed by 5 independent critics. Their findings revealed fundamental flaws:

- Prefix matching breaks natural language ("Hey Azazello" fails silently)
- Silent routing failures are **worse** than echo bugs — user says something and nothing happens
- Speaker tracking blocks legitimate follow-ups and loses state on session restart
- Agent→user and agent→agent routing were undefined (showstoppers)
- The fallback-based design was still 6 concepts too many

## Core Insight

**Stop trying to make the watcher smart. Woland is an AI — let it be the sole routing intelligence.**

Every previous design put routing heuristics in the watcher daemon. Substring matching, prefix matching, first-line scoping — all attempts to make a process guess intent from natural language. That's literally what Woland does. The watcher should be a dumb pipe.

## Design Principles

1. **The watcher is a dumb pipe.** It delivers messages with explicit `To` fields. Nothing else.
2. **Woland is the sole routing intelligence.** It decides who receives what.
3. **Echo prevention is structural.** Sender never receives own message. No timing, no counters, no state.
4. **Push delivery stays.** tmux injection for immediate chat feel.
5. **Two concepts total.** `To` field on messages. `→ agent:` convention for Woland output.

## Architecture

### 1. Message Struct Gains Explicit Routing

```go
type Message struct {
    ID        string                 `json:"id"`
    Name      string                 `json:"name"`
    Type      MessageType            `json:"type"`
    Text      string                 `json:"text"`
    To        []string               `json:"to,omitempty"` // explicit recipients
    Timestamp time.Time              `json:"timestamp"`
    Meta      map[string]interface{} `json:"meta,omitempty"`
}
```

Semantics:
- `To: ["azazello"]` — deliver to Azazello only
- `To: ["azazello", "behemoth"]` — deliver to both
- `To: nil` or `To: []` — deliver to nobody (except Woland, who always sees everything)

**No fallback matching.** Empty `To` means no agent routing.

### 2. Message Flow

```
User types in Woland's terminal
  → Watcher reads it from Woland's session JSONL
  → Watcher creates message with To: ["woland"]
  → Message goes on bus (Woland already saw it in his terminal)

Woland responds, wants to talk to Azazello
  → Woland writes: "→ azazello: Check the CI logs"
  → Watcher reads Woland's output from session JSONL
  → Watcher parses → convention, sets To: ["azazello"]
  → Watcher injects into Azazello's tmux session

Azazello responds
  → Watcher reads from Azazello's session JSONL
  → Watcher creates message with To: ["woland"]
  → Watcher injects into Woland's tmux session

Woland relays to user
  → User sees Woland's terminal output directly (no routing needed)
```

### 3. Complete Routing Table

| From | To | How `To` is set | Delivery mechanism |
|------|----|-----------------|-------------------|
| User → Woland | Always | Watcher hardcodes `To: ["woland"]` | Already in Woland's terminal |
| Woland → Agent | Explicit | Woland writes `→ agent:` prefix | tmux injection into agent |
| Woland → User | Always | No routing needed | User reads Woland's terminal |
| Agent → Woland | Always | Watcher hardcodes `To: ["woland"]` | tmux injection into Woland |
| Agent → User | Via Woland | Agent tells Woland; Woland shows it | Woland's terminal output |
| Agent → Agent | Via Woland | Agent A → Woland → `→ agentB:` | Woland relays explicitly |

### 4. The Routing Function (~15 lines)

```go
func (w *Watcher) routeMessage(msg Message) []injectionWindow {
    if msg.Type == TypeSystem {
        return nil
    }

    var targets []injectionWindow
    for _, win := range w.windows {
        // Never echo to sender.
        if win.busName == msg.Name {
            continue
        }

        // Woland always sees everything (hub).
        if win.isWoland {
            targets = append(targets, win)
            continue
        }

        // Everyone else: explicit To only.
        for _, recipient := range msg.To {
            if strings.EqualFold(recipient, win.busName) {
                targets = append(targets, win)
                break
            }
        }
    }
    return targets
}
```

That's the entire routing logic. No matching heuristics. No timing. No state tracking.

### 5. The `→ agent:` Convention

Woland signals routing with a first-line marker:

```
→ azazello: Check the CI logs for the last failure
```

The watcher parses this:
- Extracts `To: ["azazello"]`
- Strips the `→ azazello:` prefix from the delivered text
- Delivers "Check the CI logs for the last failure" to Azazello

Multi-agent addressing:
```
→ azazello, behemoth: Coordinate on the retry logic cleanup
```

Parsing function:

```go
// parseArrowRouting extracts explicit routing from Woland's output.
// Format: "→ agent1, agent2: message text"
// Returns (recipients, stripped text, ok).
func parseArrowRouting(text string) ([]string, string, bool) {
    firstLine := firstLineOf(text)
    trimmed := strings.TrimSpace(firstLine)

    if !strings.HasPrefix(trimmed, "→ ") {
        return nil, "", false
    }

    // Find the colon separator
    colonIdx := strings.IndexByte(trimmed, ':')
    if colonIdx < 0 {
        return nil, "", false
    }

    // Extract agent names between "→ " and ":"
    namesStr := strings.TrimSpace(trimmed[len("→ "):colonIdx])
    if namesStr == "" {
        return nil, "", false
    }

    // Split on comma, trim each name
    parts := strings.Split(namesStr, ",")
    var recipients []string
    for _, p := range parts {
        name := strings.TrimSpace(p)
        if name != "" {
            recipients = append(recipients, strings.ToLower(name))
        }
    }

    if len(recipients) == 0 {
        return nil, "", false
    }

    // Strip the → line from the text, keep the rest
    messageText := strings.TrimSpace(trimmed[colonIdx+1:])
    if rest := strings.TrimPrefix(text, firstLine); rest != "" {
        messageText += rest // preserve subsequent lines
    }

    return recipients, messageText, true
}

func firstLineOf(text string) string {
    if i := strings.IndexByte(text, '\n'); i >= 0 {
        return text[:i]
    }
    return text
}
```

### 6. Message Ingestion

```go
// User messages: always to Woland only.
func (w *Watcher) ingestUserMessage(text string) Message {
    msg := NewMessage("user", TypeUser, text)
    msg.To = []string{"woland"}
    return msg
}

// Agent messages: always to Woland only.
func (w *Watcher) ingestAgentMessage(agentID, text string) Message {
    msg := NewMessage(agentID, TypeChat, text)
    msg.To = []string{"woland"}
    return msg
}

// Woland messages: parse → convention for explicit routing.
func (w *Watcher) ingestWolandMessage(text string) Message {
    msg := NewMessage("woland", TypeChat, text)

    if recipients, stripped, ok := parseArrowRouting(text); ok {
        msg.To = recipients
        msg.Text = stripped
    }
    // No → prefix = no agent routing. Only user sees it (via terminal).

    return msg
}
```

## What Gets Deleted

All loop-prevention hacks (~200 lines):
- `responseSuppressWindow` constant (10 seconds)
- `maxExchangesPerTurn` constant (2)
- `lastInjectedToWoland` map and all timestamp tracking
- `exchangeCount` map and all counter logic
- The exchange reset logic (selective or otherwise)
- The suppression window check in `injectMessage()`
- `injectionTargets()` function (replaced by `routeMessage()`)
- All substring matching logic
- Any prefix matching / `isDirectlyAddressed()` if present

## What Gets Added

~60 lines of clean, testable code:
- `To []string` field on Message struct
- `routeMessage()` function (~15 lines)
- `parseArrowRouting()` function (~25 lines)
- `firstLineOf()` helper (~5 lines)
- Ingestion functions: `ingestUserMessage()`, `ingestAgentMessage()`, `ingestWolandMessage()` (~15 lines)

## What Changes

- `injectMessage()`: Gutted. Remove ~100 lines of loop prevention. Call `routeMessage()` instead of `injectionTargets()`. No state tracking needed.
- `readAgentLines()`: Use ingestion functions to populate `To` field on all messages.
- Watcher struct: Remove `lastInjectedToWoland`, `exchangeCount`, `spokenInExchange` (if present). No new state fields.

## Why No Fallback Matching

The consolidated proposal included prefix matching as a fallback for when `To` is empty. Every reviewer identified problems with this:

1. **Prefix matching breaks natural language** — "Hey Azazello" fails, "So Azazello," fails, any non-name-first phrasing fails. These are silent failures the user can't see or debug.

2. **Silent failures are worse than echo bugs** — With echoes, you see the problem and can fix it. With silent routing failures, the user says something and nothing happens. They have no idea why.

3. **Two routing paths = two failure modes** — The fallback creates ambiguity about which path was taken and why. Single path is simpler to reason about and debug.

4. **Woland is an AI** — It can trivially learn the `→` convention. There's no human ergonomics argument for "just say the name" when the speaker is an LLM that follows instructions perfectly.

## Why No Speaker Tracking

The consolidated proposal included a `spokenInExchange` map to prevent routing messages back to agents that already spoke. This was critiqued because:

1. **It blocks legitimate follow-ups** — Woland asks Azazello a question, Azazello answers, Woland has a follow-up → blocked.

2. **Session restart loses state** — The map is in-memory. Watcher restart = state gone = different behavior.

3. **It's unnecessary with explicit routing** — If Woland explicitly sets `To: ["azazello"]`, there's no echo path. The watcher doesn't guess, so there's nothing to prevent.

Echo prevention is structural:
- Sender never receives own message (`win.busName == msg.Name` check)
- Agents only receive messages with explicit `To` containing their name
- No heuristic matching = no false positives = no echoes to prevent

## Complexity Comparison

| Aspect | Current | Consolidated Proposal | Ultimate Design |
|--------|---------|----------------------|-----------------|
| Routing logic | ~50 lines substring | ~30 lines To + prefix fallback | ~15 lines To-only |
| Loop prevention | ~100 lines hacks | ~10 lines speaker tracking | 0 lines (structural) |
| State tracking | 2 maps + timestamps + counters | 1 map (speakers) | 0 maps |
| Concepts | 3 (substring, windows, counters) | 6 (To, prefix, first-line, speaker, fallback, arrow) | 2 (To, arrow) |
| Constants | 2 magic numbers | 0 | 0 |
| Failure modes | Timing races, counter bugs | Silent routing failures, state loss | Agent name typo in → (visible, fixable) |
| Testability | Time mocking, complex scenarios | Moderate | Trivial: To=[x] → delivers to x |

## Woland System Prompt Changes

Replace the current "just say their name" routing instruction with:

```
When you want to send a message to a standing agent, start your
response with the → marker:

→ azazello: Your message here

To address multiple agents:
→ azazello, behemoth: Your message here

Messages without the → marker are only visible to the user in the terminal.
Agents cannot see your output unless you explicitly route to them.
```

## Implementation Plan

Single phase — no incremental migration needed:

1. Add `To []string` field to Message struct
2. Add `parseArrowRouting()` and `firstLineOf()` functions
3. Replace `injectionTargets()` with `routeMessage()`
4. Update `readAgentLines()` to use ingestion functions (set `To` on all messages)
5. Gut `injectMessage()` — remove all loop prevention hacks
6. Remove all dead code (suppression windows, exchange counters, etc.)
7. Update Watcher struct — remove state tracking fields
8. Rewrite tests to match new routing semantics
9. Update Woland's system prompt with `→` convention

## Open Questions

1. **Should the watcher inject user messages into Woland?** Currently it does, but Woland already sees user input in his own terminal. Injecting may cause duplicates. Consider: user messages go on bus for agent routing only, but don't get injected into Woland. This needs investigation of current behavior.

2. **Phone bridge interaction**: The Telegram bridge injects user messages via tmux. These should be treated the same as terminal input — `To: ["woland"]` only. Verify this path works with the new design.

3. **`→` rendering in terminal**: The arrow character `→` should render correctly in all terminal emulators. If not, consider `>` or `@` as alternatives. Test before shipping.
