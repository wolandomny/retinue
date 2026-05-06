package task

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func TestSkipValidateField_YAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.yaml")
	store := NewFileStore(path)

	// Test task with skip_validate: true
	tasks := []Task{
		{
			ID:           "skip-validation",
			Description:  "Task that skips validation",
			Repo:         "api",
			Status:       StatusPending,
			Prompt:       "Do something",
			SkipValidate: true,
		},
		{
			ID:           "normal-task",
			Description:  "Normal task",
			Repo:         "api",
			Status:       StatusPending,
			Prompt:       "Do something else",
			SkipValidate: false,
		},
	}

	// Save and reload
	if err := store.Save(tasks); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if len(loaded) != 2 {
		t.Fatalf("Load() returned %d tasks, want 2", len(loaded))
	}

	// Check the skip_validate field was preserved
	if loaded[0].SkipValidate != true {
		t.Errorf("loaded[0].SkipValidate = %v, want true", loaded[0].SkipValidate)
	}
	if loaded[1].SkipValidate != false {
		t.Errorf("loaded[1].SkipValidate = %v, want false", loaded[1].SkipValidate)
	}

	// Also test that the YAML file contains the field when set to true
	fileContent, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading file: %v", err)
	}

	if !strings.Contains(string(fileContent), "skip_validate: true") {
		t.Errorf("YAML file should contain 'skip_validate: true', got:\n%s", string(fileContent))
	}
}

// TestConcurrentUpdates verifies that many goroutines can call Update
// concurrently without any updates being lost.
func TestConcurrentUpdates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.yaml")
	store := NewFileStore(path)

	const numTasks = 20

	// Create tasks, all starting as pending.
	tasks := make([]Task, numTasks)
	for i := range tasks {
		tasks[i] = Task{
			ID:     fmt.Sprintf("task-%d", i),
			Status: StatusPending,
		}
	}
	if err := store.Save(tasks); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Concurrently update every task to in_progress.
	var wg sync.WaitGroup
	wg.Add(numTasks)
	for i := 0; i < numTasks; i++ {
		go func(id string) {
			defer wg.Done()
			if err := store.Update(id, func(tk *Task) {
				tk.Status = StatusInProgress
			}); err != nil {
				t.Errorf("Update(%q) error = %v", id, err)
			}
		}(fmt.Sprintf("task-%d", i))
	}
	wg.Wait()

	// Verify all tasks were updated — none should still be pending.
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(loaded) != numTasks {
		t.Fatalf("expected %d tasks, got %d (tasks lost!)", numTasks, len(loaded))
	}
	for _, tk := range loaded {
		if tk.Status != StatusInProgress {
			t.Errorf("task %q status = %q, want %q", tk.ID, tk.Status, StatusInProgress)
		}
	}
}

// TestConcurrentUpdateAndArchive verifies that concurrent Update and
// Archive calls don't clobber each other. This is the exact race
// condition described in the bug: Archive loads tasks, filters, and
// writes back — if an Update happens in between, it gets lost.
func TestConcurrentUpdateAndArchive(t *testing.T) {
	dir := t.TempDir()
	tasksPath := filepath.Join(dir, "tasks.yaml")
	archivePath := filepath.Join(dir, "tasks-archive.yaml")
	store := NewFileStore(tasksPath)

	// Create 10 tasks: task-0 will be archived, the rest will be updated.
	const numTasks = 10
	tasks := make([]Task, numTasks)
	for i := range tasks {
		tasks[i] = Task{
			ID:     fmt.Sprintf("task-%d", i),
			Status: StatusPending,
		}
	}
	if err := store.Save(tasks); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	var wg sync.WaitGroup

	// Archive task-0 in one goroutine.
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := store.Archive("task-0", archivePath); err != nil {
			t.Errorf("Archive() error = %v", err)
		}
	}()

	// Concurrently update all other tasks to done.
	for i := 1; i < numTasks; i++ {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if err := store.Update(id, func(tk *Task) {
				tk.Status = StatusDone
			}); err != nil {
				t.Errorf("Update(%q) error = %v", id, err)
			}
		}(fmt.Sprintf("task-%d", i))
	}

	wg.Wait()

	// Verify: task-0 should be gone from the main file.
	remaining, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	expectedRemaining := numTasks - 1
	if len(remaining) != expectedRemaining {
		t.Fatalf("expected %d remaining tasks, got %d (tasks lost!)", expectedRemaining, len(remaining))
	}

	// All remaining tasks should have status done.
	for _, tk := range remaining {
		if tk.ID == "task-0" {
			t.Errorf("task-0 should have been archived but is still in main file")
		}
		if tk.Status != StatusDone {
			t.Errorf("task %q status = %q, want %q", tk.ID, tk.Status, StatusDone)
		}
	}

	// Verify task-0 is in the archive.
	archiveStore := NewFileStore(archivePath)
	archived, err := archiveStore.Load()
	if err != nil {
		t.Fatalf("loading archive: %v", err)
	}
	if len(archived) != 1 || archived[0].ID != "task-0" {
		t.Errorf("expected archive to contain only task-0, got %v", archived)
	}
}

// TestConcurrentMultipleArchives verifies that archiving multiple tasks
// concurrently doesn't lose any tasks or corrupt the file.
func TestConcurrentMultipleArchives(t *testing.T) {
	dir := t.TempDir()
	tasksPath := filepath.Join(dir, "tasks.yaml")
	archivePath := filepath.Join(dir, "tasks-archive.yaml")
	store := NewFileStore(tasksPath)

	// Create 5 tasks to archive and 5 tasks to keep.
	const total = 10
	const toArchive = 5
	tasks := make([]Task, total)
	for i := range tasks {
		tasks[i] = Task{
			ID:     fmt.Sprintf("task-%d", i),
			Status: StatusDone,
		}
	}
	if err := store.Save(tasks); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Archive the first 5 tasks concurrently.
	var wg sync.WaitGroup
	for i := 0; i < toArchive; i++ {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if err := store.Archive(id, archivePath); err != nil {
				t.Errorf("Archive(%q) error = %v", id, err)
			}
		}(fmt.Sprintf("task-%d", i))
	}
	wg.Wait()

	// Verify remaining tasks.
	remaining, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(remaining) != total-toArchive {
		t.Fatalf("expected %d remaining tasks, got %d", total-toArchive, len(remaining))
	}

	// Verify archived tasks.
	archiveStore := NewFileStore(archivePath)
	archived, err := archiveStore.Load()
	if err != nil {
		t.Fatalf("loading archive: %v", err)
	}
	if len(archived) != toArchive {
		t.Fatalf("expected %d archived tasks, got %d", toArchive, len(archived))
	}
}

func TestEffortField_PreservedThroughRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.yaml")
	store := NewFileStore(path)

	tasks := []Task{
		{ID: "with-effort", Status: StatusPending, Effort: "high"},
		{ID: "no-effort", Status: StatusPending},
	}
	if err := store.Save(tasks); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if loaded[0].Effort != "high" {
		t.Errorf("task[0].Effort = %q, want %q", loaded[0].Effort, "high")
	}
	if loaded[1].Effort != "" {
		t.Errorf("task[1].Effort = %q, want empty", loaded[1].Effort)
	}

	// Effort should be omitted from YAML when empty (omitempty tag).
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading file: %v", err)
	}
	if !strings.Contains(string(content), "effort: high") {
		t.Errorf("expected 'effort: high' in YAML, got:\n%s", string(content))
	}
}

func TestEffortField_InvalidValueRejectedOnLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.yaml")

	raw := `tasks:
  - id: bad-task
    description: A task with a bogus effort
    repo: api
    status: pending
    prompt: do stuff
    effort: ultra
    depends_on: []
    artifacts: []
`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	store := NewFileStore(path)
	_, err := store.Load()
	if err == nil {
		t.Fatal("Load() expected error for invalid effort 'ultra'")
	}
	if !strings.Contains(err.Error(), "ultra") {
		t.Errorf("error should mention 'ultra', got: %v", err)
	}
	if !strings.Contains(err.Error(), "bad-task") {
		t.Errorf("error should mention task id 'bad-task', got: %v", err)
	}
}

func TestEffortField_AllValidValuesAccepted(t *testing.T) {
	for _, level := range []string{"low", "medium", "high", "xhigh", "max"} {
		t.Run(level, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "tasks.yaml")
			store := NewFileStore(path)

			tk := Task{ID: "t-" + level, Status: StatusPending, Effort: level}
			if err := store.Save([]Task{tk}); err != nil {
				t.Fatalf("Save: %v", err)
			}
			loaded, err := store.Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if loaded[0].Effort != level {
				t.Errorf("Effort = %q, want %q", loaded[0].Effort, level)
			}
		})
	}
}
