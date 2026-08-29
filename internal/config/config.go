package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	OllamaBaseURL string       `yaml:"ollama_base_url"`
	MemoryDir     string       `yaml:"memory_dir"`
	DefaultModel  string       `yaml:"default_model"`
	Mode          string       `yaml:"mode"`
	Adaptive      AdaptiveOpts `yaml:"adaptive"`
}

type AdaptiveOpts struct {
	PreferLocal      bool    `yaml:"prefer_local"`
	MaxCostPerTurn   float64 `yaml:"max_cost_per_turn"`
	CloudThreshold   float64 `yaml:"cloud_threshold"`
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
	}
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
