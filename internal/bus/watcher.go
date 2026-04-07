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
	"github.com/wolandomny/retinue/internal/standing"
)

const (
	// discoverInterval is how often the watcher polls for new/removed agent windows.
	discoverInterval = 3 * time.Second

	// agentPollInterval is how often each agent output watcher checks for new content.
	agentPollInterval = 1500 * time.Millisecond

	// agentStartupWindow is how far back from end-of-file we seek when first
	// discovering an agent's session file (to drain for dedup, not forward).
	agentStartupWindow = 4096

	// rediscoveryInterval is how long the watcher waits before re-reading the
	// marker file to check if the agent restarted with a new session.
	rediscoveryInterval = 30 * time.Second

	// wolandStaleThreshold is the maximum time since last modification before
	// a Woland session file is considered stale. When stale, the watcher
	// scans for a newer JSONL file to auto-discover a new session.
	wolandStaleThreshold = 60 * time.Second
)

// HeartbeatMessage is the text injected into an agent's tmux window on each
// scheduled heartbeat tick.
const HeartbeatMessage = "[Heartbeat] Scheduled check"

// heartbeatTicker tracks a per-agent heartbeat schedule.
type heartbeatTicker struct {
	ticker *time.Ticker
	cancel context.CancelFunc
	done   chan struct{}
}

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

	// heartbeats tracks per-agent heartbeat tickers. keyed by agent bus name.
	heartbeats map[string]*heartbeatTicker

	// agentSchedules caches schedule durations loaded from agents.yaml,
	// keyed by agent ID (bus name).
	agentSchedules map[string]time.Duration

	// agentsYAMLMtime caches the last-seen mtime of agents.yaml to avoid
	// re-parsing on every discover cycle.
	agentsYAMLMtime time.Time

	// injectHeartbeatFn is the function used to inject heartbeat messages.
	// Defaults to the real tmux send-keys implementation. Override in tests.
	injectHeartbeatFn func(ctx context.Context, windowName, text string) error
}

// NewWatcher creates a Watcher that bridges the given bus with agent sessions.
func NewWatcher(b *Bus, tmuxSocket, aptPath string, logger *log.Logger) *Watcher {
	w := &Watcher{
		bus:            b,
		tmuxSocket:     tmuxSocket,
		aptPath:        aptPath,
		logger:         logger,
		watchers:       make(map[string]*agentWatcher),
		heartbeats:     make(map[string]*heartbeatTicker),
		agentSchedules: make(map[string]time.Duration),
	}
	w.injectHeartbeatFn = w.injectHeartbeatTmux
	return w
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
			w.stopAllHeartbeats()
			w.stopAllWatchers()
			return nil

		case msg, ok := <-busCh:
			if !ok {
				w.stopAllHeartbeats()
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

	// Build a set of active agent bus names for heartbeat management.
	activeAgents := make(map[string]bool)
	for _, win := range windows {
		if !win.isWoland {
			activeAgents[win.busName] = true
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

	// Manage heartbeat tickers based on agent schedules.
	w.refreshSchedules()
	w.syncHeartbeats(ctx, windows, activeAgents)
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
	// Snapshot the session file at the time the window appears.
	var sessionFile string
	if win.isWoland {
		sessionFile = w.findWolandSessionFile()
	} else {
		sessionFile = w.findAgentSessionFile(win.busName)
	}

	if sessionFile == "" {
		w.logger.Printf("no valid session file for %q, skipping", win.busName)
		// Don't add to watchers — will be retried on next poll.
		return
	}

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

	w.logger.Printf("starting output watcher for %q (window: %s, session: %s)", win.busName, win.windowName, filepath.Base(sessionFile))

	go func() {
		defer close(done)
		w.watchAgentOutput(childCtx, win.busName, sessionFile, win.isWoland)
	}()
}

// findAgentSessionFile returns the session file for the given agent by reading
// the marker file (.agent-<id>-session) in the apartment directory. The marker
// is set at startup using reliable diff-based detection and is always trusted.
// Returns "" if the marker is missing, empty, or points to a nonexistent file.
func (w *Watcher) findAgentSessionFile(agentID string) string {
	markerPath := filepath.Join(w.aptPath, fmt.Sprintf(".agent-%s-session", agentID))
	data, err := os.ReadFile(markerPath)
	if err != nil {
		w.logger.Printf("no session marker for agent %q", agentID)
		return ""
	}
	sessionPath := strings.TrimSpace(string(data))
	if sessionPath == "" {
		w.logger.Printf("empty session marker for agent %q", agentID)
		return ""
	}
	if _, err := os.Stat(sessionPath); err != nil {
		w.logger.Printf("agent %q marker points to missing file %s", agentID, filepath.Base(sessionPath))
		return ""
	}
	return sessionPath
}

// claudeProjectDir derives the Claude Code projects directory from an
// apartment path. Delegates to the shared session.ClaudeProjectDir.
func claudeProjectDir(aptPath string) string {
	return session.ClaudeProjectDir(aptPath)
}

// findWolandSessionFile reads the .woland-session marker file to locate
// Woland's active Claude session JSONL. If the marker points to a stale
// file (not modified within wolandStaleThreshold), it scans the Claude
// project directory for a newer JSONL file that doesn't belong to any
// known agent session. Returns "" if the marker is missing, empty, or
// points to a nonexistent file and no newer file can be auto-discovered.
func (w *Watcher) findWolandSessionFile() string {
	markerPath := filepath.Join(w.aptPath, ".woland-session")
	data, err := os.ReadFile(markerPath)
	if err != nil {
		w.logger.Printf("no .woland-session marker found")
		return ""
	}
	sessionPath := strings.TrimSpace(string(data))
	if sessionPath == "" {
		w.logger.Printf("empty .woland-session marker")
		return ""
	}
	info, err := os.Stat(sessionPath)
	if err != nil {
		w.logger.Printf(".woland-session marker points to missing file %q", sessionPath)
		return ""
	}

	// If the file is still being actively written to, trust the marker.
	if time.Since(info.ModTime()) <= wolandStaleThreshold {
		w.logger.Printf("using .woland-session marker: %s (modified %s ago)",
			filepath.Base(sessionPath), time.Since(info.ModTime()).Round(time.Second))
		return sessionPath
	}

	// Session file is stale — attempt auto-discovery.
	projDir := session.ClaudeProjectDir(w.aptPath)
	agentSessions := w.knownAgentSessionFiles()

	for _, candidate := range session.SortedJSONLFiles(projDir) {
		cleanCandidate := filepath.Clean(candidate)
		if cleanCandidate == filepath.Clean(sessionPath) {
			// Same file as current marker — still stale, skip.
			continue
		}
		if agentSessions[cleanCandidate] {
			// This file belongs to a known agent session, skip.
			w.logger.Printf("auto-discovery: skipping %s (claimed by agent)", filepath.Base(candidate))
			continue
		}
		// Only switch to files that have been modified recently.
		candidateInfo, err := os.Stat(candidate)
		if err != nil {
			continue
		}
		if time.Since(candidateInfo.ModTime()) > wolandStaleThreshold {
			continue
		}
		// Found a fresh file not claimed by any agent — auto-switch.
		if err := atomicWriteMarker(markerPath, candidate); err != nil {
			w.logger.Printf("failed to update .woland-session marker: %v", err)
		}
		w.logger.Printf("woland session stale, auto-discovered: %s", filepath.Base(candidate))
		return candidate
	}

	// No better candidate found; return the stale file.
	w.logger.Printf("using .woland-session marker: %s (modified %s ago, stale but no newer candidate)",
		filepath.Base(sessionPath), time.Since(info.ModTime()).Round(time.Second))
	return sessionPath
}

// knownAgentSessionFiles returns a set of session file paths currently
// referenced by agent marker files (.agent-*-session) in the apartment
// directory. Used to avoid auto-switching Woland to an agent's session.
// Paths are normalized to absolute clean paths for reliable comparison.
func (w *Watcher) knownAgentSessionFiles() map[string]bool {
	result := make(map[string]bool)
	pattern := filepath.Join(w.aptPath, ".agent-*-session")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return result
	}
	for _, markerPath := range matches {
		data, err := os.ReadFile(markerPath)
		if err != nil {
			continue
		}
		path := strings.TrimSpace(string(data))
		if path != "" {
			// Normalize to absolute clean path for reliable comparison.
			if abs, err := filepath.Abs(path); err == nil {
				path = abs
			}
			path = filepath.Clean(path)
			result[path] = true
		}
	}
	return result
}

// atomicWriteMarker updates a marker file atomically by writing to a
// temporary file and renaming it into place.
func atomicWriteMarker(markerPath, content string) error {
	tmpPath := markerPath + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmpPath, markerPath)
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

		prevOffset := offset
		offset, partialLine = w.readAgentLines(ctx, agentID, sessionFile, offset, partialLine, seen, false, isWoland)

		if offset != prevOffset {
			lastNewContent = time.Now()
		}

		// Re-read marker to check if it was updated (e.g., agent restarted).
		if time.Since(lastNewContent) > rediscoveryInterval {
			var rediscovered string
			if isWoland {
				rediscovered = w.findWolandSessionFile()
			} else {
				rediscovered = w.findAgentSessionFile(agentID)
			}
			if rediscovered != "" && rediscovered != sessionFile {
				w.logger.Printf("%q: marker updated, switching to %s", agentID, filepath.Base(rediscovered))
				sessionFile = rediscovered
				offset = 0
				partialLine = ""
				seen = make(map[string]bool)
				// Drain existing content in the new file.
				offset, partialLine = w.readAgentLines(ctx, agentID, sessionFile, offset, partialLine, seen, true, isWoland)
			}
			lastNewContent = time.Now()
		}
	}
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
			msg := NewMessage(agentID, TypeChat, text)
			if isWoland {
				// Woland's output: parse → convention for explicit agent routing.
				if recipients, stripped, ok := parseArrowRouting(text); ok {
					msg.To = recipients
					msg.Text = stripped
					w.logger.Printf("arrow routing parsed: To=%v text=%q", msg.To, stripped[:min(50, len(stripped))])
				} else {
					w.logger.Printf("no arrow routing for woland message (first 80 chars): %q", text[:min(80, len(text))])
				}
				// No arrow prefix = no agent routing (user-facing only).
			} else {
				// Standing agent messages always go to Woland (the hub).
				msg.To = []string{"woland"}
			}
			w.logger.Printf("appending to bus: name=%q type=%q to=%v", msg.Name, msg.Type, msg.To)
			if err := w.bus.Append(msg); err != nil {
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
			msg := NewMessage("user", TypeUser, text)
			msg.To = []string{"woland"}
			if err := w.bus.Append(msg); err != nil {
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

// injectMessage sends a bus message to the appropriate monitored tmux
// sessions using explicit To-based routing (via routeMessage).
func (w *Watcher) injectMessage(ctx context.Context, msg Message) {
	if msg.Type == TypeSystem {
		return
	}

	formatted := FormatForInjection(&msg)

	w.mu.Lock()
	windows := make([]injectionWindow, 0, len(w.watchers))
	for _, aw := range w.watchers {
		windows = append(windows, injectionWindow{
			busName:    aw.busName,
			windowName: aw.windowName,
			isWoland:   aw.isWoland,
		})
	}
	w.mu.Unlock()

	targets := routeMessage(windows, msg)

	// Debug: log routing decision details
	w.logger.Printf("routing decision: msg.Name=%q msg.Type=%q msg.To=%v targets=%d windows=%d",
		msg.Name, msg.Type, msg.To, len(targets), len(windows))
	for _, win := range windows {
		w.logger.Printf("  window: busName=%q isWoland=%v", win.busName, win.isWoland)
	}

	if len(targets) > 0 {
		names := make([]string, len(targets))
		for i, t := range targets {
			names[i] = t.busName
		}
		w.logger.Printf("routing message from %q to %d targets: %v",
			msg.Name, len(targets), names)
	} else if len(msg.To) > 0 {
		// Message has explicit recipients but no matching windows.
		w.logger.Printf("WARNING: message from %q has explicit recipients %v but no matching windows found",
			msg.Name, msg.To)
	}

	for _, t := range targets {
		target := fmt.Sprintf("retinue:%s", t.windowName)
		if err := shell.InjectText(ctx, w.tmuxBaseArgs(), target, formatted); err != nil {
			w.logger.Printf("error injecting message to %q (window %s): %v",
				t.busName, t.windowName, err)
		}
	}
}


// tmuxBaseArgs returns the tmux socket prefix arguments (e.g. ["-L", "retinue-apt"])
// without any subcommand. This is suitable for passing to helpers like shell.InjectText
// that append their own subcommands.
func (w *Watcher) tmuxBaseArgs() []string {
	if w.tmuxSocket != "" {
		return []string{"-L", w.tmuxSocket}
	}
	return nil
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

// ---------------------------------------------------------------------------
// Heartbeat scheduling
// ---------------------------------------------------------------------------

// refreshSchedules reloads agent schedules from agents.yaml if the file has
// changed since the last load. This avoids re-parsing on every discover cycle.
func (w *Watcher) refreshSchedules() {
	agentsFile := filepath.Join(w.aptPath, "agents.yaml")
	info, err := os.Stat(agentsFile)
	if err != nil {
		// No agents.yaml — clear cached schedules.
		w.agentSchedules = make(map[string]time.Duration)
		return
	}

	if info.ModTime().Equal(w.agentsYAMLMtime) {
		return // unchanged
	}
	w.agentsYAMLMtime = info.ModTime()

	store := standing.NewFileStore(agentsFile)
	agents, err := store.Load()
	if err != nil {
		w.logger.Printf("error loading agents.yaml for schedules: %v", err)
		return
	}

	schedules := make(map[string]time.Duration, len(agents))
	for _, a := range agents {
		interval, active, err := standing.ParseSchedule(a.Schedule)
		if err != nil {
			w.logger.Printf("agent %q has invalid schedule %q: %v", a.ID, a.Schedule, err)
			continue
		}
		if active {
			schedules[a.ID] = interval
		}
	}
	w.agentSchedules = schedules
}

// syncHeartbeats starts or stops heartbeat tickers so they match the current
// set of active agent windows and their configured schedules.
// Must be called with w.mu held.
func (w *Watcher) syncHeartbeats(ctx context.Context, windows []monitoredWindow, activeAgents map[string]bool) {
	// Build a windowName lookup for active agents (non-Woland).
	agentWindows := make(map[string]string) // busName → windowName
	for _, win := range windows {
		if !win.isWoland {
			agentWindows[win.busName] = win.windowName
		}
	}

	// Start tickers for agents that have a schedule and are active.
	for busName, interval := range w.agentSchedules {
		windowName, alive := agentWindows[busName]
		if !alive {
			continue // agent not running
		}
		if _, exists := w.heartbeats[busName]; exists {
			continue // already ticking
		}
		w.startHeartbeat(ctx, busName, windowName, interval)
	}

	// Stop tickers for agents that disappeared or lost their schedule.
	for busName, hb := range w.heartbeats {
		_, stillActive := activeAgents[busName]
		_, hasSchedule := w.agentSchedules[busName]
		if !stillActive || !hasSchedule {
			w.logger.Printf("stopping heartbeat for agent %q", busName)
			hb.cancel()
			<-hb.done
			delete(w.heartbeats, busName)
		}
	}
}

// startHeartbeat launches a goroutine that periodically injects a heartbeat
// message into the agent's tmux window.
func (w *Watcher) startHeartbeat(ctx context.Context, busName, windowName string, interval time.Duration) {
	childCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	ticker := time.NewTicker(interval)

	hb := &heartbeatTicker{
		ticker: ticker,
		cancel: cancel,
		done:   done,
	}
	w.heartbeats[busName] = hb
	w.logger.Printf("starting heartbeat for agent %q (window: %s, interval: %v)", busName, windowName, interval)

	go func() {
		defer close(done)
		defer ticker.Stop()
		for {
			select {
			case <-childCtx.Done():
				return
			case <-ticker.C:
				if err := w.injectHeartbeatFn(childCtx, windowName, HeartbeatMessage); err != nil {
					w.logger.Printf("error injecting heartbeat to agent %q: %v", busName, err)
				}
			}
		}
	}()
}

// injectHeartbeatTmux injects a heartbeat message into an agent's tmux window
// using the reliable load-buffer + paste-buffer + send-keys pattern.
// This is the default production implementation.
func (w *Watcher) injectHeartbeatTmux(ctx context.Context, windowName, text string) error {
	target := fmt.Sprintf("retinue:%s", windowName)
	if err := shell.InjectText(ctx, w.tmuxBaseArgs(), target, text); err != nil {
		return fmt.Errorf("injecting heartbeat to %s: %w", windowName, err)
	}
	return nil
}

// stopAllHeartbeats cancels all running heartbeat tickers and waits for them.
func (w *Watcher) stopAllHeartbeats() {
	w.mu.Lock()
	defer w.mu.Unlock()

	for busName, hb := range w.heartbeats {
		w.logger.Printf("stopping heartbeat for agent %q", busName)
		hb.cancel()
		<-hb.done
		delete(w.heartbeats, busName)
	}
}
