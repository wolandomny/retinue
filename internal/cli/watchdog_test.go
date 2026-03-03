package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWatchdog_StallDetection(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "task.log")

	// Write initial content.
	if err := os.WriteFile(logFile, []byte("starting\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	state := newWatchdogState()
	state.addTask("stall-task", logFile)

	cfg := watchdogConfig{
		PollInterval:  time.Second,
		StallTimeout:  50 * time.Millisecond, // short for test
		LoopThreshold: 20,
	}

	// First check: should record size, no alert.
	alerts := state.check(cfg)
	if len(alerts) != 0 {
		t.Fatalf("expected no alerts on first check, got %d", len(alerts))
	}

	// Wait past stall timeout with no new output.
	time.Sleep(100 * time.Millisecond)

	alerts = state.check(cfg)
	if len(alerts) != 1 {
		t.Fatalf("expected 1 stall alert, got %d", len(alerts))
	}
	if alerts[0].taskID != "stall-task" {
		t.Errorf("expected taskID 'stall-task', got %q", alerts[0].taskID)
	}
	if !strings.Contains(alerts[0].reason, "no output") {
		t.Errorf("expected 'no output' in reason, got %q", alerts[0].reason)
	}
}

func TestWatchdog_LoopDetection(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "task.log")

	// Write initial content.
	if err := os.WriteFile(logFile, []byte("starting\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	state := newWatchdogState()
	state.addTask("loop-task", logFile)

	cfg := watchdogConfig{
		PollInterval:  time.Second,
		StallTimeout:  time.Hour, // don't trigger stall
		LoopThreshold: 5,         // low threshold for test
	}

	// First check to establish baseline size.
	_ = state.check(cfg)

	// Append many identical lines.
	var loopContent strings.Builder
	for i := 0; i < 10; i++ {
		loopContent.WriteString(`{"type":"content_block_delta","delta":"thinking"}` + "\n")
	}
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(loopContent.String())
	f.Close()

	alerts := state.check(cfg)
	if len(alerts) != 1 {
		t.Fatalf("expected 1 loop alert, got %d", len(alerts))
	}
	if !strings.Contains(alerts[0].reason, "loop") {
		t.Errorf("expected 'loop' in reason, got %q", alerts[0].reason)
	}
}

func TestWatchdog_NoAlertOnProgress(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "task.log")

	if err := os.WriteFile(logFile, []byte("line1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	state := newWatchdogState()
	state.addTask("ok-task", logFile)

	cfg := watchdogConfig{
		PollInterval:  time.Second,
		StallTimeout:  50 * time.Millisecond,
		LoopThreshold: 20,
	}

	// First check.
	_ = state.check(cfg)

	// Add new, varied output before the stall timeout.
	time.Sleep(30 * time.Millisecond)
	f, _ := os.OpenFile(logFile, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("line2 different content\nline3 more content\n")
	f.Close()

	alerts := state.check(cfg)
	if len(alerts) != 0 {
		t.Fatalf("expected no alerts for active worker, got %d", len(alerts))
	}
}

func TestWatchdog_RemoveTask(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "task.log")
	os.WriteFile(logFile, []byte("x\n"), 0o644)

	state := newWatchdogState()
	state.addTask("t1", logFile)
	state.removeTask("t1")

	cfg := defaultWatchdogConfig()
	cfg.StallTimeout = time.Millisecond
	time.Sleep(5 * time.Millisecond)

	alerts := state.check(cfg)
	if len(alerts) != 0 {
		t.Fatalf("expected no alerts after remove, got %d", len(alerts))
	}
}

func TestWatchdog_MissingLogFile(t *testing.T) {
	state := newWatchdogState()
	state.addTask("no-file", "/tmp/nonexistent-watchdog-test.log")

	cfg := defaultWatchdogConfig()
	cfg.StallTimeout = time.Millisecond
	time.Sleep(5 * time.Millisecond)

	// Should not alert — file may not exist yet during startup.
	alerts := state.check(cfg)
	if len(alerts) != 0 {
		t.Fatalf("expected no alerts for missing log file, got %d", len(alerts))
	}
}
