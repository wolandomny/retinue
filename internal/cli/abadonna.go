package cli

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// abadonnaConfig holds tunable parameters for Abadonna, the silent
// monitor that watches worker logs and kills stalled or looping agents.
type abadonnaConfig struct {
	// PollInterval is how often Abadonna checks each log file.
	PollInterval time.Duration

	// StallTimeout is how long a worker can go without new log output
	// before being considered stalled.
	StallTimeout time.Duration

	// LoopThreshold is the number of identical consecutive lines that
	// indicates a looping worker.
	LoopThreshold int
}

// defaultAbadonnaConfig returns sensible defaults.
func defaultAbadonnaConfig() abadonnaConfig {
	return abadonnaConfig{
		PollInterval:  30 * time.Second,
		StallTimeout:  10 * time.Minute,
		LoopThreshold: 20,
	}
}

// abadonnaState tracks per-task monitoring state.
type abadonnaState struct {
	mu    sync.Mutex
	tasks map[string]*taskWatchState // taskID -> state
}

type taskWatchState struct {
	logFile       string
	lastSize      int64
	lastCheckTime time.Time
	lastLines     []string // ring buffer of recent lines for loop detection
}

func newAbadonnaState() *abadonnaState {
	return &abadonnaState{
		tasks: make(map[string]*taskWatchState),
	}
}

// addTask registers a task for monitoring.
func (w *abadonnaState) addTask(taskID, logFile string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.tasks[taskID] = &taskWatchState{
		logFile:       logFile,
		lastCheckTime: time.Now(),
	}
}

// removeTask stops monitoring a task (called when task completes).
func (w *abadonnaState) removeTask(taskID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.tasks, taskID)
}

// check examines all tracked tasks and returns a list of taskIDs
// that should be killed, along with the reason.
func (w *abadonnaState) check(cfg abadonnaConfig) []abadonnaAlert {
	w.mu.Lock()
	defer w.mu.Unlock()

	var alerts []abadonnaAlert
	now := time.Now()

	for taskID, state := range w.tasks {
		info, err := os.Stat(state.logFile)
		if err != nil {
			// Log file doesn't exist yet — worker may still be starting.
			continue
		}

		currentSize := info.Size()

		if currentSize > state.lastSize {
			// New output — check for loops, reset stall timer.
			state.lastCheckTime = now
			newBytes := currentSize - state.lastSize
			state.lastSize = currentSize

			// Read the new bytes for loop detection.
			if reason, repeatedLine, looping := checkForLoop(state, newBytes, cfg.LoopThreshold); looping {
				alerts = append(alerts, abadonnaAlert{
					taskID:  taskID,
					reason:  reason,
					context: "repeated: " + repeatedLine,
				})
			}
		} else {
			// No new output — check stall timeout.
			if now.Sub(state.lastCheckTime) > cfg.StallTimeout {
				logCtx := tailLogFile(state.logFile, 3)
				alerts = append(alerts, abadonnaAlert{
					taskID:  taskID,
					reason:  fmt.Sprintf("no output for %s", now.Sub(state.lastCheckTime).Round(time.Second)),
					context: logCtx,
				})
			}
		}
	}

	return alerts
}

type abadonnaAlert struct {
	taskID  string
	reason  string
	context string // last few lines of worker output for diagnostics
}

// tailLogFile reads the last n non-empty lines from a log file.
// Returns an empty string if the file can't be read.
func tailLogFile(logFile string, n int) string {
	data, err := os.ReadFile(logFile)
	if err != nil {
		return ""
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")

	// Filter to non-empty lines and take the last n.
	var nonEmpty []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			nonEmpty = append(nonEmpty, trimmed)
		}
	}

	if len(nonEmpty) == 0 {
		return ""
	}

	start := len(nonEmpty) - n
	if start < 0 {
		start = 0
	}
	tail := nonEmpty[start:]

	// Truncate each line to 200 chars.
	var result []string
	for _, line := range tail {
		if len(line) > 200 {
			line = line[:200] + "..."
		}
		result = append(result, line)
	}

	return strings.Join(result, "\n")
}

// checkForLoop reads the tail of the log file and checks whether
// the last N lines are identical (indicating a loop).
func checkForLoop(state *taskWatchState, newBytes int64, threshold int) (string, string, bool) {
	// Read the tail of the file for new content.
	f, err := os.Open(state.logFile)
	if err != nil {
		return "", "", false
	}
	defer f.Close()

	// Seek to where we last read.
	offset := state.lastSize - newBytes
	if offset < 0 {
		offset = 0
	}
	buf := make([]byte, newBytes)
	if _, err := f.ReadAt(buf, offset); err != nil {
		return "", "", false
	}

	// Split into lines and update ring buffer.
	newLines := strings.Split(strings.TrimSpace(string(buf)), "\n")
	state.lastLines = append(state.lastLines, newLines...)

	// Keep only the last 2*threshold lines.
	maxKeep := threshold * 2
	if len(state.lastLines) > maxKeep {
		state.lastLines = state.lastLines[len(state.lastLines)-maxKeep:]
	}

	// Check if the last `threshold` lines are all identical.
	if len(state.lastLines) < threshold {
		return "", "", false
	}
	tail := state.lastLines[len(state.lastLines)-threshold:]
	first := tail[0]
	if first == "" {
		return "", "", false
	}
	for _, line := range tail[1:] {
		if line != first {
			return "", "", false
		}
	}

	repeated := first
	if len(repeated) > 200 {
		repeated = repeated[:200] + "..."
	}
	return fmt.Sprintf("detected loop: %d identical lines", threshold), repeated, true
}
