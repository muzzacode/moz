package tokens

import (
	"strings"
	"testing"

	"github.com/muzzacode/moz/internal/memory"
)

func TestEstimateTextScalesWithLength(t *testing.T) {
	short := EstimateText("hello")
	long := EstimateText(strings.Repeat("hello ", 100))
	if short <= 0 {
		t.Fatal("expected a positive estimate")
	}
	if long <= short {
		t.Fatalf("expected longer text to cost more: %d vs %d", short, long)
	}
}

func TestEstimateTextEmpty(t *testing.T) {
	if EstimateText("") != 0 {
		t.Fatal("empty string should cost 0")
	}
}

// The estimator must not under-count, or budgets silently overflow the real
// context window.
func TestEstimateIsConservative(t *testing.T) {
	// ~1000 chars of code-like text is realistically 250-400 real tokens.
	code := strings.Repeat("func f(x int) { return x + 1 }\n", 33)
	got := EstimateText(code)
	if got < len(code)/4 {
		t.Fatalf("estimate %d is below the 4 chars/token floor for %d chars", got, len(code))
	}
}

func TestEstimateMessageIncludesToolCalls(t *testing.T) {
	plain := memory.Message{Role: "assistant", Content: "hi"}
	withTool := memory.Message{
		Role:    "assistant",
		Content: "hi",
		ToolCalls: []memory.ToolCall{
			{ID: "c1", Name: "read_file", Arguments: `{"path":"main.go"}`},
		},
	}
	if EstimateMessage(withTool) <= EstimateMessage(plain) {
		t.Fatal("tool calls must contribute to the estimate")
	}
}

func TestEstimateMessagesSumsAndCountsOverhead(t *testing.T) {
	msgs := []memory.Message{
		{Role: "system", Content: "rules"},
		{Role: "user", Content: "question"},
	}
	total := EstimateMessages(msgs)
	if total <= EstimateText("rules")+EstimateText("question") {
		t.Fatal("expected per-message framing overhead to be included")
	}
	if EstimateMessages(nil) != 0 {
		t.Fatal("nil conversation should cost 0")
	}
}
