// Package phone bridges a running Woland Claude Code session to Telegram,
// allowing the user to chat with Woland from their phone.
package phone

import (
	"bufio"
	"bytes"
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

	// startupWindow is how far back from end-of-file we seek when first
	// discovering a session file, so we can catch recently-written messages.
	startupWindow = 4096

	// maxSessionCheckBytes is the maximum number of bytes to read from the
	// start of a session file when checking if it belongs to a Woland
	// planning session.
	maxSessionCheckBytes = 32 * 1024

	// minBytesForConclusive is the minimum amount of file content required
	// before we conclusively determine a file is NOT a Woland session.
	// Files smaller than this may not yet have their system prompt written.
	minBytesForConclusive = 256
)

// wolandKeywords are substrings (lowercase) that identify a Woland planning
// session's content. Worker agent sessions use a different system prompt
// ("You are a worker agent...") that does not contain these terms.
var wolandKeywords = []string{
	"woland",
	"koroviev",
}

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
	projectDir   string
	logger       *log.Logger
	seen         map[string]bool // track seen UUIDs to avoid duplicates
	partialLine  string          // buffered incomplete line from previous read
	sessionCache map[string]bool // caches per-file Woland session check results
}

// NewWatcher creates a Watcher that monitors the given Claude Code projects
// directory for session JSONL files.
func NewWatcher(apartmentPath string, logger *log.Logger) *Watcher {
	return &Watcher{
		projectDir:   claudeProjectDir(apartmentPath),
		logger:       logger,
		seen:         make(map[string]bool),
		sessionCache: make(map[string]bool),
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
			w.partialLine = "" // Clear partial line from previous file.

			info, err := os.Stat(currentFile)
			if err != nil {
				w.logger.Printf("error stating session file: %v", err)
				continue
			}

			// Seek to the last startupWindow bytes to catch recent messages
			// instead of seeking to the exact end (which would miss them).
			offset = info.Size() - startupWindow
			if offset < 0 {
				offset = 0
			}
			if offset > 0 {
				// Find the first complete line boundary after the offset.
				adjusted, err := seekToLineStart(currentFile, offset)
				if err != nil {
					w.logger.Printf("error finding line boundary: %v", err)
					offset = info.Size()
				} else {
					offset = adjusted
					w.logger.Printf("startup: seeking to offset %d (file size %d)", offset, info.Size())
				}
			}
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
// project directory that belongs to a Woland planning session. Worker agent
// session files are filtered out based on their content.
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

		path := filepath.Join(w.projectDir, entry.Name())

		// Skip files conclusively identified as non-Woland sessions.
		if !w.mayBeWolandSession(path) {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		if info.ModTime().After(newestTime) {
			newestTime = info.ModTime()
			newest = path
		}
	}

	return newest, nil
}

// mayBeWolandSession returns true if the file at path could be a Woland
// planning session. Returns false only when the file has been conclusively
// identified as a non-Woland session (e.g., a worker agent). Results are
// cached so each file is only inspected once.
func (w *Watcher) mayBeWolandSession(path string) bool {
	if w.sessionCache == nil {
		w.sessionCache = make(map[string]bool)
	}

	if result, cached := w.sessionCache[path]; cached {
		return result
	}

	isWoland, conclusive := checkWolandSession(path)
	if conclusive {
		w.sessionCache[path] = isWoland
		return isWoland
	}

	// Inconclusive — file may be too new/small to have its system prompt
	// written yet. Don't cache; allow it as a candidate so new Woland
	// sessions aren't missed during startup.
	return true
}

// checkWolandSession reads the first maxSessionCheckBytes bytes of a JSONL
// session file and checks whether the content contains Woland-identifying
// keywords. Returns (isWoland, conclusive) where conclusive indicates whether
// the file had enough content to make a reliable determination.
func checkWolandSession(path string) (isWoland bool, conclusive bool) {
	f, err := os.Open(path)
	if err != nil {
		return false, false
	}
	defer f.Close()

	buf := make([]byte, maxSessionCheckBytes)
	n, err := f.Read(buf)
	if n == 0 {
		return false, false // Empty file — inconclusive.
	}

	content := strings.ToLower(string(buf[:n]))
	for _, kw := range wolandKeywords {
		if strings.Contains(content, kw) {
			return true, true
		}
	}

	// If we read enough content without finding keywords, this is
	// conclusively not a Woland session.
	if n >= minBytesForConclusive {
		return false, true
	}

	return false, false
}

// seekToLineStart finds the first complete line boundary at or after the
// given offset by scanning forward for the first newline. Returns the
// position immediately after the newline.
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

	// Skip past the first (likely partial) line to find a clean boundary.
	r := bufio.NewReader(f)
	skipped, err := r.ReadBytes('\n')
	if err != nil {
		// EOF without newline — return end of what we read.
		return offset + int64(len(skipped)), nil
	}
	return offset + int64(len(skipped)), nil
}

// readNewLines reads any new lines appended to the file since the given offset,
// parses them, and sends extracted text to the output channel. Returns the
// new offset.
//
// Partial lines (data without a trailing newline) are buffered in w.partialLine
// and prepended to the first line on the next call. The file offset advances
// past all bytes read; the partial content is reconstructed from the saved
// buffer so nothing is lost.
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
		w.partialLine = ""
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

	// Read all new data from the file.
	newData, err := io.ReadAll(f)
	if err != nil {
		w.logger.Printf("error reading file: %v", err)
		return offset
	}

	bytesRead := int64(len(newData))

	// Prepend any buffered partial line from the previous read.
	var data []byte
	if w.partialLine != "" {
		data = make([]byte, len(w.partialLine)+len(newData))
		copy(data, w.partialLine)
		copy(data[len(w.partialLine):], newData)
		w.partialLine = ""
	} else {
		data = newData
	}

	// Process complete lines (terminated by \n). Any trailing data without
	// a newline is saved as a partial line for the next read.
	pos := 0
	for pos < len(data) {
		nlIdx := bytes.IndexByte(data[pos:], '\n')
		if nlIdx == -1 {
			// No more complete lines — buffer the remainder.
			w.partialLine = string(data[pos:])
			w.logger.Printf("buffering partial line (%d bytes)", len(w.partialLine))
			break
		}

		line := string(data[pos : pos+nlIdx])
		pos += nlIdx + 1

		select {
		case <-ctx.Done():
			return offset + bytesRead
		default:
		}

		text, uuid, ok := w.parseLine(line)
		if !ok {
			continue
		}

		if uuid != "" && w.seen[uuid] {
			w.logger.Printf("skipping duplicate (uuid=%s)", uuid)
			continue
		}
		if uuid != "" {
			w.seen[uuid] = true
		}

		if text != "" {
			preview := text
			if len(preview) > 50 {
				preview = preview[:50] + "..."
			}
			w.logger.Printf("new assistant message: %q", preview)

			select {
			case out <- text:
			case <-ctx.Done():
				return offset + bytesRead
			}
		}
	}

	return offset + bytesRead
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

// SeekToLineStart is an exported version of seekToLineStart for testing.
func SeekToLineStart(path string, offset int64) (int64, error) {
	return seekToLineStart(path, offset)
}

// CheckWolandSession is an exported version of checkWolandSession for testing.
func CheckWolandSession(path string) (isWoland bool, conclusive bool) {
	return checkWolandSession(path)
}
