package config

import (
	"os"
	"path/filepath"
	"time"

	"github.com/muzzacode/moz/internal/approval"
	"gopkg.in/yaml.v3"
)

// DefaultMaxTurns is the default per-task tool-call budget for the agent loop.
const DefaultMaxTurns = 40

// DefaultRequestTimeout bounds a single model request so a hung provider cannot
// wedge the whole session.
const DefaultRequestTimeout = 300

type Config struct {
	OllamaBaseURL string           `yaml:"ollama_base_url"`
	MemoryDir     string           `yaml:"memory_dir"`
	DefaultModel  string           `yaml:"default_model"`
	Mode          string           `yaml:"mode"`
	Adaptive      AdaptiveOpts     `yaml:"adaptive"`
	Workspace     string           `yaml:"workspace"`
	Approval      *approval.Policy `yaml:"approval"`
	Agent         bool             `yaml:"agent"`
	AgentOpts     AgentOpts        `yaml:"agent_options"`
}

type AgentOpts struct {
	// MaxTurns caps tool-call iterations for a single task.
	MaxTurns int `yaml:"max_turns"`
	// RequestTimeoutSeconds bounds each individual model request.
	RequestTimeoutSeconds int `yaml:"request_timeout_seconds"`
	// Verify runs the project's verification command after file edits and
	// feeds failures back to the model.
	Verify bool `yaml:"verify"`
	// VerifyCommand overrides the auto-detected verification command.
	VerifyCommand string `yaml:"verify_command"`
}

type AdaptiveOpts struct {
	PreferLocal    bool    `yaml:"prefer_local"`
	MaxCostPerTurn float64 `yaml:"max_cost_per_turn"`
	CloudThreshold float64 `yaml:"cloud_threshold"`
}

func Default() *Config {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return &Config{
		OllamaBaseURL: "http://127.0.0.1:11434/v1/",
		MemoryDir:     filepath.Join(home, ".config", "moz", "memory"),
		DefaultModel:  "qwen2.5-coder:14b",
		Mode:          "adaptive",
		Adaptive: AdaptiveOpts{
			PreferLocal:    true,
			MaxCostPerTurn: 0.0,
			CloudThreshold: 0.75,
		},
		// Workspace is deliberately empty: persisting the first run's directory
		// into global config would pin every later session to it. It is an
		// optional extra allowed root, not the active project.
		Workspace: "",
		Approval:  approval.Default(),
		Agent:     false,
		AgentOpts: AgentOpts{
			MaxTurns:              DefaultMaxTurns,
			RequestTimeoutSeconds: DefaultRequestTimeout,
			Verify:                true,
		},
	}
}

// RequestTimeout returns the per-request timeout as a duration.
func (c *Config) RequestTimeout() time.Duration {
	s := c.AgentOpts.RequestTimeoutSeconds
	if s <= 0 {
		s = DefaultRequestTimeout
	}
	return time.Duration(s) * time.Second
}

func Load(path string) (*Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func (c *Config) ExpandMemoryDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return c.MemoryDir
	}
	dir := c.MemoryDir
	if len(dir) >= 2 && dir[:2] == "~/" {
		dir = filepath.Join(home, dir[2:])
	}
	return os.Expand(dir, func(key string) string {
		if key == "HOME" {
			return home
		}
		return os.Getenv(key)
	})
}

func Dir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".moz"
	}
	return filepath.Join(home, ".config", "moz")
}
