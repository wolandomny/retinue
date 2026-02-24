package task

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.yaml")

	store := NewFileStore(path)

	tasks := []Task{
		{
			ID:          "task-1",
			Description: "First task",
			Repo:        "api",
			Status:      StatusPending,
			Prompt:      "Do something",
			DependsOn:   []string{},
			Artifacts:   []string{},
			Meta:        map[string]string{"priority": "high"},
		},
		{
			ID:          "task-2",
			Description: "Second task",
			Repo:        "db",
			Status:      StatusDone,
			Prompt:      "Do another thing",
			DependsOn:   []string{"task-1"},
			Artifacts:   []string{"migration.sql"},
		},
	}

	if err := store.Save(tasks); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if len(loaded) != len(tasks) {
		t.Fatalf("Load() returned %d tasks, want %d", len(loaded), len(tasks))
	}

	for i, got := range loaded {
		want := tasks[i]
		if got.ID != want.ID {
			t.Errorf("task[%d].ID = %q, want %q", i, got.ID, want.ID)
		}
		if got.Status != want.Status {
			t.Errorf("task[%d].Status = %q, want %q", i, got.Status, want.Status)
		}
		if got.Prompt != want.Prompt {
			t.Errorf("task[%d].Prompt = %q, want %q", i, got.Prompt, want.Prompt)
		}
		if got.Repo != want.Repo {
			t.Errorf("task[%d].Repo = %q, want %q", i, got.Repo, want.Repo)
		}
	}
}

func TestFileStoreGet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.yaml")
	store := NewFileStore(path)

	tasks := []Task{
		{ID: "a", Status: StatusPending},
		{ID: "b", Status: StatusDone},
	}
	if err := store.Save(tasks); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, err := store.Get("b")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.ID != "b" {
		t.Errorf("Get() returned task %q, want %q", got.ID, "b")
	}

	_, err = store.Get("nonexistent")
	if err == nil {
		t.Error("Get() expected error for nonexistent task")
	}
}

func TestFileStoreUpdate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.yaml")
	store := NewFileStore(path)

	tasks := []Task{
		{ID: "a", Status: StatusPending},
	}
	if err := store.Save(tasks); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	err := store.Update("a", func(t *Task) {
		t.Status = StatusInProgress
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	got, err := store.Get("a")
	if err != nil {
		t.Fatalf("Get() after update error = %v", err)
	}
	if got.Status != StatusInProgress {
		t.Errorf("status = %q, want %q", got.Status, StatusInProgress)
	}
}

func TestFileStoreLoadMissingFile(t *testing.T) {
	store := NewFileStore("/nonexistent/tasks.yaml")
	_, err := store.Load()
	if err == nil {
		t.Error("Load() expected error for missing file")
	}
}

func TestFileStoreYAMLFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.yaml")

	// Write raw YAML matching the expected format.
	raw := `tasks:
  - id: my-task
    description: A test task
    repo: api
    depends_on: []
    status: pending
    prompt: |
      Do the thing.
    artifacts: []
    meta:
      priority: high
`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	store := NewFileStore(path)
	tasks, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if len(tasks) != 1 {
		t.Fatalf("Load() returned %d tasks, want 1", len(tasks))
	}

	if tasks[0].ID != "my-task" {
		t.Errorf("task.ID = %q, want %q", tasks[0].ID, "my-task")
	}
	if tasks[0].Meta["priority"] != "high" {
		t.Errorf("task.Meta[priority] = %q, want %q", tasks[0].Meta["priority"], "high")
	}
}
