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

func (p *Profile) Has(c Capability) bool {
	for _, have := range p.Capabilities {
		if have == c {
			return true
		}
	}
	return false
}

// SupportsNativeTools reports whether the provider will return structured tool
// calls, so the model does not need to be taught a JSON output contract.
//
// Asking a model to emit bare JSON *and* passing it native tool schemas gives
// it two conflicting instructions, which is a major source of malformed calls.
func (p *Profile) SupportsNativeTools() bool {
	if !p.Has(CapToolCalling) {
		return false
	}
	switch p.ProviderKind {
	case ProviderAnthropic, ProviderOpenAICompatible, ProviderOpenRouter:
		return true
	case ProviderOllama:
		// Ollama's OpenAI-compatible layer supports tools, but coverage varies
		// by model, so the text-JSON fallback is retained for local models.
		return false
	default:
		return false
	}
}

type Stack struct {
	Name     string    `yaml:"name"`
	Class    TaskClass `yaml:"class"`
	Profiles []string  `yaml:"profiles"`
}

type TaskClass string

const (
	TaskQuickChat    TaskClass = "quick_chat"
	TaskCodeEdit     TaskClass = "code_edit"
	TaskDebug        TaskClass = "debug"
	TaskReasoning    TaskClass = "reasoning"
	TaskArchitecture TaskClass = "architecture"
	TaskVision       TaskClass = "vision"
	TaskChat         TaskClass = "chat"
)

type Registry struct {
	Profiles []Profile `yaml:"profiles"`
	Stacks   []Stack   `yaml:"stacks"`
	byID     map[string]*Profile
	byStack  map[string]*Stack
}

// DefaultProfiles returns the built-in model lineup.
//
// The lineup is chosen on price-for-capability, measured rather than assumed.
// Two facts drive it:
//
//   - GLM 5.3 Flash costs $0.075/$0.25 per million tokens and benchmarks higher
//     than Claude Sonnet 5 at $2/$10. It is the workhorse for that reason: about
//     27x cheaper for equal or better coding ability.
//   - Frontier models are only worth their price on genuinely hard reasoning, so
//     they sit at the end of the stacks and are gated behind a higher threshold.
//
// Paid models are reached through OpenRouter so that one key covers every tier.
// Direct provider profiles are kept for when their billing is set up, but are
// left out of the stacks so a dead key cannot break routing.
func DefaultProfiles() *Registry {
	return &Registry{
		Profiles: []Profile{
			// ---------- Local, free ----------
			{
				ID:            "local-coder",
				Name:          "Local Qwen2.5 Coder 14B",
				ProviderKind:  ProviderOllama,
				Model:         "qwen2.5-coder:14b",
				BaseURL:       "http://127.0.0.1:11434/v1/",
				Capabilities:  []Capability{CapToolCalling, CapCode},
				ContextLength: 131072,
				CostTier:      "local",
				DefaultParams: map[string]any{
					"temperature": 0.2,
					"max_tokens":  4096,
				},
			},

			// ---------- Cheap cloud, the workhorse tier ----------
			{
				// Best measured capability per dollar in this tier: intelligence
				// 57.5, coding 71.5, agentic 58.2 at $0.075/$0.25.
				ID:               "glm-flash",
				Name:             "GLM 5.3 Flash",
				ProviderKind:     ProviderOpenRouter,
				Model:            "z-ai/glm-5.3-flash",
				BaseURL:          "https://openrouter.ai/api/v1",
				APIKeyCredential: "OPENROUTER_API_KEY",
				Capabilities:     []Capability{CapToolCalling, CapCode, CapReasoning, CapLongContext},
				ContextLength:    1048576,
				CostTier:         "cloud-cheap",
				DefaultParams: map[string]any{
					"temperature": 0.3,
					"max_tokens":  8192,
				},
			},
			{
				// Cheapest usable option, and the only one here with vision.
				ID:               "qwen-flash",
				Name:             "Qwen 3.7 Flash",
				ProviderKind:     ProviderOpenRouter,
				Model:            "qwen/qwen3.7-flash",
				BaseURL:          "https://openrouter.ai/api/v1",
				APIKeyCredential: "OPENROUTER_API_KEY",
				Capabilities:     []Capability{CapToolCalling, CapCode, CapVision, CapLongContext},
				ContextLength:    1000000,
				CostTier:         "cloud-cheap",
				DefaultParams: map[string]any{
					"temperature": 0.3,
					"max_tokens":  4096,
				},
			},
			{
				ID:               "deepseek-flash",
				Name:             "DeepSeek V4 Flash",
				ProviderKind:     ProviderOpenRouter,
				Model:            "deepseek/deepseek-v4-flash-0731",
				BaseURL:          "https://openrouter.ai/api/v1",
				APIKeyCredential: "OPENROUTER_API_KEY",
				Capabilities:     []Capability{CapToolCalling, CapCode, CapReasoning, CapLongContext},
				ContextLength:    1310720,
				CostTier:         "cloud-cheap",
				DefaultParams: map[string]any{
					"temperature": 0.3,
					"max_tokens":  8192,
				},
			},

			// ---------- Frontier, for genuinely hard reasoning ----------
			{
				// Frontier-class scores at a third of frontier price, so it is
				// the first premium option tried.
				ID:               "glm-5.3",
				Name:             "GLM 5.3",
				ProviderKind:     ProviderOpenRouter,
				Model:            "z-ai/glm-5.3",
				BaseURL:          "https://openrouter.ai/api/v1",
				APIKeyCredential: "OPENROUTER_API_KEY",
				Capabilities:     []Capability{CapToolCalling, CapCode, CapReasoning, CapLongContext},
				ContextLength:    1310720,
				CostTier:         "cloud-premium",
				DefaultParams: map[string]any{
					"temperature": 0.4,
					"max_tokens":  8192,
				},
			},
			{
				ID:               "grok-4.6",
				Name:             "Grok 4.6",
				ProviderKind:     ProviderOpenRouter,
				Model:            "x-ai/grok-4.6",
				BaseURL:          "https://openrouter.ai/api/v1",
				APIKeyCredential: "OPENROUTER_API_KEY",
				Capabilities:     []Capability{CapToolCalling, CapCode, CapReasoning},
				ContextLength:    500000,
				CostTier:         "cloud-premium",
				DefaultParams: map[string]any{
					"temperature": 0.4,
					"max_tokens":  8192,
				},
			},
			{
				// Highest measured capability available: intelligence 63.1,
				// coding 78. Reserved for the hardest work because of the price.
				ID:               "claude-opus-5",
				Name:             "Claude Opus 5",
				ProviderKind:     ProviderOpenRouter,
				Model:            "anthropic/claude-opus-5",
				BaseURL:          "https://openrouter.ai/api/v1",
				APIKeyCredential: "OPENROUTER_API_KEY",
				Capabilities:     []Capability{CapToolCalling, CapCode, CapReasoning, CapVision, CapLongContext},
				ContextLength:    1000000,
				CostTier:         "cloud-premium",
				DefaultParams: map[string]any{
					"temperature": 0.4,
					"max_tokens":  8192,
				},
			},

			// ---------- Direct provider access ----------
			// Kept usable through --model or /model, but deliberately absent
			// from the stacks: routing should not depend on a second billing
			// relationship being active.
			{
				ID:               "claude-sonnet-5-direct",
				Name:             "Claude Sonnet 5 (direct)",
				ProviderKind:     ProviderAnthropic,
				Model:            "claude-sonnet-5",
				APIKeyCredential: "ANTHROPIC_API_KEY",
				Capabilities:     []Capability{CapToolCalling, CapReasoning, CapCode, CapVision},
				ContextLength:    1000000,
				CostTier:         "cloud-premium",
				DefaultParams: map[string]any{
					"temperature": 0.4,
					"max_tokens":  8192,
				},
			},
			{
				// GPT-4o costs $2.50/$10, the same class as a frontier model, so
				// it is tiered as premium rather than cheap.
				ID:               "openai-gpt-4o",
				Name:             "OpenAI GPT-4o (direct)",
				ProviderKind:     ProviderOpenAICompatible,
				Model:            "gpt-4o",
				APIKeyCredential: "OPENAI_API_KEY",
				Capabilities:     []Capability{CapToolCalling, CapReasoning, CapCode, CapVision},
				ContextLength:    128000,
				CostTier:         "cloud-premium",
				DefaultParams: map[string]any{
					"temperature": 0.4,
					"max_tokens":  4096,
				},
			},
			{
				ID:               "openai-gpt-4o-mini",
				Name:             "OpenAI GPT-4o Mini (direct)",
				ProviderKind:     ProviderOpenAICompatible,
				Model:            "gpt-4o-mini",
				APIKeyCredential: "OPENAI_API_KEY",
				Capabilities:     []Capability{CapToolCalling, CapCode},
				ContextLength:    128000,
				CostTier:         "cloud-cheap",
				DefaultParams: map[string]any{
					"temperature": 0.3,
					"max_tokens":  4096,
				},
			},
			{
				// Zero-cost router, useful for throwaway work. Quality varies,
				// so it is not in any stack.
				ID:               "openrouter-free",
				Name:             "OpenRouter Free",
				ProviderKind:     ProviderOpenRouter,
				Model:            "openrouter/free",
				BaseURL:          "https://openrouter.ai/api/v1",
				APIKeyCredential: "OPENROUTER_API_KEY",
				Capabilities:     []Capability{CapToolCalling, CapCode},
				ContextLength:    200000,
				CostTier:         "cloud-cheap",
				DefaultParams: map[string]any{
					"temperature": 0.4,
					"max_tokens":  4096,
				},
			},
		},

		// Stacks list candidates cheapest first. Routing picks by cost tier
		// rather than position, so ordering here is documentation and a
		// tiebreaker within a tier.
		Stacks: []Stack{
			{Name: "daily", Class: TaskQuickChat, Profiles: []string{"local-coder", "qwen-flash"}},
			{Name: "chat", Class: TaskChat, Profiles: []string{"local-coder", "glm-flash"}},
			{Name: "code", Class: TaskCodeEdit, Profiles: []string{"local-coder", "glm-flash", "deepseek-flash", "glm-5.3", "claude-opus-5"}},
			{Name: "debug", Class: TaskDebug, Profiles: []string{"local-coder", "glm-flash", "deepseek-flash", "glm-5.3", "claude-opus-5"}},
			{Name: "reasoning", Class: TaskReasoning, Profiles: []string{"local-coder", "glm-flash", "glm-5.3", "grok-4.6", "claude-opus-5"}},
			{Name: "architecture", Class: TaskArchitecture, Profiles: []string{"local-coder", "glm-flash", "glm-5.3", "grok-4.6", "claude-opus-5"}},
			// No local vision model is installed, so vision starts at the
			// cheapest cloud model that accepts images.
			{Name: "vision", Class: TaskVision, Profiles: []string{"qwen-flash", "claude-opus-5"}},
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
