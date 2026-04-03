package session

import (
	"os"
	"path/filepath"
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
