// Package standing defines standing agent definitions — long-lived,
// user-defined agents with mandates (like a CI watcher or codebase
// gardener). They are distinct from ephemeral task workers: they
// persist, have identities, and run alongside Woland.
package standing

// Agent represents a standing agent definition. Each agent has a
// unique ID, a role description, and a prompt that defines its
// mandate and behavior.
type Agent struct {
	ID       string   `yaml:"id"`
	Name     string   `yaml:"name"`
	Role     string   `yaml:"role,omitempty"`
	Repos    []string `yaml:"repos,omitempty"`
	Schedule string   `yaml:"schedule,omitempty"`
	Model    string   `yaml:"model,omitempty"`
	Prompt   string   `yaml:"prompt"`
	Enabled  bool     `yaml:"enabled,omitempty"`
}

// AgentFile is the top-level structure of agents.yaml.
type AgentFile struct {
	Agents []Agent `yaml:"agents"`
}
