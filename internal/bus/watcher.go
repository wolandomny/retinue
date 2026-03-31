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
	"strings"
	"sync"
	"time"

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
)

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

// agentWatcher tracks per-agent output monitoring state.
type agentWatcher struct {
	cancel context.CancelFunc
	done   chan struct{}
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
	watchers map[string]*agentWatcher // agentID → watcher
}

// NewWatcher creates a Watcher that bridges the given bus with agent sessions.
func NewWatcher(b *Bus, tmuxSocket, aptPath string, logger *log.Logger) *Watcher {
	return &Watcher{
		bus:        b,
		tmuxSocket: tmuxSocket,
		aptPath:    aptPath,
		logger:     logger,
		watchers:   make(map[string]*agentWatcher),
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

// discoverAgents polls tmux for running agent windows and starts/stops
// output watchers as needed.
func (w *Watcher) discoverAgents(ctx context.Context) {
	windows, err := w.listAgentWindows(ctx)
	if err != nil {
		w.logger.Printf("error listing agent windows: %v", err)
		return
	}

	active := make(map[string]bool, len(windows))
	for _, id := range windows {
		active[id] = true
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	// Start watchers for new agents.
	for _, id := range windows {
		if _, exists := w.watchers[id]; !exists {
			w.startAgentWatcher(ctx, id)
		}
	}

	// Stop watchers for agents that are no longer running.
	for id, aw := range w.watchers {
		if !active[id] {
			w.logger.Printf("agent %q window disappeared, stopping output watcher", id)
			aw.cancel()
			<-aw.done
			delete(w.watchers, id)
		}
	}
}

// listAgentWindows returns the agent IDs extracted from tmux window names
// matching the "agent-" prefix.
func (w *Watcher) listAgentWindows(ctx context.Context) ([]string, error) {
	args := w.tmuxArgs("list-windows", "-t", "retinue", "-F", "#{window_name}")
	cmd := exec.CommandContext(ctx, "tmux", args...)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return nil, nil // session doesn't exist
		}
		return nil, fmt.Errorf("tmux list-windows: %w", err)
	}

	return parseAgentWindows(string(out)), nil
}

// parseAgentWindows extracts agent IDs from tmux list-windows output.
// It filters for windows with "agent-" prefixed names and returns the
// agent ID portion (everything after "agent-").
func parseAgentWindows(tmuxOutput string) []string {
	var agents []string
	for _, line := range strings.Split(strings.TrimSpace(tmuxOutput), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "agent-") {
			id := strings.TrimPrefix(line, "agent-")
			if id != "" {
				agents = append(agents, id)
			}
		}
	}
	return agents
}

// startAgentWatcher begins monitoring an agent's Claude session JSONL file.
// Must be called with w.mu held.
func (w *Watcher) startAgentWatcher(ctx context.Context, agentID string) {
	childCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	aw := &agentWatcher{
		cancel: cancel,
		done:   done,
	}
	w.watchers[agentID] = aw

	// Snapshot the newest session file at the time the agent window appears.
	sessionFile := w.findAgentSessionFile(agentID)

	w.logger.Printf("starting output watcher for agent %q (session: %s)", agentID, filepath.Base(sessionFile))

	go func() {
		defer close(done)
		w.watchAgentOutput(childCtx, agentID, sessionFile)
	}()
}

// findAgentSessionFile returns the most recently modified .jsonl file in the
// Claude projects directory. This is the file that was active when the agent
// started.
func (w *Watcher) findAgentSessionFile(agentID string) string {
	projDir := claudeProjectDir(w.aptPath)
	entries, err := os.ReadDir(projDir)
	if err != nil {
		w.logger.Printf("error reading Claude projects dir for agent %q: %v", agentID, err)
		return ""
	}

	var newest string
	var newestTime time.Time

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(newestTime) {
			newestTime = info.ModTime()
			newest = filepath.Join(projDir, entry.Name())
		}
	}

	return newest
}

// claudeProjectDir derives the Claude Code projects directory from an
// apartment path.
func claudeProjectDir(aptPath string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}
	mangled := strings.ReplaceAll(aptPath, "/", "-")
	mangled = strings.ReplaceAll(mangled, ".", "-")
	return filepath.Join(home, ".claude", "projects", mangled)
}

// watchAgentOutput tails an agent's Claude session JSONL file and writes
// extracted assistant messages to the bus.
func (w *Watcher) watchAgentOutput(ctx context.Context, agentID, sessionFile string) {
	seen := make(map[string]bool)
	var partialLine string
	var offset int64

	projDir := claudeProjectDir(w.aptPath)

	// Wait for a session file if none was found at startup.
	if sessionFile == "" {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(agentPollInterval):
			}
			sessionFile = w.findNewestJSONL(projDir)
			if sessionFile != "" {
				w.logger.Printf("agent %q: found session file %s", agentID, filepath.Base(sessionFile))
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
	offset, partialLine = w.readAgentLines(ctx, agentID, sessionFile, offset, partialLine, seen, true)

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(agentPollInterval):
		}

		// Check if a newer session file has appeared (the agent may have
		// restarted its Claude session).
		newest := w.findNewestJSONL(projDir)
		if newest != "" && newest != sessionFile {
			w.logger.Printf("agent %q: session file changed to %s", agentID, filepath.Base(newest))
			sessionFile = newest
			offset = 0
			partialLine = ""
			// Drain the new file.
			offset, partialLine = w.readAgentLines(ctx, agentID, sessionFile, offset, partialLine, seen, true)
			continue
		}

		offset, partialLine = w.readAgentLines(ctx, agentID, sessionFile, offset, partialLine, seen, false)
	}
}

// findNewestJSONL returns the most recently modified .jsonl file in the given directory.
func (w *Watcher) findNewestJSONL(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}

	var newest string
	var newestTime time.Time

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(newestTime) {
			newestTime = info.ModTime()
			newest = filepath.Join(dir, entry.Name())
		}
	}
	return newest
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
// extracted assistant messages to the bus (unless draining).
func (w *Watcher) readAgentLines(ctx context.Context, agentID, path string, offset int64, partialLine string, seen map[string]bool, draining bool) (int64, string) {
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

		text, uuid, ok := parseSessionLine(line)
		if !ok {
			continue
		}

		if uuid != "" && seen[uuid] {
			continue
		}
		if uuid != "" {
			seen[uuid] = true
		}

		if text != "" && !draining {
			if err := w.bus.Append(NewMessage(agentID, TypeChat, text)); err != nil {
				w.logger.Printf("error writing agent %q message to bus: %v", agentID, err)
			}
		}
	}

	return offset + bytesRead, partialLine
}

// parseSessionLine parses a single JSONL line and extracts text from
// assistant messages. Returns the concatenated text, UUID, and success.
func parseSessionLine(line string) (text string, uuid string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", "", false
	}

	var msg sessionMessage
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		return "", "", false
	}

	if msg.Type != "assistant" {
		return "", "", true // valid JSON but not an assistant message
	}

	var parts []string
	for _, block := range msg.Message.Content {
		if block.Type == "text" && block.Text != "" {
			parts = append(parts, block.Text)
		}
	}

	if len(parts) == 0 {
		return "", msg.UUID, true
	}

	return strings.Join(parts, "\n"), msg.UUID, true
}

// injectMessage sends a bus message to all active agent tmux sessions
// except the sender.
func (w *Watcher) injectMessage(ctx context.Context, msg Message) {
	// Don't inject system messages into agent sessions.
	if msg.Type == TypeSystem {
		return
	}

	formatted := FormatForInjection(&msg)
	escaped := shell.EscapeTmux(formatted)

	w.mu.Lock()
	agents := make([]string, 0, len(w.watchers))
	for id := range w.watchers {
		agents = append(agents, id)
	}
	w.mu.Unlock()

	targets := injectionTargets(agents, msg)
	for _, agentID := range targets {
		target := fmt.Sprintf("retinue:agent-%s", agentID)
		args := w.tmuxArgs("send-keys", "-t", target, "--", escaped, "Enter")

		cmd := exec.CommandContext(ctx, "tmux", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			w.logger.Printf("error injecting message to agent %q: %v: %s", agentID, err, string(out))
		}
	}
}

// injectionTargets returns the subset of agent IDs that should receive a
// given message. It filters out the sender (to avoid echo) and system messages.
func injectionTargets(agents []string, msg Message) []string {
	if msg.Type == TypeSystem {
		return nil
	}
	var targets []string
	for _, id := range agents {
		if id == msg.Name {
			continue
		}
		targets = append(targets, id)
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
