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

	"github.com/wolandomny/retinue/internal/session"
)

const (
	// pollInterval is how often we check the session file for new content.
	pollInterval = 1500 * time.Millisecond

	// dirPollInterval is how often we check for new session files.
	dirPollInterval = 3 * time.Second

	// startupWindow is how far back from end-of-file we seek when first
	// discovering a session file, so we can catch recently-written messages.
	startupWindow = 4096

	// wolandSessionMarker is the name of the marker file that locks the
	// watcher onto a specific Woland session. This prevents worker agent
	// session files from causing false "session switch" notifications.
	wolandSessionMarker = ".woland-session"
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
	projectDir  string
	aptPath     string          // apartment root directory (for marker file)
	logger      *log.Logger
	seen        map[string]bool // track seen UUIDs to avoid duplicates
	partialLine string          // buffered incomplete line from previous read
	draining    bool            // when true, parse lines for dedup but don't forward
}

// NewWatcher creates a Watcher that monitors the given Claude Code projects
// directory for session JSONL files.
func NewWatcher(apartmentPath string, logger *log.Logger) *Watcher {
	return &Watcher{
		projectDir: claudeProjectDir(apartmentPath),
		aptPath:    apartmentPath,
		logger:     logger,
		seen:       make(map[string]bool),
	}
}

// claudeProjectDir derives the Claude Code projects directory from an
// apartment path. Delegates to the shared session.ClaudeProjectDir.
func claudeProjectDir(aptPath string) string {
	return session.ClaudeProjectDir(aptPath)
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

			// Drain the startup window: parse lines to populate the seen
			// UUID map for deduplication, but don't forward stale messages.
			w.draining = true
			offset = w.readNewLines(ctx, currentFile, offset, out)
			w.draining = false
		}

		offset = w.readNewLines(ctx, currentFile, offset, out)

		select {
		case <-ctx.Done():
			return
		case <-time.After(pollInterval):
		}
	}
}

// findActiveSession returns the current Woland session file. It first checks
// for a marker file (.woland-session) in the apartment directory. If the
// marker exists and points to a valid file, that file is returned without
// scanning the directory — this prevents worker agent session files from
// causing false session switches. If no marker exists (or it points to a
// missing file), we fall back to finding the most recently modified .jsonl
// file and write the marker to lock onto it.
func (w *Watcher) findActiveSession() (string, error) {
	// If we have an apartment path, check for a marker file that locks
	// onto the current Woland session.
	if w.aptPath != "" {
		markerPath := filepath.Join(w.aptPath, wolandSessionMarker)
		if data, err := os.ReadFile(markerPath); err == nil {
			sessionPath := strings.TrimSpace(string(data))
			if sessionPath != "" {
				if _, err := os.Stat(sessionPath); err == nil {
					return sessionPath, nil
				}
				// Marker points to a missing file — remove it and re-scan.
				w.logger.Printf("marker file points to missing session: %s", sessionPath)
				os.Remove(markerPath)
			}
		}
	}

	// Fall back to scanning for the newest .jsonl file. When aptPath is
	// set, this means no marker was found — Woland may not have started
	// yet, or the marker was cleared due to a stale session file. The
	// scan may pick a standing agent's session file if one was recently
	// modified. Woland startup writes the marker proactively, so this
	// fallback should be rare in normal operation.
	if w.aptPath != "" {
		w.logger.Printf("warning: no Woland session marker found, falling back to newest .jsonl scan")
	}
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

	// Write the marker file so subsequent polls lock onto this session.
	if newest != "" && w.aptPath != "" {
		markerPath := filepath.Join(w.aptPath, wolandSessionMarker)
		if err := os.WriteFile(markerPath, []byte(newest), 0o644); err != nil {
			w.logger.Printf("warning: failed to write session marker: %v", err)
		}
	}

	return newest, nil
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

		if text != "" && !w.draining {
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

