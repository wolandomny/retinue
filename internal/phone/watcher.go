// Package phone bridges a running Woland Claude Code session to Telegram,
// allowing the user to chat with Woland from their phone.
package phone

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// pollInterval is how often we check the session file for new content.
	pollInterval = 1500 * time.Millisecond

	// dirPollInterval is how often we check for new session files.
	dirPollInterval = 3 * time.Second
)

// sessionMessage represents the top-level structure of a JSONL line from
// Claude Code's session file. We only parse the fields we need.
type sessionMessage struct {
	Type    string          `json:"type"`
	UUID    string          `json:"uuid"`
	Message messageContent  `json:"message"`
}

// messageContent represents the "message" field within a session message.
type messageContent struct {
	Content []contentBlock `json:"content"`
}

// contentBlock represents a single content block within a message.
type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Watcher monitors a Claude Code session JSONL file for new assistant messages
// and emits their text content on a channel.
type Watcher struct {
	projectDir string
	logger     *log.Logger
	seen       map[string]bool // track seen UUIDs to avoid duplicates
}

// NewWatcher creates a Watcher that monitors the given Claude Code projects
// directory for session JSONL files.
func NewWatcher(apartmentPath string, logger *log.Logger) *Watcher {
	return &Watcher{
		projectDir: claudeProjectDir(apartmentPath),
		logger:     logger,
		seen:       make(map[string]bool),
	}
}

// claudeProjectDir derives the Claude Code projects directory from an
// apartment path. For example, /Users/broc.oppler/apt becomes
// ~/.claude/projects/-Users-broc-oppler-apt/
func claudeProjectDir(aptPath string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}
	// Replace path separators and dots with hyphens to match Claude Code's convention.
	mangled := strings.ReplaceAll(aptPath, "/", "-")
	mangled = strings.ReplaceAll(mangled, ".", "-")
	return filepath.Join(home, ".claude", "projects", mangled)
}

// Watch starts watching for new assistant messages and returns a channel
// that emits extracted text. The channel is closed when the context is cancelled.
// The sessionSwitch channel (if non-nil) receives a signal when a new session
// file is detected.
func (w *Watcher) Watch(ctx context.Context, sessionSwitch chan<- struct{}) <-chan string {
	out := make(chan string, 16)

	go func() {
		defer close(out)
		w.watchLoop(ctx, out, sessionSwitch)
	}()

	return out
}

// watchLoop is the main loop that discovers session files and tails them.
func (w *Watcher) watchLoop(ctx context.Context, out chan<- string, sessionSwitch chan<- struct{}) {
	var currentFile string
	var offset int64

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		activeFile, err := w.findActiveSession()
		if err != nil {
			w.logger.Printf("error finding session file: %v", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(dirPollInterval):
				continue
			}
		}

		if activeFile == "" {
			select {
			case <-ctx.Done():
				return
			case <-time.After(dirPollInterval):
				continue
			}
		}

		if activeFile != currentFile {
			if currentFile != "" {
				// Session changed — notify.
				w.logger.Printf("session file changed: %s -> %s", filepath.Base(currentFile), filepath.Base(activeFile))
				if sessionSwitch != nil {
					select {
					case sessionSwitch <- struct{}{}:
					default:
					}
				}
			} else {
				w.logger.Printf("watching session file: %s", filepath.Base(activeFile))
			}
			currentFile = activeFile
			// Start from end of new file to avoid replaying old messages.
			info, err := os.Stat(currentFile)
			if err != nil {
				w.logger.Printf("error stating session file: %v", err)
				continue
			}
			offset = info.Size()
		}

		offset = w.readNewLines(ctx, currentFile, offset, out)

		select {
		case <-ctx.Done():
			return
		case <-time.After(pollInterval):
		}
	}
}

// findActiveSession finds the most recently modified .jsonl file in the
// project directory (not including subdirectories).
func (w *Watcher) findActiveSession() (string, error) {
	entries, err := os.ReadDir(w.projectDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("reading project dir: %w", err)
	}

	var newest string
	var newestTime time.Time

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		if info.ModTime().After(newestTime) {
			newestTime = info.ModTime()
			newest = filepath.Join(w.projectDir, entry.Name())
		}
	}

	return newest, nil
}

// readNewLines reads any new lines appended to the file since the given offset,
// parses them, and sends extracted text to the output channel. Returns the
// new offset.
func (w *Watcher) readNewLines(ctx context.Context, path string, offset int64, out chan<- string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		w.logger.Printf("error stating file: %v", err)
		return offset
	}

	// Handle file truncation.
	if info.Size() < offset {
		w.logger.Printf("file truncated, resetting position")
		offset = 0
	}

	if info.Size() == offset {
		return offset
	}

	f, err := os.Open(path)
	if err != nil {
		w.logger.Printf("error opening file: %v", err)
		return offset
	}
	defer f.Close()

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		w.logger.Printf("error seeking: %v", err)
		return offset
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024) // up to 10MB lines
	var partialLine string

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return offset
		default:
		}

		line := scanner.Text()
		if partialLine != "" {
			line = partialLine + line
			partialLine = ""
		}

		text, uuid, ok := w.parseLine(line)
		if !ok {
			// Might be a partial write — buffer for next poll.
			// But only if it looks like it could be incomplete JSON.
			if len(line) > 0 && line[len(line)-1] != '}' {
				partialLine = line
				continue
			}
			continue
		}

		if uuid != "" && w.seen[uuid] {
			continue
		}
		if uuid != "" {
			w.seen[uuid] = true
		}

		if text != "" {
			select {
			case out <- text:
			case <-ctx.Done():
				return offset
			}
		}
	}

	// Get the new offset from current file position.
	newOffset, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		w.logger.Printf("error getting position: %v", err)
		return offset
	}

	return newOffset
}

// parseLine parses a single JSONL line and extracts text from assistant messages.
// Returns the concatenated text, the UUID (if any), and whether parsing succeeded.
func (w *Watcher) parseLine(line string) (text string, uuid string, ok bool) {
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

// ParseLine is an exported version of parseLine for testing.
func ParseLine(line string) (text string, uuid string, ok bool) {
	w := &Watcher{seen: make(map[string]bool)}
	return w.parseLine(line)
}

// ClaudeProjectDir is an exported version of claudeProjectDir for testing.
func ClaudeProjectDir(aptPath string) string {
	return claudeProjectDir(aptPath)
}
