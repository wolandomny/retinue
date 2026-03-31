package bus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"syscall"
	"time"
)

const (
	// pollInterval is how often Tail checks the file for new content.
	pollInterval = 500 * time.Millisecond
)

// Bus provides append-only access to a JSONL message file that serves as
// the shared communication channel between standing agents and the user.
type Bus struct {
	Path string // path to messages.jsonl
}

// New creates a Bus that reads and writes to the given JSONL file path.
func New(path string) *Bus {
	return &Bus{Path: path}
}

// Append marshals msg to JSON and appends it as a single line to the bus file.
// File locking (flock LOCK_EX) prevents concurrent write corruption.
func (b *Bus) Append(msg Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshaling bus message: %w", err)
	}
	data = append(data, '\n')

	f, err := os.OpenFile(b.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("opening bus file: %w", err)
	}
	defer f.Close()

	// Acquire an exclusive lock to serialize concurrent appends.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("locking bus file: %w", err)
	}

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("writing bus message: %w", err)
	}

	return nil
}

// ReadRecent reads the JSONL file and returns the last n messages.
// If fewer than n messages exist, all messages are returned.
// If the file does not exist, an empty slice is returned (not an error).
func (b *Bus) ReadRecent(n int) ([]Message, error) {
	data, err := os.ReadFile(b.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return []Message{}, nil
		}
		return nil, fmt.Errorf("reading bus file: %w", err)
	}

	var messages []Message
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var msg Message
		if err := json.Unmarshal(line, &msg); err != nil {
			continue // skip malformed lines
		}
		messages = append(messages, msg)
	}

	if n >= len(messages) {
		return messages, nil
	}
	return messages[len(messages)-n:], nil
}

// Tail returns a channel that emits new messages as they are appended to
// the bus file. A background goroutine polls the file every 500ms for new
// content. The channel is closed when ctx is cancelled. If the file does
// not yet exist, Tail waits for it to appear.
func (b *Bus) Tail(ctx context.Context) (<-chan Message, error) {
	out := make(chan Message, 16)

	go func() {
		defer close(out)
		b.tailLoop(ctx, out)
	}()

	return out, nil
}

// tailLoop is the internal polling loop for Tail.
func (b *Bus) tailLoop(ctx context.Context, out chan<- Message) {
	var offset int64
	var partialLine string
	fileExists := false

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Wait for the file to appear.
		if !fileExists {
			if _, err := os.Stat(b.Path); err != nil {
				select {
				case <-ctx.Done():
					return
				case <-time.After(pollInterval):
					continue
				}
			}
			fileExists = true
			offset = 0
			partialLine = ""
		}

		info, err := os.Stat(b.Path)
		if err != nil {
			// File may have been removed; wait for it to reappear.
			fileExists = false
			continue
		}

		// Handle file truncation.
		if info.Size() < offset {
			offset = 0
			partialLine = ""
		}

		// No new data.
		if info.Size() == offset {
			select {
			case <-ctx.Done():
				return
			case <-time.After(pollInterval):
				continue
			}
		}

		// Read new data from the last offset.
		offset, partialLine = b.readNewLines(ctx, offset, partialLine, out)

		select {
		case <-ctx.Done():
			return
		case <-time.After(pollInterval):
		}
	}
}

// readNewLines reads bytes appended after offset, parses complete JSONL lines,
// and sends parsed messages to out. Partial lines (data without a trailing
// newline) are buffered and returned for the next call. Returns the updated
// file offset and any buffered partial line.
func (b *Bus) readNewLines(ctx context.Context, offset int64, partialLine string, out chan<- Message) (int64, string) {
	f, err := os.Open(b.Path)
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

	// Prepend any buffered partial line from the previous read.
	var data []byte
	if partialLine != "" {
		data = make([]byte, len(partialLine)+len(newData))
		copy(data, partialLine)
		copy(data[len(partialLine):], newData)
		partialLine = ""
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
			partialLine = string(data[pos:])
			break
		}

		line := bytes.TrimSpace(data[pos : pos+nlIdx])
		pos += nlIdx + 1

		if len(line) == 0 {
			continue
		}

		var msg Message
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}

		select {
		case out <- msg:
		case <-ctx.Done():
			return offset + bytesRead, partialLine
		}
	}

	return offset + bytesRead, partialLine
}
