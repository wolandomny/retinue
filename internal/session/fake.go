package session

import (
	"context"
	"fmt"
	"sync"
)

// sessionRecord holds the metadata stored for a fake session.
type sessionRecord struct {
	workDir string
	command string
}

// windowRecord holds the metadata stored for a fake window.
type windowRecord struct {
	workDir string
	command string
}

// FakeManager is an in-memory Manager implementation intended for use in tests.
// All operations are safe for concurrent use.
type FakeManager struct {
	mu       sync.Mutex
	sessions map[string]sessionRecord
	windows  map[string]map[string]windowRecord // session -> window -> record
}

// NewFakeManager returns a FakeManager with no active sessions.
func NewFakeManager() *FakeManager {
	return &FakeManager{
		sessions: make(map[string]sessionRecord),
		windows:  make(map[string]map[string]windowRecord),
	}
}

// Create records a new session. Returns an error if a session with that name
// already exists, mirroring tmux behaviour.
func (f *FakeManager) Create(_ context.Context, name, workDir, command string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, exists := f.sessions[name]; exists {
		return fmt.Errorf("session %q already exists", name)
	}
	f.sessions[name] = sessionRecord{workDir: workDir, command: command}
	return nil
}

// Kill removes the named session. Returns nil whether or not the session exists,
// matching the real TmuxManager behaviour.
func (f *FakeManager) Kill(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	delete(f.sessions, name)
	return nil
}

// Exists reports whether the named session is currently recorded.
func (f *FakeManager) Exists(_ context.Context, name string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	_, ok := f.sessions[name]
	return ok, nil
}

// Wait returns nil immediately; the fake manager does not model session
// duration.
func (f *FakeManager) Wait(_ context.Context, _ string) error {
	return nil
}

// Command returns the command string recorded for the named session, or an
// empty string if the session does not exist. Intended for use in tests.
func (f *FakeManager) Command(name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.sessions[name].command
}

// CreateWindow records a new window in the given session. If the session doesn't
// exist, it creates the session with this window as the first window.
func (f *FakeManager) CreateWindow(_ context.Context, session, window, workDir, command string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	wins, ok := f.windows[session]
	if !ok {
		wins = make(map[string]windowRecord)
		f.windows[session] = wins
		f.sessions[session] = sessionRecord{workDir: workDir}
	}
	if _, exists := wins[window]; exists {
		return fmt.Errorf("window %q already exists in session %q", window, session)
	}
	wins[window] = windowRecord{workDir: workDir, command: command}
	return nil
}

// KillWindow removes the named window. Returns nil whether or not the window
// exists, matching the real TmuxManager behaviour.
func (f *FakeManager) KillWindow(_ context.Context, session, window string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if wins, ok := f.windows[session]; ok {
		delete(wins, window)
	}
	return nil
}

// HasWindow reports whether a window with the given name exists in the session.
func (f *FakeManager) HasWindow(_ context.Context, session, window string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	wins, ok := f.windows[session]
	if !ok {
		return false, nil
	}
	_, exists := wins[window]
	return exists, nil
}

// ListWindows returns the names of all windows in the given session.
// Returns nil if the session has no windows.
func (f *FakeManager) ListWindows(_ context.Context, session string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	wins, ok := f.windows[session]
	if !ok {
		return nil, nil
	}
	names := make([]string, 0, len(wins))
	for name := range wins {
		names = append(names, name)
	}
	return names, nil
}

// WindowCommand returns the command string recorded for the named window, or
// an empty string if not found. Intended for tests.
func (f *FakeManager) WindowCommand(session, window string) string {
	f.mu.Lock()
	defer f.mu.Unlock()

	wins, ok := f.windows[session]
	if !ok {
		return ""
	}
	return wins[window].command
}
