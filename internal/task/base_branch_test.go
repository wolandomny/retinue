package task

import (
	"testing"

	"github.com/wolandomny/retinue/internal/workspace"
)

func TestResolveBaseBranch_Default(t *testing.T) {
	tk := Task{ID: "t1", Repo: "web"}
	repos := map[string]workspace.RepoConfig{
		"web": {Path: "repos/web"},
	}
	got := ResolveBaseBranch(tk, repos)
	if got != "main" {
		t.Errorf("expected 'main', got %q", got)
	}
}

func TestResolveBaseBranch_RepoConfig(t *testing.T) {
	tk := Task{ID: "t1", Repo: "web"}
	repos := map[string]workspace.RepoConfig{
		"web": {Path: "repos/web", BaseBranch: "develop"},
	}
	got := ResolveBaseBranch(tk, repos)
	if got != "develop" {
		t.Errorf("expected 'develop', got %q", got)
	}
}

func TestResolveBaseBranch_TaskOverride(t *testing.T) {
	tk := Task{ID: "t1", Repo: "web", BaseBranch: "feature/auth"}
	repos := map[string]workspace.RepoConfig{
		"web": {Path: "repos/web", BaseBranch: "develop"},
	}
	got := ResolveBaseBranch(tk, repos)
	if got != "feature/auth" {
		t.Errorf("expected 'feature/auth', got %q", got)
	}
}

func TestResolveBaseBranch_NoRepo(t *testing.T) {
	tk := Task{ID: "t1"}
	got := ResolveBaseBranch(tk, nil)
	if got != "main" {
		t.Errorf("expected 'main', got %q", got)
	}
}

func TestResolveBaseBranch_UnknownRepo(t *testing.T) {
	tk := Task{ID: "t1", Repo: "unknown"}
	repos := map[string]workspace.RepoConfig{
		"web": {Path: "repos/web", BaseBranch: "develop"},
	}
	got := ResolveBaseBranch(tk, repos)
	if got != "main" {
		t.Errorf("expected 'main', got %q", got)
	}
}
