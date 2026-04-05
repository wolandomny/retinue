package session

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ClaudeProjectDir returns the Claude Code projects directory for an apartment path.
// Claude mangles the path by replacing "/" and "." with "-":
//
//	/Users/foo/apt → ~/.claude/projects/-Users-foo-apt/
func ClaudeProjectDir(aptPath string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}
	mangled := strings.ReplaceAll(aptPath, "/", "-")
	mangled = strings.ReplaceAll(mangled, ".", "-")
	return filepath.Join(home, ".claude", "projects", mangled)
}

// NewestJSONLFile returns the most recently modified .jsonl file in a directory,
// or "" if none exist or the directory cannot be read.
func NewestJSONLFile(dir string) string {
	files := SortedJSONLFiles(dir)
	if len(files) == 0 {
		return ""
	}
	return files[0]
}

// SortedJSONLFiles returns all .jsonl files in a directory sorted by
// modification time (newest first). Returns nil if the directory cannot
// be read or contains no .jsonl files.
func SortedJSONLFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	type fileWithTime struct {
		path    string
		modTime time.Time
	}

	var files []fileWithTime
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, fileWithTime{
			path:    filepath.Join(dir, entry.Name()),
			modTime: info.ModTime(),
		})
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.After(files[j].modTime)
	})

	result := make([]string, len(files))
	for i, f := range files {
		result[i] = f.path
	}
	return result
}

// SnapshotJSONLFiles returns a set of all current .jsonl files in a directory.
func SnapshotJSONLFiles(dir string) map[string]bool {
	files := make(map[string]bool)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return files
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".jsonl") {
			files[filepath.Join(dir, entry.Name())] = true
		}
	}
	return files
}

// WaitForNewJSONL watches the Claude projects directory for a new .jsonl file
// that doesn't exist in the provided snapshot of existing files. Returns the
// path of the new file, or empty string if timeout is reached.
func WaitForNewJSONL(dir string, existingFiles map[string]bool, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
				continue
			}
			fullPath := filepath.Join(dir, entry.Name())
			if !existingFiles[fullPath] {
				return fullPath // This file is NEW
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return ""
}

// RefreshSessionMarker checks if a session marker file points to a valid,
// recently-modified session file. If the marker is missing, stale (>5 minutes),
// or points to a deleted file, it updates the marker to point to the newest
// .jsonl file in the Claude projects directory.
func RefreshSessionMarker(aptPath, markerName string) error {
	markerPath := filepath.Join(aptPath, markerName)
	projDir := ClaudeProjectDir(aptPath)

	// Check if marker exists and points to a valid file
	if data, err := os.ReadFile(markerPath); err == nil {
		sessionPath := strings.TrimSpace(string(data))
		if sessionPath != "" {
			if info, err := os.Stat(sessionPath); err == nil {
				// File exists - check if it's been modified recently (within 5 minutes)
				if time.Since(info.ModTime()) <= 5*time.Minute {
					return nil // Marker is valid and recent
				}
				// File is stale - continue to refresh logic
			}
			// File doesn't exist anymore - continue to refresh logic
		}
	}

	// Marker is missing, stale, or points to deleted file - find newest .jsonl
	newest := NewestJSONLFile(projDir)
	if newest == "" {
		return nil // No .jsonl files available
	}

	// Update marker to point to newest file
	return os.WriteFile(markerPath, []byte(newest), 0o644)
}
