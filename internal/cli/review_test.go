package cli

import (
	"context"
	"testing"

	"github.com/wolandomny/retinue/internal/task"
)

func TestRunGit_Success(t *testing.T) {
	out, err := runGit(context.Background(), ".", "--version")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if out == "" {
		t.Fatal("expected non-empty output from git --version")
	}
}

func TestRunGit_Failure(t *testing.T) {
	_, err := runGit(context.Background(), ".", "nonexistent-subcommand")
	if err == nil {
		t.Fatal("expected error for invalid git subcommand")
	}
	if got := err.Error(); got == "" {
		t.Fatal("expected non-empty error message")
	}
}

func TestIsApproved(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect bool
	}{
		{"exact", "APPROVED", true},
		{"with message", "APPROVED\nLooks good", true},
		{"leading whitespace", "  APPROVED  ", true},
		{"rejected", "REJECTED\nbad code", false},
		{"empty", "", false},
		{"lowercase", "approved", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isApproved(tt.input)
			if got != tt.expect {
				t.Errorf("isApproved(%q) = %v, want %v", tt.input, got, tt.expect)
			}
		})
	}
}

func TestFindReviewable(t *testing.T) {
	tests := []struct {
		name   string
		tasks  []task.Task
		expect *string // nil means no task found, otherwise the expected task ID
	}{
		{
			name:   "no tasks",
			tasks:  nil,
			expect: nil,
		},
		{
			name: "wrong status",
			tasks: []task.Task{
				{ID: "a", Status: task.StatusPending, Branch: "retinue/a", Repo: "myrepo"},
				{ID: "b", Status: task.StatusInProgress, Branch: "retinue/b", Repo: "myrepo"},
			},
			expect: nil,
		},
		{
			name: "done but no branch",
			tasks: []task.Task{
				{ID: "a", Status: task.StatusDone, Branch: "", Repo: "myrepo"},
			},
			expect: nil,
		},
		{
			name: "done but no repo",
			tasks: []task.Task{
				{ID: "a", Status: task.StatusDone, Branch: "retinue/a", Repo: ""},
			},
			expect: nil,
		},
		{
			name: "done with branch and repo",
			tasks: []task.Task{
				{ID: "skip", Status: task.StatusPending, Branch: "retinue/skip", Repo: "myrepo"},
				{ID: "target", Status: task.StatusDone, Branch: "retinue/target", Repo: "myrepo"},
				{ID: "other", Status: task.StatusDone, Branch: "retinue/other", Repo: "myrepo"},
			},
			expect: strPtr("target"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findReviewable(tt.tasks)
			if tt.expect == nil {
				if got != nil {
					t.Errorf("expected nil, got task %q", got.ID)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected task %q, got nil", *tt.expect)
			}
			if got.ID != *tt.expect {
				t.Errorf("expected task %q, got %q", *tt.expect, got.ID)
			}
		})
	}
}

func strPtr(s string) *string {
	return &s
}
