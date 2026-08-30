// Package tokens provides provider-agnostic token estimation.
//
// Exact tokenization differs per model family (BPE vocab for GPT, SentencePiece
// for Llama/Qwen, a proprietary tokenizer for Claude), and Moz targets all of
// them. Rather than shipping vocab files for one family and being wrong about
// the rest, we use a calibrated character-based estimator that is deliberately
// conservative: it over-estimates slightly so budgets are never exceeded.
package tokens

import (
	"github.com/muzzacode/moz/internal/memory"
)

// Empirically, English prose averages ~4 chars/token and source code averages
// ~3 chars/token because of punctuation and identifiers. We use 3.0 so mixed
// code/prose history is over-estimated rather than under-estimated.
const charsPerToken = 3.0

// Per-message framing overhead (role markers, delimiters). Both the OpenAI and
// Anthropic wire formats add a handful of tokens per message.
const perMessageOverhead = 4

// EstimateText estimates the token count of a raw string.
func EstimateText(s string) int {
	if s == "" {
		return 0
	}
	n := int(float64(len(s))/charsPerToken) + 1
	return n
}

// EstimateMessage estimates the token count of a single message, including its
// tool calls and framing overhead.
func EstimateMessage(m memory.Message) int {
	n := perMessageOverhead
	n += EstimateText(m.Content)
	n += EstimateText(m.ToolCallID)
	for _, tc := range m.ToolCalls {
		n += EstimateText(tc.Name)
		n += EstimateText(tc.Arguments)
		n += perMessageOverhead
	}
	return n
}

// EstimateMessages estimates the token count of a conversation.
func EstimateMessages(msgs []memory.Message) int {
	total := 0
	for _, m := range msgs {
		total += EstimateMessage(m)
	}
	return total
}
