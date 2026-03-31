package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

const (
	ConfigFile  = "retinue.yaml"
	TasksFile   = "tasks.yaml"
	ArchiveFile = "tasks-archive.yaml"
	AgentsFile  = "agents.yaml"
	WorktreeDir = ".worktrees"
	LogsDir     = "logs"
	ReposDir    = "repos"

	DefaultModel      = "claude-opus-4-6"
	DefaultMaxWorkers = 20
)

type Workspace struct {
	Path   string
	Config Config

	ghToken string
	ghOnce  sync.Once
	ghErr   error
}

// Create initializes a new workspace at the given path.
func Create(path string, cfg Config) (*Workspace, error) {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return nil, fmt.Errorf("creating workspace directory: %w", err)
	}

	for _, dir := range []string{LogsDir, ReposDir} {
		if err := os.MkdirAll(filepath.Join(path, dir), 0o755); err != nil {
			return nil, fmt.Errorf("creating %s directory: %w", dir, err)
		}
	}

	if cfg.Model == "" {
		cfg.Model = DefaultModel
	}
	if cfg.MaxWorkers == 0 {
		cfg.MaxWorkers = DefaultMaxWorkers
	}

	ws := &Workspace{Path: path, Config: cfg}

	if err := ws.SaveConfig(); err != nil {
		return nil, err
	}

	// Create empty tasks file.
	tasksPath := filepath.Join(path, TasksFile)
	if err := os.WriteFile(tasksPath, []byte("tasks: []\n"), 0o644); err != nil {
		return nil, fmt.Errorf("creating tasks file: %w", err)
	}

	return ws, nil
}

// Detect finds a workspace by looking for retinue.yaml in the current directory.
func Detect() (*Workspace, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getting working directory: %w", err)
	}

	cfgPath := filepath.Join(cwd, ConfigFile)
	if _, err := os.Stat(cfgPath); err != nil {
		return nil, fmt.Errorf("no %s found in current directory; use --workspace or cd into an apartment", ConfigFile)
	}

	return Load(cwd)
}

// Load reads an existing workspace from the given path.
func Load(path string) (*Workspace, error) {
	cfgPath := filepath.Join(path, ConfigFile)
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	return &Workspace{Path: path, Config: cfg}, nil
}

// SaveConfig writes the workspace config to retinue.yaml.
func (ws *Workspace) SaveConfig() error {
	data, err := yaml.Marshal(&ws.Config)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	cfgPath := filepath.Join(ws.Path, ConfigFile)
	if err := os.WriteFile(cfgPath, data, 0o644); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	return nil
}

// TasksPath returns the path to the tasks.yaml file.
func (ws *Workspace) TasksPath() string {
	return filepath.Join(ws.Path, TasksFile)
}

// ArchivePath returns the path to the tasks-archive.yaml file.
func (ws *Workspace) ArchivePath() string {
	return filepath.Join(ws.Path, ArchiveFile)
}

// AgentsPath returns the path to the agents.yaml file.
func (ws *Workspace) AgentsPath() string {
	return filepath.Join(ws.Path, AgentsFile)
}

// LogsPath returns the path to the logs directory.
func (ws *Workspace) LogsPath() string {
	return filepath.Join(ws.Path, LogsDir)
}
