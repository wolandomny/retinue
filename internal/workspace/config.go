package workspace

import "gopkg.in/yaml.v3"

// TelegramConfig holds Telegram integration settings.
type TelegramConfig struct {
	Token  string `yaml:"token,omitempty"`
	ChatID int64  `yaml:"chat_id,omitempty"`
}

// Config holds the workspace configuration persisted in retinue.yaml.
type Config struct {
	Name          string                `yaml:"name"`               // workspace display name
	GithubAccount string                `yaml:"github_account"`     // GitHub account for gh CLI auth
	Repos         map[string]RepoConfig `yaml:"repos"`              // repo name → repo configuration
	Model         string                `yaml:"model"`              // Claude model to use for agents
	MaxWorkers    int                   `yaml:"max_workers"`        // max concurrent worker agents
	Validate      map[string]string     `yaml:"validate,omitempty"` // repo name → validation shell command
	Telegram      *TelegramConfig       `yaml:"telegram,omitempty"` // Telegram bot configuration
}

// RepoConfig holds per-repository configuration.
type RepoConfig struct {
	Path        string `yaml:"path"`
	BaseBranch  string `yaml:"base_branch,omitempty"`
	CommitStyle string `yaml:"commit_style,omitempty"`
}

// UnmarshalYAML supports both simple string format ("repos/retinue")
// and full object format ({path: "repos/retinue", base_branch: "develop"}).
func (r *RepoConfig) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		// Simple string format: just the path.
		r.Path = value.Value
		return nil
	}
	// Object format: decode normally.
	type rawRepoConfig RepoConfig
	return value.Decode((*rawRepoConfig)(r))
}
