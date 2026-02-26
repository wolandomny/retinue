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

// FakeManager is an in-memory Manager implementation intended for use in tests.
// All operations are safe for concurrent use.
type FakeManager struct {
	mu       sync.Mutex
	sessions map[string]sessionRecord
}

// NewFakeManager returns a FakeManager with no active sessions.
func NewFakeManager() *FakeManager {
	return &FakeManager{
		sessions: make(map[string]sessionRecord),
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
