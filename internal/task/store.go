package task

import (
	"fmt"
	"os"
	"sync"

	"gopkg.in/yaml.v3"
)

// FileStore persists tasks to a YAML file on disk. All operations
// (Load, Save, Get, Update, Archive) are protected by a RWMutex so
// that compound Load→Modify→Save cycles are atomic. Read-only
// operations (Load, Get) take a read lock; mutating operations
// (Save, Update, Archive) take a write lock.
type FileStore struct {
	Path string
	mu   sync.RWMutex
}

func NewFileStore(path string) *FileStore {
	return &FileStore{Path: path}
}

// load reads and parses the tasks file without acquiring any lock.
// Callers must hold at least a read lock.
func (s *FileStore) load() ([]Task, error) {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return nil, fmt.Errorf("reading tasks file: %w", err)
	}

	var tf TaskFile
	if err := yaml.Unmarshal(data, &tf); err != nil {
		return nil, fmt.Errorf("parsing tasks file: %w", err)
	}

	if err := validateFields(tf.Tasks); err != nil {
		return nil, err
	}

	return tf.Tasks, nil
}

// save writes tasks to disk without acquiring any lock.
// Callers must hold the write lock.
func (s *FileStore) save(tasks []Task) error {
	tf := TaskFile{Tasks: tasks}

	data, err := yaml.Marshal(&tf)
	if err != nil {
		return fmt.Errorf("marshaling tasks: %w", err)
	}

	if err := os.WriteFile(s.Path, data, 0o644); err != nil {
		return fmt.Errorf("writing tasks file: %w", err)
	}

	return nil
}

// Load reads all tasks from the YAML file. It acquires a read lock to
// prevent reading a partially-written file.
func (s *FileStore) Load() ([]Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.load()
}

// Save writes all tasks to the YAML file. It acquires a write lock to
// prevent concurrent writes.
func (s *FileStore) Save(tasks []Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.save(tasks)
}

// Get returns a single task by ID. It acquires a read lock.
func (s *FileStore) Get(id string) (*Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tasks, err := s.load()
	if err != nil {
		return nil, err
	}

	for i := range tasks {
		if tasks[i].ID == id {
			return &tasks[i], nil
		}
	}

	return nil, fmt.Errorf("task %q not found", id)
}

// Archive removes a task from the main tasks file and appends it
// to the archive file. The task is saved with its final state.
// It acquires a write lock for the entire Load→Modify→Save cycle
// so that concurrent Updates cannot be clobbered.
func (s *FileStore) Archive(id string, archivePath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Load current tasks.
	tasks, err := s.load()
	if err != nil {
		return fmt.Errorf("loading tasks for archive: %w", err)
	}

	// Find and remove the task.
	var archived *Task
	var remaining []Task
	for i := range tasks {
		if tasks[i].ID == id {
			archived = &tasks[i]
		} else {
			remaining = append(remaining, tasks[i])
		}
	}

	if archived == nil {
		return fmt.Errorf("task %q not found for archiving", id)
	}

	// Append to archive file.
	var archiveTasks []Task
	if data, err := os.ReadFile(archivePath); err == nil {
		var tf TaskFile
		if parseErr := yaml.Unmarshal(data, &tf); parseErr == nil {
			archiveTasks = tf.Tasks
		}
	}
	// If file doesn't exist or can't be parsed, start fresh.

	archiveTasks = append(archiveTasks, *archived)

	archiveData, err := yaml.Marshal(&TaskFile{Tasks: archiveTasks})
	if err != nil {
		return fmt.Errorf("marshaling archive: %w", err)
	}
	if err := os.WriteFile(archivePath, archiveData, 0o644); err != nil {
		return fmt.Errorf("writing archive: %w", err)
	}

	// Save remaining tasks back to the main file.
	return s.save(remaining)
}

// Update applies fn to the task with the given ID, then writes all
// tasks back to disk. It acquires a write lock for the entire
// Load→Modify→Save cycle.
func (s *FileStore) Update(id string, fn func(*Task)) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tasks, err := s.load()
	if err != nil {
		return err
	}

	for i := range tasks {
		if tasks[i].ID == id {
			fn(&tasks[i])
			return s.save(tasks)
		}
	}

	return fmt.Errorf("task %q not found", id)
}
