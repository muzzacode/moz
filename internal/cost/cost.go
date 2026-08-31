package cost

import (
	"fmt"

	openai "github.com/sashabaranov/go-openai"
)

// USD per 1M tokens.
type Price struct {
	Input  float64
	Output float64
}

// knownPrices maps a profile ID to its published price per million tokens.
//
// These feed the session budget, so an inaccurate entry means the ceiling is
// enforced against the wrong number. Keep them in step with the profiles in
// internal/models.
var knownPrices = map[string]Price{
	// Local inference is free.
	"local-coder": {Input: 0, Output: 0},

	// Cheap cloud, the workhorse tier.
	"qwen-flash":     {Input: 0.030, Output: 0.130},
	"deepseek-flash": {Input: 0.065, Output: 0.180},
	"glm-flash":      {Input: 0.075, Output: 0.250},

	// Frontier.
	"glm-5.3":       {Input: 1.40, Output: 4.40},
	"grok-4.6":      {Input: 2.00, Output: 6.00},
	"claude-opus-5": {Input: 5.00, Output: 25.00},

	// Direct provider access.
	"claude-sonnet-5-direct": {Input: 2.00, Output: 10.00},
	"openai-gpt-4o":          {Input: 2.50, Output: 10.00},
	"openai-gpt-4o-mini":     {Input: 0.15, Output: 0.60},

	// The free router bills nothing.
	"openrouter-free": {Input: 0, Output: 0},
}

func Estimate(profileID string, u openai.Usage) float64 {
	p, ok := knownPrices[profileID]
	if !ok {
		return 0
	}
	inputCost := float64(u.PromptTokens) * p.Input / 1e6
	outputCost := float64(u.CompletionTokens) * p.Output / 1e6
	return inputCost + outputCost
}

func Format(profileID string, u openai.Usage) string {
	c := Estimate(profileID, u)
	if c == 0 {
		return ""
	}
	if c < 0.01 {
		return fmt.Sprintf("<$0.01")
	}
	return fmt.Sprintf("$%.4f", c)
}
