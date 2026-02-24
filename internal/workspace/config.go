package workspace

type Config struct {
	Name       string            `yaml:"name"`
	Repos      map[string]string `yaml:"repos"`
	Model      string            `yaml:"model"`
	MaxWorkers int               `yaml:"max_workers"`
}
