package workspace

// Config holds the workspace configuration persisted in retinue.yaml.
type Config struct {
	Name          string            `yaml:"name"`           // workspace display name
	GithubAccount string            `yaml:"github_account"` // GitHub account for gh CLI auth
	Repos         map[string]string `yaml:"repos"`          // repo name → relative path from workspace root
	Model         string            `yaml:"model"`          // Claude model to use for agents
	MaxWorkers    int               `yaml:"max_workers"`    // max concurrent worker agents
	Validate      map[string]string `yaml:"validate,omitempty"` // repo name → validation shell command
}
