package workspace

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRepoConfig_UnmarshalYAML_StringFormat(t *testing.T) {
	input := `repos/retinue`
	var rc RepoConfig
	if err := yaml.Unmarshal([]byte(input), &rc); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if rc.Path != "repos/retinue" {
		t.Errorf("Path = %q, want %q", rc.Path, "repos/retinue")
	}
	if rc.BaseBranch != "" {
		t.Errorf("BaseBranch = %q, want empty", rc.BaseBranch)
	}
}

func TestRepoConfig_UnmarshalYAML_ObjectFormat(t *testing.T) {
	input := "path: repos/retinue\nbase_branch: develop\n"
	var rc RepoConfig
	if err := yaml.Unmarshal([]byte(input), &rc); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if rc.Path != "repos/retinue" {
		t.Errorf("Path = %q, want %q", rc.Path, "repos/retinue")
	}
	if rc.BaseBranch != "develop" {
		t.Errorf("BaseBranch = %q, want %q", rc.BaseBranch, "develop")
	}
}

func TestRepoConfig_UnmarshalYAML_ObjectNoBaseBranch(t *testing.T) {
	input := "path: repos/retinue\n"
	var rc RepoConfig
	if err := yaml.Unmarshal([]byte(input), &rc); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if rc.Path != "repos/retinue" {
		t.Errorf("Path = %q, want %q", rc.Path, "repos/retinue")
	}
	if rc.BaseBranch != "" {
		t.Errorf("BaseBranch = %q, want empty", rc.BaseBranch)
	}
}

func TestRepoConfig_UnmarshalYAML_WithCommitStyle(t *testing.T) {
	input := "path: repos/retinue\ncommit_style: conventional\n"
	var rc RepoConfig
	if err := yaml.Unmarshal([]byte(input), &rc); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if rc.Path != "repos/retinue" {
		t.Errorf("Path = %q, want %q", rc.Path, "repos/retinue")
	}
	if rc.CommitStyle != "conventional" {
		t.Errorf("CommitStyle = %q, want %q", rc.CommitStyle, "conventional")
	}
}

func TestRepoConfig_UnmarshalYAML_StringFormatNoCommitStyle(t *testing.T) {
	// String format can't carry commit_style — verify it's empty.
	input := `repos/retinue`
	var rc RepoConfig
	if err := yaml.Unmarshal([]byte(input), &rc); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if rc.CommitStyle != "" {
		t.Errorf("CommitStyle = %q, want empty", rc.CommitStyle)
	}
}

func TestConfig_UnmarshalYAML_MixedRepoFormats(t *testing.T) {
	input := `
name: test
repos:
  simple: repos/simple
  full:
    path: repos/full
    base_branch: develop
model: claude-opus-4-6
max_workers: 4
`
	var cfg Config
	if err := yaml.Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if cfg.Repos["simple"].Path != "repos/simple" {
		t.Errorf("simple path = %q", cfg.Repos["simple"].Path)
	}
	if cfg.Repos["full"].Path != "repos/full" {
		t.Errorf("full path = %q", cfg.Repos["full"].Path)
	}
	if cfg.Repos["full"].BaseBranch != "develop" {
		t.Errorf("full base_branch = %q", cfg.Repos["full"].BaseBranch)
	}
}
