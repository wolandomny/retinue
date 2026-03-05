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

func TestArchive(t *testing.T) {
	dir := t.TempDir()
	tasksPath := filepath.Join(dir, "tasks.yaml")
	archivePath := filepath.Join(dir, "tasks-archive.yaml")

	// Create initial tasks file with two tasks.
	store := NewFileStore(tasksPath)
	tasks := []Task{
		{ID: "task-a", Status: StatusMerged, Description: "First task"},
		{ID: "task-b", Status: StatusPending, Description: "Second task"},
	}
	if err := store.Save(tasks); err != nil {
		t.Fatal(err)
	}

	// Archive task-a.
	if err := store.Archive("task-a", archivePath); err != nil {
		t.Fatalf("Archive failed: %v", err)
	}

	// Verify task-a is gone from main file.
	remaining, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 {
		t.Fatalf("expected 1 remaining task, got %d", len(remaining))
	}
	if remaining[0].ID != "task-b" {
		t.Errorf("expected task-b to remain, got %q", remaining[0].ID)
	}

	// Verify task-a is in archive.
	archiveStore := NewFileStore(archivePath)
	archived, err := archiveStore.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(archived) != 1 {
		t.Fatalf("expected 1 archived task, got %d", len(archived))
	}
	if archived[0].ID != "task-a" {
		t.Errorf("expected task-a in archive, got %q", archived[0].ID)
	}
}

func TestArchive_AppendsToExisting(t *testing.T) {
	dir := t.TempDir()
	tasksPath := filepath.Join(dir, "tasks.yaml")
	archivePath := filepath.Join(dir, "tasks-archive.yaml")

	// Pre-populate archive with one task.
	archiveStore := NewFileStore(archivePath)
	archiveStore.Save([]Task{
		{ID: "old-task", Status: StatusMerged, Description: "Old task"},
	})

	// Create main file with a task to archive.
	store := NewFileStore(tasksPath)
	store.Save([]Task{
		{ID: "new-task", Status: StatusMerged, Description: "New task"},
	})

	if err := store.Archive("new-task", archivePath); err != nil {
		t.Fatal(err)
	}

	// Archive should now have both tasks.
	archived, err := archiveStore.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(archived) != 2 {
		t.Fatalf("expected 2 archived tasks, got %d", len(archived))
	}
	if archived[0].ID != "old-task" || archived[1].ID != "new-task" {
		t.Errorf("unexpected archive order: %s, %s", archived[0].ID, archived[1].ID)
	}
}

func TestArchive_NotFound(t *testing.T) {
	dir := t.TempDir()
	tasksPath := filepath.Join(dir, "tasks.yaml")
	archivePath := filepath.Join(dir, "tasks-archive.yaml")

	store := NewFileStore(tasksPath)
	store.Save([]Task{
		{ID: "task-a", Status: StatusPending},
	})

	err := store.Archive("nonexistent", archivePath)
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestArchive_EmptyRemaining(t *testing.T) {
	dir := t.TempDir()
	tasksPath := filepath.Join(dir, "tasks.yaml")
	archivePath := filepath.Join(dir, "tasks-archive.yaml")

	store := NewFileStore(tasksPath)
	store.Save([]Task{
		{ID: "only-task", Status: StatusMerged},
	})

	if err := store.Archive("only-task", archivePath); err != nil {
		t.Fatal(err)
	}

	remaining, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Errorf("expected 0 remaining tasks, got %d", len(remaining))
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
