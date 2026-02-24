package worktree

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

type FakeGit struct {
	Calls []GitCall
	Err   error
}

type GitCall struct {
	Dir  string
	Args []string
}

func (f *FakeGit) Exec(_ context.Context, dir string, args ...string) (string, error) {
	f.Calls = append(f.Calls, GitCall{Dir: dir, Args: args})
	if f.Err != nil {
		return "", f.Err
	}
	return "", nil
}

func TestManagerCreate(t *testing.T) {
	git := &FakeGit{}
	mgr := NewManager(git, "/worktrees")

	path, err := mgr.Create(context.Background(), "/repo", "task-1", "")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if path != "/worktrees/task-1" {
		t.Errorf("path = %q, want %q", path, "/worktrees/task-1")
	}

	if len(git.Calls) != 1 {
		t.Fatalf("expected 1 git call, got %d", len(git.Calls))
	}

	call := git.Calls[0]
	if call.Dir != "/repo" {
		t.Errorf("git dir = %q, want %q", call.Dir, "/repo")
	}

	args := strings.Join(call.Args, " ")
	if !strings.Contains(args, "worktree add") {
		t.Errorf("expected worktree add command, got: %s", args)
	}
	if !strings.Contains(args, "retinue/task-1") {
		t.Errorf("expected branch retinue/task-1, got: %s", args)
	}
}

func TestManagerCreateCustomBranch(t *testing.T) {
	git := &FakeGit{}
	mgr := NewManager(git, "/worktrees")

	_, err := mgr.Create(context.Background(), "/repo", "task-1", "my-branch")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	args := strings.Join(git.Calls[0].Args, " ")
	if !strings.Contains(args, "my-branch") {
		t.Errorf("expected custom branch, got: %s", args)
	}
}

func TestManagerCreateError(t *testing.T) {
	git := &FakeGit{Err: fmt.Errorf("git error")}
	mgr := NewManager(git, "/worktrees")

	_, err := mgr.Create(context.Background(), "/repo", "task-1", "")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestManagerRemove(t *testing.T) {
	git := &FakeGit{}
	mgr := NewManager(git, "/worktrees")

	err := mgr.Remove(context.Background(), "/repo", "task-1")
	if err != nil {
		t.Fatalf("Remove() error = %v", err)
	}

	if len(git.Calls) != 1 {
		t.Fatalf("expected 1 git call, got %d", len(git.Calls))
	}

	args := strings.Join(git.Calls[0].Args, " ")
	if !strings.Contains(args, "worktree remove") {
		t.Errorf("expected worktree remove, got: %s", args)
	}
}
