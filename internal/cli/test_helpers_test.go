package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wolandomny/retinue/internal/task"
)

// initTestRepo creates a bare-minimum git repo in a temp directory
// with one commit on main. It returns the repo path.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()

	cmds := [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "Test"},
	}
	for _, args := range cmds {
		if _, err := runGit(ctx, dir, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}

	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, dir, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, dir, "commit", "-m", "initial"); err != nil {
		t.Fatal(err)
	}

	return dir
}

// writeTasks creates a tasks.yaml with the given tasks and returns
// a FileStore.
func writeTasks(t *testing.T, tasks []task.Task) *task.FileStore {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.yaml")
	store := task.NewFileStore(path)
	if err := store.Save(tasks); err != nil {
		t.Fatalf("saving tasks: %v", err)
	}
	return store
}
