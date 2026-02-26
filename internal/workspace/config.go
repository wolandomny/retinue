package workspace

type Config struct {
	Name          string            `yaml:"name"`
	GithubAccount string            `yaml:"github_account"`
	Repos         map[string]string `yaml:"repos"`
	Model         string            `yaml:"model"`
	MaxWorkers    int               `yaml:"max_workers"`
}
