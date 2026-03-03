package cli

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// watchdogConfig holds tunable parameters for the watchdog.
type watchdogConfig struct {
	// PollInterval is how often the watchdog checks each log file.
	PollInterval time.Duration

	// StallTimeout is how long a worker can go without new log output
	// before being considered stalled.
	StallTimeout time.Duration

	// LoopThreshold is the number of identical consecutive lines that
	// indicates a looping worker.
	LoopThreshold int
}

// defaultWatchdogConfig returns sensible defaults.
func defaultWatchdogConfig() watchdogConfig {
	return watchdogConfig{
		PollInterval:  30 * time.Second,
		StallTimeout:  10 * time.Minute,
		LoopThreshold: 20,
	}
}

// watchdogState tracks per-task monitoring state.
type watchdogState struct {
	mu    sync.Mutex
	tasks map[string]*taskWatchState // taskID -> state
}

type taskWatchState struct {
	logFile       string
	lastSize      int64
	lastCheckTime time.Time
	lastLines     []string // ring buffer of recent lines for loop detection
}

func newWatchdogState() *watchdogState {
	return &watchdogState{
		tasks: make(map[string]*taskWatchState),
	}
}

// addTask registers a task for monitoring.
func (w *watchdogState) addTask(taskID, logFile string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.tasks[taskID] = &taskWatchState{
		logFile:       logFile,
		lastCheckTime: time.Now(),
	}
}

// removeTask stops monitoring a task (called when task completes).
func (w *watchdogState) removeTask(taskID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.tasks, taskID)
}

// check examines all tracked tasks and returns a list of taskIDs
// that should be killed, along with the reason.
func (w *watchdogState) check(cfg watchdogConfig) []watchdogAlert {
	w.mu.Lock()
	defer w.mu.Unlock()

	var alerts []watchdogAlert
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
			if reason, looping := checkForLoop(state, newBytes, cfg.LoopThreshold); looping {
				alerts = append(alerts, watchdogAlert{
					taskID: taskID,
					reason: reason,
				})
			}
		} else {
			// No new output — check stall timeout.
			if now.Sub(state.lastCheckTime) > cfg.StallTimeout {
				alerts = append(alerts, watchdogAlert{
					taskID: taskID,
					reason: fmt.Sprintf("no output for %s", now.Sub(state.lastCheckTime).Round(time.Second)),
				})
			}
		}
	}

	return alerts
}

type watchdogAlert struct {
	taskID string
	reason string
}

// checkForLoop reads the tail of the log file and checks whether
// the last N lines are identical (indicating a loop).
func checkForLoop(state *taskWatchState, newBytes int64, threshold int) (string, bool) {
	// Read the tail of the file for new content.
	f, err := os.Open(state.logFile)
	if err != nil {
		return "", false
	}
	defer f.Close()

	// Seek to where we last read.
	offset := state.lastSize - newBytes
	if offset < 0 {
		offset = 0
	}
	buf := make([]byte, newBytes)
	if _, err := f.ReadAt(buf, offset); err != nil {
		return "", false
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
		return "", false
	}
	tail := state.lastLines[len(state.lastLines)-threshold:]
	first := tail[0]
	if first == "" {
		return "", false
	}
	for _, line := range tail[1:] {
		if line != first {
			return "", false
		}
	}

	return fmt.Sprintf("detected loop: %d identical lines", threshold), true
}
