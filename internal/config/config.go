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
	// PreferLocal keeps work on local inference unless the task is demanding.
	PreferLocal bool `yaml:"prefer_local"`
	// CloudThreshold is the task confidence needed to leave local inference.
	CloudThreshold float64 `yaml:"cloud_threshold"`
	// PremiumThreshold is the task confidence needed to use a frontier model.
	PremiumThreshold float64 `yaml:"premium_threshold"`
	// MaxSessionCost caps total spend for one session, in USD. Zero is
	// unlimited. Once reached, routing stops choosing paid models.
	MaxSessionCost float64 `yaml:"max_session_cost"`
}

func Default() *Config {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return &Config{
		OllamaBaseURL: "http://127.0.0.1:11434/v1/",
		MemoryDir:     filepath.Join(home, ".config", "moz", "memory"),
		// A profile ID, not a model name: the registry is keyed by profile.
		DefaultModel: "local-coder",
		Mode:         "adaptive",
		Adaptive: AdaptiveOpts{
			PreferLocal:      true,
			CloudThreshold:   0.5,
			PremiumThreshold: 0.8,
			// Unlimited by default: a surprise hard stop mid-task would be worse
			// than a surprise bill. Set this to opt into a ceiling.
			MaxSessionCost: 0,
		},
		// Workspace is deliberately empty: persisting the first run's directory
		// into global config would pin every later session to it. It is an
		// optional extra allowed root, not the active project.
		Workspace: "",
		Approval:  approval.Default(),
		// On by default. Moz is a coding tool, and without tools it cannot read
		// the repository it was launched in, so questions about "this project"
		// get answered from training data. Writes still require approval.
		Agent: true,
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
