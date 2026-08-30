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

var knownPrices = map[string]Price{
	"claude-sonnet-5":    {Input: 2.0, Output: 10.0},
	"claude-opus-5":      {Input: 5.0, Output: 25.0},
	"claude-haiku-4-5":   {Input: 1.0, Output: 5.0},
	"glm-5.3":            {Input: 0.50, Output: 1.50},
	"openai-gpt-4o":      {Input: 2.50, Output: 10.0},
	"openai-gpt-4o-mini": {Input: 0.15, Output: 0.60},
	"openrouter-default": {Input: 0.10, Output: 0.30},
	"coding-default":     {Input: 0, Output: 0},
	"coding-quality":     {Input: 0, Output: 0},
	"general-default":    {Input: 0, Output: 0},
	"vision-default":     {Input: 0, Output: 0},
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
