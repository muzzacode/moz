package models

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Capability string

const (
	CapToolCalling Capability = "tool_calling"
	CapCode        Capability = "code"
	CapReasoning   Capability = "reasoning"
	CapLongContext Capability = "long_context"
	CapVision      Capability = "vision"
)

type ProviderKind string

const (
	ProviderOllama           ProviderKind = "ollama"
	ProviderOpenAICompatible ProviderKind = "openai-compatible"
	ProviderAnthropic        ProviderKind = "anthropic"
	ProviderGoogle           ProviderKind = "google"
	ProviderOpenRouter       ProviderKind = "openrouter"
)

type Profile struct {
	ID               string         `yaml:"id"`
	Name             string         `yaml:"name"`
	ProviderKind     ProviderKind   `yaml:"provider_kind"`
	Model            string         `yaml:"model"`
	BaseURL          string         `yaml:"base_url"`
	APIKeyCredential string         `yaml:"api_key_credential"`
	Capabilities     []Capability   `yaml:"capabilities"`
	ContextLength    int            `yaml:"context_length"`
	CostTier         string         `yaml:"cost_tier"`
	DefaultParams    map[string]any `yaml:"default_params"`
}

func (p *Profile) IsLocal() bool {
	return p.ProviderKind == ProviderOllama
}

func (p *Profile) CanUseOpenAIClient() bool {
	return p.ProviderKind == ProviderOllama ||
		p.ProviderKind == ProviderOpenAICompatible ||
		p.ProviderKind == ProviderOpenRouter
}

type Stack struct {
	Name     string   `yaml:"name"`
	Class    TaskClass `yaml:"class"`
	Profiles []string `yaml:"profiles"`
}

type TaskClass string

const (
	TaskQuickChat  TaskClass = "quick_chat"
	TaskCodeEdit   TaskClass = "code_edit"
	TaskDebug      TaskClass = "debug"
	TaskReasoning  TaskClass = "reasoning"
	TaskArchitecture TaskClass = "architecture"
	TaskVision     TaskClass = "vision"
	TaskChat       TaskClass = "chat"
)

type Registry struct {
	Profiles []Profile `yaml:"profiles"`
	Stacks   []Stack   `yaml:"stacks"`
	byID     map[string]*Profile
	byStack  map[string]*Stack
}

func DefaultProfiles() *Registry {
	return &Registry{
		Profiles: []Profile{
			{
				ID:           "coding-default",
				Name:         "PAIEP Coding Default",
				ProviderKind: ProviderOllama,
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
				ID:           "coding-quality",
				Name:         "PAIEP Coding Quality",
				ProviderKind: ProviderOllama,
				Model:        "qwen2.5-coder:14b",
				BaseURL:      "http://127.0.0.1:11434/v1/",
				Capabilities: []Capability{CapToolCalling, CapCode, CapReasoning},
				ContextLength: 131072,
				CostTier:     "local",
				DefaultParams: map[string]any{
					"temperature": 0.3,
					"max_tokens":  4096,
				},
			},
			{
				ID:           "general-default",
				Name:         "PAIEP General Default",
				ProviderKind: ProviderOllama,
				Model:        "qwen2.5-coder:14b",
				BaseURL:      "http://127.0.0.1:11434/v1/",
				Capabilities: []Capability{},
				ContextLength: 131072,
				CostTier:     "local",
				DefaultParams: map[string]any{
					"temperature": 0.7,
					"max_tokens":  4096,
				},
			},
			{
				ID:           "vision-default",
				Name:         "PAIEP Vision Default",
				ProviderKind: ProviderOllama,
				Model:        "qwen2.5-coder:14b",
				BaseURL:      "http://127.0.0.1:11434/v1/",
				Capabilities: []Capability{CapVision, CapToolCalling},
				ContextLength: 131072,
				CostTier:     "local",
				DefaultParams: map[string]any{
					"temperature": 0.4,
					"max_tokens":  4096,
				},
			},
			{
				ID:           "glm-5.3",
				Name:         "GLM 5.3 (Z.ai)",
				ProviderKind: ProviderOpenAICompatible,
				Model:        "glm-5.3",
				BaseURL:      "https://api.z.ai/api/paas/v4",
				APIKeyCredential: "ZAI_API_KEY",
				Capabilities: []Capability{CapToolCalling, CapReasoning, CapCode, CapLongContext},
				ContextLength: 1048576,
				CostTier:     "cloud-cheap",
				DefaultParams: map[string]any{
					"temperature": 0.6,
					"max_tokens":  128000,
					"reasoning_effort": "max",
				},
			},
			{
				ID:           "claude-sonnet-4",
				Name:         "Claude Sonnet 4",
				ProviderKind: ProviderAnthropic,
				Model:        "claude-sonnet-4-20250801",
				APIKeyCredential: "ANTHROPIC_API_KEY",
				Capabilities: []Capability{CapToolCalling, CapReasoning, CapCode, CapVision},
				ContextLength: 200000,
				CostTier:     "cloud-premium",
				DefaultParams: map[string]any{
					"temperature": 0.6,
					"max_tokens":  8192,
				},
			},
			{
				ID:           "openrouter-default",
				Name:         "OpenRouter Default",
				ProviderKind: ProviderOpenRouter,
				Model:        "openrouter/quasar-alpha",
				BaseURL:      "https://openrouter.ai/api/v1",
				APIKeyCredential: "OPENROUTER_API_KEY",
				Capabilities: []Capability{CapToolCalling, CapCode, CapReasoning},
				ContextLength: 200000,
				CostTier:     "cloud-cheap",
				DefaultParams: map[string]any{
					"temperature": 0.5,
					"max_tokens":  4096,
				},
			},
		},
		Stacks: []Stack{
			{Name: "daily", Class: TaskQuickChat, Profiles: []string{"general-default", "coding-default"}},
			{Name: "chat", Class: TaskChat, Profiles: []string{"general-default"}},
			{Name: "code", Class: TaskCodeEdit, Profiles: []string{"coding-default", "glm-5.3", "claude-sonnet-4"}},
			{Name: "debug", Class: TaskDebug, Profiles: []string{"coding-default", "glm-5.3", "claude-sonnet-4"}},
			{Name: "reasoning", Class: TaskReasoning, Profiles: []string{"coding-default", "glm-5.3", "claude-sonnet-4"}},
			{Name: "architecture", Class: TaskArchitecture, Profiles: []string{"coding-default", "glm-5.3", "claude-sonnet-4"}},
			{Name: "vision", Class: TaskVision, Profiles: []string{"vision-default", "claude-sonnet-4"}},
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
	if r.byStack == nil {
		r.byStack = make(map[string]*Stack)
	}
	for i := range r.Stacks {
		s := &r.Stacks[i]
		r.byStack[string(s.Class)] = s
		r.byStack[s.Name] = s
	}
}

func (r *Registry) Find(id string) (*Profile, error) {
	r.index()
	if p, ok := r.byID[id]; ok {
		return p, nil
	}
	return nil, fmt.Errorf("model profile not found: %s", id)
}

func (r *Registry) FindStack(class TaskClass) (*Stack, bool) {
	r.index()
	s, ok := r.byStack[string(class)]
	return s, ok
}

func (r *Registry) List() []Profile {
	return r.Profiles
}
