package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTailLogFile_Basic(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "test.log")

	content := "line1\nline2\nline3\nline4\nline5\n"
	if err := os.WriteFile(logFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	got := tailLogFile(logFile, 3)
	if !strings.Contains(got, "line3") || !strings.Contains(got, "line4") || !strings.Contains(got, "line5") {
		t.Errorf("expected last 3 lines, got: %q", got)
	}
	if strings.Contains(got, "line2") {
		t.Errorf("should not contain line2, got: %q", got)
	}
}

func TestTailLogFile_SkipsEmptyLines(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "test.log")

	content := "line1\n\n\nline2\n\nline3\n\n"
	if err := os.WriteFile(logFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	got := tailLogFile(logFile, 2)
	if !strings.Contains(got, "line2") || !strings.Contains(got, "line3") {
		t.Errorf("expected line2 and line3, got: %q", got)
	}
}

func TestTailLogFile_TruncatesLongLines(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "test.log")

	longLine := strings.Repeat("x", 300)
	if err := os.WriteFile(logFile, []byte(longLine+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := tailLogFile(logFile, 1)
	if len(got) > 210 {
		t.Errorf("expected truncated line, got length %d", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("expected ... suffix, got: %q", got[len(got)-10:])
	}
}

func TestTailLogFile_NonExistentFile(t *testing.T) {
	got := tailLogFile("/nonexistent/file.log", 3)
	if got != "" {
		t.Errorf("expected empty string for nonexistent file, got: %q", got)
	}
}

func TestCheckForLoop_ReturnValues(t *testing.T) {
	// Verify the three-return-value signature compiles correctly.
	state := &taskWatchState{
		logFile:  "/nonexistent",
		lastSize: 0,
	}
	reason, repeated, looping := checkForLoop(state, 0, 20)
	if looping {
		t.Error("expected no loop for empty state")
	}
	_ = reason
	_ = repeated
}
