package bus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/wolandomny/retinue/internal/session"
	"github.com/wolandomny/retinue/internal/shell"
)

const (
	// discoverInterval is how often the watcher polls for new/removed agent windows.
	discoverInterval = 3 * time.Second

	// agentPollInterval is how often each agent output watcher checks for new content.
	agentPollInterval = 1500 * time.Millisecond

	// agentStartupWindow is how far back from end-of-file we seek when first
	// discovering an agent's session file (to drain for dedup, not forward).
	agentStartupWindow = 4096

	// watcherStaleness is how long the watcher tolerates no new content from a
	// session file before attempting to re-discover via the marker file.
	watcherStaleness = 30 * time.Second

	// responseSuppressWindow is how long after an agent's message is injected
	// into Woland before we suppress routing Woland's response back to that
	// agent. This prevents the immediate bounce-back (Woland acknowledging
	// an agent's message being routed back to the agent).
	responseSuppressWindow = 10 * time.Second

	// maxExchangesPerTurn is the maximum number of times Woland can route a
	// message to a given agent between user messages. This is a safety net
	// against infinite Woland↔Agent ping-pong loops.
	maxExchangesPerTurn = 2
)

// injectedMessagePattern matches messages injected by the bus watcher via tmux
// send-keys. These use the format "[Name] message text" as produced by
// FormatForInjection in format.go. We must filter these out when propagating
// "human" messages from Woland's session to avoid creating a feedback loop.
var injectedMessagePattern = regexp.MustCompile(`^\[.+\] `)

// isInjectedMessage returns true if the text looks like a bus-injected message
// (matches the "[Name] text" format produced by FormatForInjection).
func isInjectedMessage(text string) bool {
	return injectedMessagePattern.MatchString(text)
}

// sessionMessage represents a JSONL line from Claude Code's session file.
type sessionMessage struct {
	Type    string         `json:"type"`
	UUID    string         `json:"uuid"`
	Message messageContent `json:"message"`
}

// messageContent is the "message" field within a session message.
type messageContent struct {
	Content []contentBlock `json:"content"`
}

// contentBlock is a single content block within a message.
type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// monitoredWindow describes a tmux window that the bus watcher should monitor.
type monitoredWindow struct {
	busName    string // identity on the bus (e.g. "azazello", "woland")
	windowName string // tmux window name (e.g. "agent-azazello", "woland")
	isWoland   bool   // true for woland/babytalk windows
}

// wolandWindowNames are tmux window names that belong to Woland.
var wolandWindowNames = map[string]bool{
	"woland":   true,
	"babytalk": true,
}

// agentWatcher tracks per-agent output monitoring state.
type agentWatcher struct {
	cancel     context.CancelFunc
	done       chan struct{}
	busName    string // identity on the bus
	windowName string // tmux window name
	isWoland   bool   // true for woland/babytalk windows
}

// Watcher is the bus watcher daemon. It bridges the message bus with running
// standing agent sessions:
//   - Bus → Agents: tails the bus, injects new messages into agent tmux sessions
//   - Agents → Bus: watches each agent's Claude session JSONL, writes to the bus
type Watcher struct {
	bus        *Bus
	tmuxSocket string
	aptPath    string
	logger     *log.Logger

	mu       sync.Mutex
	watchers map[string]*agentWatcher // windowName → watcher

	// Loop prevention state (protected by mu).

	// lastInjectedToWoland tracks when each agent's message was last injected
	// into Woland's window. Used to suppress routing Woland's immediate
	// acknowledgment back to the originating agent.
	lastInjectedToWoland map[string]time.Time // agent busName → timestamp

	// exchangeCount tracks how many times Woland has routed a message to each
	// agent since the last user message. Capped at maxExchangesPerTurn.
	exchangeCount map[string]int // agent busName → count

	// timeNow is a function that returns the current time. It defaults to
	// time.Now but can be overridden in tests.
	timeNow func() time.Time
}

// NewWatcher creates a Watcher that bridges the given bus with agent sessions.
func NewWatcher(b *Bus, tmuxSocket, aptPath string, logger *log.Logger) *Watcher {
	return &Watcher{
		bus:                  b,
		tmuxSocket:           tmuxSocket,
		aptPath:              aptPath,
		logger:               logger,
		watchers:             make(map[string]*agentWatcher),
		lastInjectedToWoland: make(map[string]time.Time),
		exchangeCount:        make(map[string]int),
		timeNow:              time.Now,
	}
}

// Run starts the bus watcher daemon and blocks until ctx is cancelled.
func (w *Watcher) Run(ctx context.Context) error {
	// Start tailing the bus for new messages.
	busCh := w.bus.Tail(ctx)

	// Ticker for periodic agent discovery.
	ticker := time.NewTicker(discoverInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.stopAllWatchers()
			return nil

		case msg, ok := <-busCh:
			if !ok {
				w.stopAllWatchers()
				return fmt.Errorf("bus tail channel closed")
			}
			w.injectMessage(ctx, *msg)

		case <-ticker.C:
			w.discoverAgents(ctx)
		}
	}
}

// discoverAgents polls tmux for running agent and Woland windows and
// starts/stops output watchers as needed.
func (w *Watcher) discoverAgents(ctx context.Context) {
	windows, err := w.listMonitoredWindows(ctx)
	if err != nil {
		w.logger.Printf("error listing monitored windows: %v", err)
		return
	}

	active := make(map[string]bool, len(windows))
	for _, win := range windows {
		active[win.windowName] = true
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	// Start watchers for new windows.
	for _, win := range windows {
		if _, exists := w.watchers[win.windowName]; !exists {
			// Gate non-Woland agent windows on their session marker file.
			// The marker (.agent-<id>-session) is written by `retinue agent start`
			// only after the Claude session is fully initialized. Without this
			// check, the bus watcher can route messages to an agent before it's
			// ready, causing echo loops.
			if !win.isWoland && !w.agentMarkerExists(win.busName) {
				w.logger.Printf("skipping agent %q: session marker not yet present (agent still starting)", win.busName)
				continue
			}
			w.startMonitoredWatcher(ctx, win)
		}
	}

	// Stop watchers for windows that are no longer running.
	for windowName, aw := range w.watchers {
		if !active[windowName] {
			w.logger.Printf("%q window %q disappeared, stopping output watcher", aw.busName, windowName)
			aw.cancel()
			<-aw.done
			delete(w.watchers, windowName)
		}
	}
}

// agentMarkerExists returns true if the session marker file for the given
// agent ID exists in the apartment directory. The marker file is written by
// `retinue agent start` after the agent's Claude session is fully initialized.
func (w *Watcher) agentMarkerExists(agentID string) bool {
	markerPath := filepath.Join(w.aptPath, fmt.Sprintf(".agent-%s-session", agentID))
	_, err := os.Stat(markerPath)
	return err == nil
}

// listMonitoredWindows returns the monitored windows extracted from tmux,
// including both agent windows (prefixed "agent-") and Woland windows
// ("woland", "babytalk").
func (w *Watcher) listMonitoredWindows(ctx context.Context) ([]monitoredWindow, error) {
	args := w.tmuxArgs("list-windows", "-t", "retinue", "-F", "#{window_name}")
	cmd := exec.CommandContext(ctx, "tmux", args...)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return nil, nil // session doesn't exist
		}
		return nil, fmt.Errorf("tmux list-windows: %w", err)
	}

	return parseMonitoredWindows(string(out)), nil
}

// parseMonitoredWindows extracts monitored windows from tmux list-windows
// output. It includes agent windows (prefixed "agent-") and Woland windows
// ("woland", "babytalk"). Agent windows use the portion after "agent-" as
// the bus identity; Woland windows always use "woland" as the bus identity.
func parseMonitoredWindows(tmuxOutput string) []monitoredWindow {
	var windows []monitoredWindow
	for _, line := range strings.Split(strings.TrimSpace(tmuxOutput), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "agent-") {
			id := strings.TrimPrefix(line, "agent-")
			if id != "" {
				windows = append(windows, monitoredWindow{
					busName:    id,
					windowName: line,
				})
			}
		} else if wolandWindowNames[line] {
			windows = append(windows, monitoredWindow{
				busName:    "woland",
				windowName: line,
				isWoland:   true,
			})
		}
	}
	return windows
}

// startMonitoredWatcher begins monitoring a tmux window's Claude session
// JSONL file. Works for both agent windows and Woland windows.
// Must be called with w.mu held.
func (w *Watcher) startMonitoredWatcher(ctx context.Context, win monitoredWindow) {
	childCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	aw := &agentWatcher{
		cancel:     cancel,
		done:       done,
		busName:    win.busName,
		windowName: win.windowName,
		isWoland:   win.isWoland,
	}
	w.watchers[win.windowName] = aw

	// Snapshot the session file at the time the window appears.
	var sessionFile string
	if win.isWoland {
		sessionFile = w.findWolandSessionFile()
	} else {
		sessionFile = w.findAgentSessionFile(win.busName)
	}

	w.logger.Printf("starting output watcher for %q (window: %s, session: %s)", win.busName, win.windowName, filepath.Base(sessionFile))

	go func() {
		defer close(done)
		w.watchAgentOutput(childCtx, win.busName, sessionFile, win.isWoland)
	}()
}

// findAgentSessionFile returns the session file for the given agent. It first
// checks for a marker file (.agent-<id>-session) in the apartment directory.
// If the marker exists and points to a valid, fresh file, that file is returned.
// If the marker target is stale (not modified within watcherStaleness), it falls
// through to the newest .jsonl file. Otherwise it falls back to the newest .jsonl
// file in the Claude projects directory (legacy behavior).
func (w *Watcher) findAgentSessionFile(agentID string) string {
	// Try marker file first.
	markerPath := filepath.Join(w.aptPath, fmt.Sprintf(".agent-%s-session", agentID))
	if data, err := os.ReadFile(markerPath); err == nil {
		sessionPath := strings.TrimSpace(string(data))
		if sessionPath != "" {
			info, err := os.Stat(sessionPath)
			if err != nil {
				w.logger.Printf("agent %q marker points to missing file, falling back", agentID)
			} else if time.Since(info.ModTime()) > watcherStaleness {
				// Marker target is stale — try to find a newer file.
				w.logger.Printf("agent %q marker target %s is stale (modified %s ago), checking for newer session",
					agentID, filepath.Base(sessionPath), time.Since(info.ModTime()).Round(time.Second))
				newest := session.NewestJSONLFile(session.ClaudeProjectDir(w.aptPath))
				if newest != "" && newest != sessionPath {
					w.logger.Printf("agent %q: found newer session file %s, ignoring stale marker", agentID, filepath.Base(newest))
					return newest
				}
				// No better file; return the stale marker target.
				return sessionPath
			} else {
				return sessionPath
			}
		}
	}

	// Fallback: newest file (legacy behavior, but log a warning).
	w.logger.Printf("no session marker for agent %q, using newest JSONL (may be wrong)", agentID)
	return w.findNewestJSONL(session.ClaudeProjectDir(w.aptPath))
}

// claudeProjectDir derives the Claude Code projects directory from an
// apartment path. Delegates to the shared session.ClaudeProjectDir.
func claudeProjectDir(aptPath string) string {
	return session.ClaudeProjectDir(aptPath)
}

// findWolandSessionFile reads the .woland-session marker file to locate
// Woland's active Claude session JSONL. Falls back to the newest JSONL
// in the Claude projects directory if the marker is absent or stale.
func (w *Watcher) findWolandSessionFile() string {
	markerPath := filepath.Join(w.aptPath, ".woland-session")
	data, err := os.ReadFile(markerPath)
	if err != nil {
		w.logger.Printf("no .woland-session marker found, falling back to newest JSONL")
		return w.findAgentSessionFile("woland")
	}
	sessionPath := strings.TrimSpace(string(data))
	if sessionPath == "" {
		return w.findAgentSessionFile("woland")
	}
	// Verify the file exists and log diagnostics.
	info, err := os.Stat(sessionPath)
	if err != nil {
		w.logger.Printf(".woland-session marker points to missing file %q, falling back", sessionPath)
		return w.findAgentSessionFile("woland")
	}
	w.logger.Printf("found .woland-session marker pointing to %s", filepath.Base(sessionPath))
	w.logger.Printf("marker file modified %s ago", time.Since(info.ModTime()).Round(time.Second))

	// If the marker target hasn't been modified within watcherStaleness,
	// it's stale — fall through to the newest JSONL file instead of
	// blindly trusting the marker.
	if time.Since(info.ModTime()) > watcherStaleness {
		w.logger.Printf(".woland-session marker target %s is stale (modified %s ago, threshold %s), falling back to newest JSONL",
			filepath.Base(sessionPath), time.Since(info.ModTime()).Round(time.Second), watcherStaleness)
		newest := session.NewestJSONLFile(session.ClaudeProjectDir(w.aptPath))
		if newest != "" && newest != sessionPath {
			w.logger.Printf("found newer session file %s, ignoring stale marker", filepath.Base(newest))
			return newest
		}
		// No better file found; return the stale one as last resort.
		w.logger.Printf("no newer session file found, using stale marker target")
	}

	return sessionPath
}

// watchAgentOutput tails an agent's (or Woland's) Claude session JSONL file
// and writes extracted assistant messages to the bus.
func (w *Watcher) watchAgentOutput(ctx context.Context, agentID, sessionFile string, isWoland bool) {
	seen := make(map[string]bool)
	var partialLine string
	var offset int64

	// Wait for a session file if none was found at startup.
	if sessionFile == "" {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(agentPollInterval):
			}
			if isWoland {
				sessionFile = w.findWolandSessionFile()
			} else {
				sessionFile = w.findAgentSessionFile(agentID)
			}
			if sessionFile != "" {
				w.logger.Printf("%q: found session file %s", agentID, filepath.Base(sessionFile))
				break
			}
		}
	}

	// Seek to near the end of the file for startup drain.
	info, err := os.Stat(sessionFile)
	if err == nil {
		offset = info.Size() - agentStartupWindow
		if offset < 0 {
			offset = 0
		}
		if offset > 0 {
			adjusted, err := seekToLineStart(sessionFile, offset)
			if err == nil {
				offset = adjusted
			} else {
				offset = info.Size()
			}
		}
	}

	// Drain: parse existing lines for dedup but don't write to bus.
	offset, partialLine = w.readAgentLines(ctx, agentID, sessionFile, offset, partialLine, seen, true, isWoland)

	lastNewContent := time.Now()

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(agentPollInterval):
		}

		// Check if a newer session file has appeared (the agent/Woland
		// may have restarted its Claude session).
		var newest string
		if isWoland {
			newest = w.findWolandSessionFile()
		} else {
			newest = w.findAgentSessionFile(agentID)
		}
		if newest != "" && newest != sessionFile {
			w.logger.Printf("%q: session file changed to %s", agentID, filepath.Base(newest))
			sessionFile = newest
			offset = 0
			partialLine = ""
			lastNewContent = time.Now()
			// Drain the new file.
			offset, partialLine = w.readAgentLines(ctx, agentID, sessionFile, offset, partialLine, seen, true, isWoland)
			continue
		}

		prevOffset := offset
		offset, partialLine = w.readAgentLines(ctx, agentID, sessionFile, offset, partialLine, seen, false, isWoland)

		if offset != prevOffset {
			lastNewContent = time.Now()
		}

		// Staleness detection: if no new content for watcherStaleness and the
		// file's ModTime is also older than watcherStaleness, attempt to
		// re-discover the session file via the marker.
		if time.Since(lastNewContent) > watcherStaleness {
			if info, err := os.Stat(sessionFile); err != nil || time.Since(info.ModTime()) > watcherStaleness {
				w.logger.Printf("%q: session file %s appears stale (no new content for %s), re-discovering",
					agentID, filepath.Base(sessionFile), time.Since(lastNewContent).Round(time.Second))
				var rediscovered string
				if isWoland {
					rediscovered = w.findWolandSessionFile()
				} else {
					rediscovered = w.findAgentSessionFile(agentID)
				}
				if rediscovered != "" && rediscovered != sessionFile {
					w.logger.Printf("%q: re-discovered session file %s", agentID, filepath.Base(rediscovered))
					sessionFile = rediscovered
					offset = 0
					partialLine = ""
					lastNewContent = time.Now()
					offset, partialLine = w.readAgentLines(ctx, agentID, sessionFile, offset, partialLine, seen, true, isWoland)
				} else {
					// Re-discovery returned the same stale file (or empty).
					// The marker is stale and points back to the same file.
					// Bypass markers entirely and try newest JSONL directly.
					projDir := session.ClaudeProjectDir(w.aptPath)
					newest := session.NewestJSONLFile(projDir)
					if newest != "" && newest != sessionFile {
						w.logger.Printf("%q: marker returned same stale file, bypassing marker — switching to newest JSONL %s",
							agentID, filepath.Base(newest))
						sessionFile = newest
						offset = 0
						partialLine = ""
						lastNewContent = time.Now()
						offset, partialLine = w.readAgentLines(ctx, agentID, sessionFile, offset, partialLine, seen, true, isWoland)

						// Update the marker so subsequent lookups benefit.
						var markerName string
						if isWoland {
							markerName = ".woland-session"
						} else {
							markerName = fmt.Sprintf(".agent-%s-session", agentID)
						}
						markerPath := filepath.Join(w.aptPath, markerName)
						if err := os.WriteFile(markerPath, []byte(newest), 0o644); err != nil {
							w.logger.Printf("%q: failed to update marker %s: %v", agentID, markerName, err)
						} else {
							w.logger.Printf("%q: updated marker %s → %s", agentID, markerName, filepath.Base(newest))
						}
					} else {
						// Reset timer to avoid spamming re-discovery.
						lastNewContent = time.Now()
					}
				}
			}
		}
	}
}

// findNewestJSONL returns the most recently modified .jsonl file in the given directory.
// Delegates to the shared session.NewestJSONLFile.
func (w *Watcher) findNewestJSONL(dir string) string {
	return session.NewestJSONLFile(dir)
}

// seekToLineStart finds the first complete line boundary at or after offset.
func seekToLineStart(path string, offset int64) (int64, error) {
	if offset <= 0 {
		return 0, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return offset, err
	}
	defer f.Close()

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return offset, err
	}

	buf := make([]byte, 4096)
	n, err := f.Read(buf)
	if n == 0 {
		return offset, err
	}

	idx := bytes.IndexByte(buf[:n], '\n')
	if idx == -1 {
		return offset + int64(n), nil
	}
	return offset + int64(idx) + 1, nil
}

// readAgentLines reads new lines from the agent's session file and writes
// extracted assistant messages to the bus (unless draining). When isWoland
// is true, genuine "human" messages (not bus-injected) are also propagated
// to the bus as TypeUser, enabling exchange counter resets.
func (w *Watcher) readAgentLines(ctx context.Context, agentID, path string, offset int64, partialLine string, seen map[string]bool, draining bool, isWoland bool) (int64, string) {
	info, err := os.Stat(path)
	if err != nil {
		return offset, partialLine
	}

	// Handle truncation.
	if info.Size() < offset {
		offset = 0
		partialLine = ""
	}

	if info.Size() == offset {
		return offset, partialLine
	}

	f, err := os.Open(path)
	if err != nil {
		return offset, partialLine
	}
	defer f.Close()

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return offset, partialLine
	}

	newData, err := io.ReadAll(f)
	if err != nil {
		return offset, partialLine
	}

	bytesRead := int64(len(newData))

	var data []byte
	if partialLine != "" {
		data = make([]byte, len(partialLine)+len(newData))
		copy(data, partialLine)
		copy(data[len(partialLine):], newData)
		partialLine = ""
	} else {
		data = newData
	}

	pos := 0
	for pos < len(data) {
		nlIdx := bytes.IndexByte(data[pos:], '\n')
		if nlIdx == -1 {
			partialLine = string(data[pos:])
			break
		}

		line := string(data[pos : pos+nlIdx])
		pos += nlIdx + 1

		select {
		case <-ctx.Done():
			return offset + bytesRead, partialLine
		default:
		}

		msgType, text, uuid, ok := parseSessionLineTyped(line)
		if !ok {
			continue
		}

		if uuid != "" && seen[uuid] {
			continue
		}
		if uuid != "" {
			seen[uuid] = true
		}

		if text == "" || draining {
			continue
		}

		switch msgType {
		case "assistant":
			if err := w.bus.Append(NewMessage(agentID, TypeChat, text)); err != nil {
				w.logger.Printf("error writing agent %q message to bus: %v", agentID, err)
			}
		case "human":
			// Only propagate human messages from Woland's session.
			// Agent "human" messages are bus-injected content, not real user input.
			if !isWoland {
				continue
			}
			// Filter out bus-injected messages (format: "[Name] text").
			if isInjectedMessage(text) {
				continue
			}
			if err := w.bus.Append(NewMessage("user", TypeUser, text)); err != nil {
				w.logger.Printf("error writing user message from Woland session to bus: %v", err)
			}
		}
	}

	return offset + bytesRead, partialLine
}

// parseSessionLine parses a single JSONL line and extracts text from
// assistant messages. Returns the concatenated text, UUID, and success.
func parseSessionLine(line string) (text string, uuid string, ok bool) {
	_, text, uuid, ok = parseSessionLineTyped(line)
	return text, uuid, ok
}

// parseSessionLineTyped parses a single JSONL line and extracts the session
// message type, text content, UUID, and success. It recognizes both "assistant"
// and "human" message types. Other types return ok=true but empty fields.
func parseSessionLineTyped(line string) (msgType string, text string, uuid string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", "", "", false
	}

	var msg sessionMessage
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		return "", "", "", false
	}

	if msg.Type != "assistant" && msg.Type != "human" {
		return msg.Type, "", "", true // valid JSON but not a type we extract text from
	}

	var parts []string
	for _, block := range msg.Message.Content {
		if block.Type == "text" && block.Text != "" {
			parts = append(parts, block.Text)
		}
	}

	if len(parts) == 0 {
		return msg.Type, "", msg.UUID, true
	}

	return msg.Type, strings.Join(parts, "\n"), msg.UUID, true
}

// injectionWindow pairs a bus identity with its tmux window name for
// message injection targeting.
type injectionWindow struct {
	busName    string // identity on the bus (used for sender filtering)
	windowName string // tmux window name (used as tmux target)
	isWoland   bool   // true for woland/babytalk windows (hub: receives all messages)
}

// injectMessage sends a bus message to all active monitored tmux sessions
// except the sender.
func (w *Watcher) injectMessage(ctx context.Context, msg Message) {
	// Don't inject system messages into agent sessions.
	if msg.Type == TypeSystem {
		return
	}

	formatted := FormatForInjection(&msg)
	escaped := shell.EscapeTmux(formatted)

	now := w.timeNow()

	w.mu.Lock()

	// When a user message arrives, reset all exchange counts so that
	// Woland can address agents again.
	if msg.Type == TypeUser {
		for k := range w.exchangeCount {
			delete(w.exchangeCount, k)
		}
	}

	windows := make([]injectionWindow, 0, len(w.watchers))
	for _, aw := range w.watchers {
		windows = append(windows, injectionWindow{
			busName:    aw.busName,
			windowName: aw.windowName,
			isWoland:   aw.isWoland,
		})
	}

	// Compute base routing targets (pure function, no loop-prevention).
	baseTargets := injectionTargets(windows, msg)

	// Apply loop prevention: filter Woland→Agent routing through the
	// suppression window and exchange limiter. User messages bypass all
	// loop prevention entirely.
	var targets []injectionWindow
	for _, t := range baseTargets {
		if t.isWoland {
			// Hub rule: Woland always receives all messages.
			// When injecting into Woland from a standing agent, record
			// the timestamp for suppression window tracking.
			if msg.Type != TypeUser && msg.Type != TypeSystem && msg.Name != "woland" {
				w.lastInjectedToWoland[msg.Name] = now
			}
			targets = append(targets, t)
			continue
		}

		// For agent targets: if this is a Woland→Agent route, check
		// loop prevention. User messages bypass all checks.
		if msg.Name == "woland" && msg.Type != TypeUser {
			// Layer 1: suppression window.
			if lastInj, ok := w.lastInjectedToWoland[t.busName]; ok {
				if now.Sub(lastInj) < responseSuppressWindow {
					w.logger.Printf("suppressing Woland→%s route: agent message injected %s ago (within %s window)",
						t.busName, now.Sub(lastInj).Round(time.Millisecond), responseSuppressWindow)
					continue
				}
			}

			// Layer 2: exchange limiter.
			if w.exchangeCount[t.busName] >= maxExchangesPerTurn {
				w.logger.Printf("suppressing Woland→%s route: exchange limit reached (%d/%d)",
					t.busName, w.exchangeCount[t.busName], maxExchangesPerTurn)
				continue
			}

			// Routing allowed — increment exchange count.
			w.exchangeCount[t.busName]++
		}

		targets = append(targets, t)
	}
	w.mu.Unlock()

	if len(targets) > 0 {
		names := make([]string, len(targets))
		for i, t := range targets {
			names[i] = t.busName
		}
		w.logger.Printf("routing message from %q to %d targets: %v", msg.Name, len(targets), names)
	}

	for _, t := range targets {
		target := fmt.Sprintf("retinue:%s", t.windowName)
		args := w.tmuxArgs("send-keys", "-t", target, "--", escaped, "Enter")

		cmd := exec.CommandContext(ctx, "tmux", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			w.logger.Printf("error injecting message to %q (window %s): %v: %s", t.busName, t.windowName, err, string(out))
		}
	}
}

// injectionTargets returns the subset of monitored windows that should
// receive a given message. It uses hub-and-spoke routing:
//   - System messages are never injected.
//   - The sender never receives its own message (echo prevention).
//   - Woland (the hub/orchestrator) always receives all messages.
//   - Standing agents only receive messages from the user or from Woland
//     that mention their busName (case-insensitive substring match).
//   - Agent-to-agent communication always goes through Woland (the hub),
//     preventing echo loops where agent A mentions agent B, B responds
//     mentioning A, and so on.
func injectionTargets(windows []injectionWindow, msg Message) []injectionWindow {
	if msg.Type == TypeSystem {
		return nil
	}

	textLower := strings.ToLower(msg.Text)
	var targets []injectionWindow

	for _, w := range windows {
		// Never echo to sender.
		if w.busName == msg.Name {
			continue
		}
		// Woland always sees everything (he's the orchestrator/hub).
		if w.isWoland {
			targets = append(targets, w)
			continue
		}
		// Standing agents only receive messages from user or Woland
		// that mention their name. Agent-to-agent goes through Woland.
		if msg.Type == TypeUser || msg.Name == "woland" {
			if strings.Contains(textLower, strings.ToLower(w.busName)) {
				targets = append(targets, w)
			}
		}
	}

	return targets
}

// tmuxArgs builds a tmux argument list, prepending -L <socket> when configured.
func (w *Watcher) tmuxArgs(args ...string) []string {
	if w.tmuxSocket != "" {
		return append([]string{"-L", w.tmuxSocket}, args...)
	}
	return args
}

// stopAllWatchers cancels all running agent output watchers and waits for them.
func (w *Watcher) stopAllWatchers() {
	w.mu.Lock()
	defer w.mu.Unlock()

	for id, aw := range w.watchers {
		w.logger.Printf("stopping output watcher for agent %q", id)
		aw.cancel()
		<-aw.done
		delete(w.watchers, id)
	}
}
