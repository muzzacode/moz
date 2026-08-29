package models

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Capability string

const (
	CapToolCalling  Capability = "tool_calling"
	CapCode         Capability = "code"
	CapReasoning    Capability = "reasoning"
	CapLongContext  Capability = "long_context"
	CapVision       Capability = "vision"
)

type Profile struct {
	ID              string            `yaml:"id"`
	Name            string            `yaml:"name"`
	ProviderKind    string            `yaml:"provider_kind"`
	Model           string            `yaml:"model"`
	BaseURL         string            `yaml:"base_url"`
	APIKeyCredential string           `yaml:"api_key_credential"`
	Capabilities    []Capability      `yaml:"capabilities"`
	ContextLength   int               `yaml:"context_length"`
	CostTier        string            `yaml:"cost_tier"`
	DefaultParams   map[string]any    `yaml:"default_params"`
}

type Registry struct {
	Profiles []Profile `yaml:"profiles"`
	byID     map[string]*Profile
}

func DefaultProfiles() *Registry {
	return &Registry{
		Profiles: []Profile{
			{
				ID:           "coding-default",
				Name:         "PAIEP Coding Default",
				ProviderKind: "ollama",
				Model:        "qwen2.5-coder:14b",
				BaseURL:      "http://127.0.0.1:11434/v1/",
				Capabilities: []Capability{CapToolCalling, CapCode},
				ContextLength: 131072,
				CostTier:     "local",
				DefaultParams: map[string]any{
					"temperature": 0.4,
					"max_tokens":  4096,
				},
			},
			{
				ID:           "general-default",
				Name:         "PAIEP General Default",
				ProviderKind: "ollama",
				Model:        "qwen3:8b",
				BaseURL:      "http://127.0.0.1:11434/v1/",
				Capabilities: []Capability{},
				ContextLength: 131072,
				CostTier:     "local",
				DefaultParams: map[string]any{
					"temperature": 0.7,
					"max_tokens":  4096,
				},
			},
		},
	}
}

func Load(path string) (*Registry, error) {
	r := DefaultProfiles()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return r, nil
		}
		return nil, err
	}
	if err := yaml.Unmarshal(data, r); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Registry) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := yaml.Marshal(r)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func (r *Registry) index() {
	if r.byID == nil {
		r.byID = make(map[string]*Profile)
	}
	for i := range r.Profiles {
		p := &r.Profiles[i]
		r.byID[p.ID] = p
	}
}

func (r *Registry) Find(id string) (*Profile, error) {
	r.index()
	if p, ok := r.byID[id]; ok {
		return p, nil
	}
	return nil, fmt.Errorf("model profile not found: %s", id)
}

func (r *Registry) List() []Profile {
	return r.Profiles
}
