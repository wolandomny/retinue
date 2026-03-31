package standing

import (
	"fmt"
	"os"
	"sync"

	"gopkg.in/yaml.v3"
)

// FileStore persists standing agent definitions to a YAML file on disk.
// All operations (Load, Save, Get, Update) are protected by a RWMutex
// so that compound Load→Modify→Save cycles are atomic. Read-only
// operations (Load, Get) take a read lock; mutating operations
// (Save, Update) take a write lock.
type FileStore struct {
	Path string
	mu   sync.RWMutex
}

func NewFileStore(path string) *FileStore {
	return &FileStore{Path: path}
}

// load reads and parses the agents file without acquiring any lock.
// Callers must hold at least a read lock.
func (s *FileStore) load() ([]Agent, error) {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return nil, fmt.Errorf("reading agents file: %w", err)
	}

	var af AgentFile
	if err := yaml.Unmarshal(data, &af); err != nil {
		return nil, fmt.Errorf("parsing agents file: %w", err)
	}

	return af.Agents, nil
}

// save writes agents to disk without acquiring any lock.
// Callers must hold the write lock.
func (s *FileStore) save(agents []Agent) error {
	af := AgentFile{Agents: agents}

	data, err := yaml.Marshal(&af)
	if err != nil {
		return fmt.Errorf("marshaling agents: %w", err)
	}

	if err := os.WriteFile(s.Path, data, 0o644); err != nil {
		return fmt.Errorf("writing agents file: %w", err)
	}

	return nil
}

// Load reads all agents from the YAML file. It acquires a read lock to
// prevent reading a partially-written file.
func (s *FileStore) Load() ([]Agent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.load()
}

// Save writes all agents to the YAML file. It acquires a write lock to
// prevent concurrent writes.
func (s *FileStore) Save(agents []Agent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.save(agents)
}

// Get returns a single agent by ID. It acquires a read lock.
func (s *FileStore) Get(id string) (*Agent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	agents, err := s.load()
	if err != nil {
		return nil, err
	}

	for i := range agents {
		if agents[i].ID == id {
			return &agents[i], nil
		}
	}

	return nil, fmt.Errorf("agent %q not found", id)
}

// Update applies fn to the agent with the given ID, then writes all
// agents back to disk. It acquires a write lock for the entire
// Load→Modify→Save cycle.
func (s *FileStore) Update(id string, fn func(*Agent)) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	agents, err := s.load()
	if err != nil {
		return err
	}

	for i := range agents {
		if agents[i].ID == id {
			fn(&agents[i])
			return s.save(agents)
		}
	}

	return fmt.Errorf("agent %q not found", id)
}
