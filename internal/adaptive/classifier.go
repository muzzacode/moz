package adaptive

import (
	"strings"

	"github.com/muzzacode/moz/internal/models"
)

type TaskSignal struct {
	Class  models.TaskClass
	Reason string
	Boost  float64
}

var rules = []struct {
	patterns []string
	signal   TaskSignal
}{
	{
		patterns: []string{"design", "architecture", "system design", "design pattern", "microservice", "monolith", "scalability"},
		signal:   TaskSignal{Class: models.TaskArchitecture, Reason: "architecture keywords", Boost: 0.9},
	},
	{
		patterns: []string{"why", "explain", "reason", "reasoning", "compare", "trade-off", "tradeoff", "implications", "consequences"},
		signal:   TaskSignal{Class: models.TaskReasoning, Reason: "reasoning keywords", Boost: 0.7},
	},
	{
		patterns: []string{"debug", "fix", "error", "exception", "stack trace", "crash", "bug", "failing test"},
		signal:   TaskSignal{Class: models.TaskDebug, Reason: "debugging keywords", Boost: 0.8},
	},
	{
		patterns: []string{"refactor", "rewrite", "implement", "write a function", "add a feature", "code change", "pull request"},
		signal:   TaskSignal{Class: models.TaskCodeEdit, Reason: "code edit keywords", Boost: 0.7},
	},
	{
		patterns: []string{"screenshot", "image", "diagram", "mockup", "ui", "visual"},
		signal:   TaskSignal{Class: models.TaskVision, Reason: "vision keywords", Boost: 0.9},
	},
}

func Classify(prompt string) TaskSignal {
	lower := strings.ToLower(prompt)

	for _, rule := range rules {
		for _, p := range rule.patterns {
			if strings.Contains(lower, p) {
				return rule.signal
			}
		}
	}

	// If it is very short and had no strong signal, treat as quick chat.
	if len(strings.Fields(prompt)) <= 2 {
		return TaskSignal{Class: models.TaskQuickChat, Reason: "short prompt", Boost: 0.0}
	}

	// Default: code if there are paths, code-ish tokens, or it looks technical.
	if looksLikeCode(lower) {
		return TaskSignal{Class: models.TaskCodeEdit, Reason: "technical/code context", Boost: 0.5}
	}

	return TaskSignal{Class: models.TaskChat, Reason: "general chat", Boost: 0.0}
}

func looksLikeCode(s string) bool {
	indicators := []string{"/", "\n", "func ", "def ", "class ", "{", "}", "package ", "import ", "#include", "console.log", "print(", "(", ")"}
	for _, i := range indicators {
		if strings.Contains(s, i) {
			return true
		}
	}
	return false
}
