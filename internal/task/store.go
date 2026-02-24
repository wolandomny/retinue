package task

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

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
