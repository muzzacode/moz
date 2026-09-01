package llm

import (
	"context"
	"fmt"
	"strings"

	"github.com/muzzacode/moz/internal/memory"
)

const summarySystemPrompt = `You compress an AI coding agent's conversation history.

Write a dense summary that lets the agent continue working without re-reading the original turns. Preserve, in this order of priority:
1. The user's goal and any explicit constraints or preferences.
2. Decisions made and the reasoning behind them.
3. Files created or modified, and what changed in each.
4. Commands run and their outcomes, especially failures and error messages.
5. What remains unfinished.

Omit greetings, restated file contents, and tool call mechanics. Use terse bullet points. Never invent facts that are not present in the history.`

// Summarize compresses conversation history into prose. It satisfies
// compact.Summarizer.
func (c *Client) Summarize(ctx context.Context, msgs []memory.Message) (string, error) {
	if len(msgs) == 0 {
		return "", fmt.Errorf("nothing to summarize")
	}

	transcript := renderTranscript(msgs)
	req := []memory.Message{
		{Role: "system", Content: summarySystemPrompt},
		{Role: "user", Content: "Summarize this conversation history:\n\n" + transcript},
	}

	// Summarization must never itself call tools.
	resp, err := c.Chat(ctx, req, nil)
	if err != nil {
		return "", err
	}
	out := strings.TrimSpace(resp.Content)
	if out == "" {
		return "", fmt.Errorf("summarizer returned empty content")
	}
	return out, nil
}

// renderTranscript flattens structured history into plain text. Tool calls
// become readable lines so the summarizer can reason about what the agent did
// without needing the wire format.
func renderTranscript(msgs []memory.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		switch m.Role {
		case "system":
			// Operating instructions are re-sent separately; including them
			// here wastes budget and invites the model to summarize its own
			// rules back at us.
			continue
		case "tool":
			fmt.Fprintf(&b, "[tool result] %s\n", oneLine(m.Content, 600))
		case "assistant":
			if txt := strings.TrimSpace(m.Content); txt != "" {
				fmt.Fprintf(&b, "[assistant] %s\n", oneLine(txt, 1200))
			}
			for _, tc := range m.ToolCalls {
				fmt.Fprintf(&b, "[tool call] %s(%s)\n", tc.Name, oneLine(tc.Arguments, 300))
			}
		default:
			fmt.Fprintf(&b, "[%s] %s\n", m.Role, oneLine(m.Content, 1200))
		}
	}
	return b.String()
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
