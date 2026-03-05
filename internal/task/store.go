package task

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// FileStore persists tasks to a YAML file on disk. All operations
// (Load, Save, Get, Update) are atomic at the file level — each call
// reads or rewrites the entire file.
type FileStore struct {
	Path string
}

func NewFileStore(path string) *FileStore {
	return &FileStore{Path: path}
}

func (s *FileStore) Load() ([]Task, error) {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return nil, fmt.Errorf("reading tasks file: %w", err)
	}

	var tf TaskFile
	if err := yaml.Unmarshal(data, &tf); err != nil {
		return nil, fmt.Errorf("parsing tasks file: %w", err)
	}

	return tf.Tasks, nil
}

func (s *FileStore) Save(tasks []Task) error {
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

func (s *FileStore) Get(id string) (*Task, error) {
	tasks, err := s.Load()
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
func (s *FileStore) Archive(id string, archivePath string) error {
	// Load current tasks.
	tasks, err := s.Load()
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
	return s.Save(remaining)
}

func (s *FileStore) Update(id string, fn func(*Task)) error {
	tasks, err := s.Load()
	if err != nil {
		return err
	}

	for i := range tasks {
		if tasks[i].ID == id {
			fn(&tasks[i])
			return s.Save(tasks)
		}
	}

	return fmt.Errorf("task %q not found", id)
}
