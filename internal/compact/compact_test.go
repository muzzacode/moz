package compact

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/muzzacode/moz/internal/memory"
	"github.com/muzzacode/moz/internal/tokens"
)

func tokensOf(msgs []memory.Message) int { return tokens.EstimateMessages(msgs) }

type stubSummarizer struct {
	out   string
	err   error
	calls int
}

func (s *stubSummarizer) Summarize(context.Context, []memory.Message) (string, error) {
	s.calls++
	return s.out, s.err
}

func msg(role, content string) memory.Message {
	return memory.Message{Role: role, Content: content}
}

func bulk(role string, n, size int) []memory.Message {
	out := make([]memory.Message, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, msg(role, fmt.Sprintf("m%d:", i)+strings.Repeat("x", size)))
	}
	return out
}

func testConfig() Config {
	cfg := DefaultConfig(8192)
	cfg.ReserveOutput = 1024
	return cfg
}

func TestNoCompactionUnderBudget(t *testing.T) {
	c := New(testConfig(), &stubSummarizer{out: "summary"})
	in := []memory.Message{msg("system", "rules"), msg("user", "hi"), msg("assistant", "hello")}

	out, res, err := c.Compact(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if res.Compacted {
		t.Fatal("should not compact a small conversation")
	}
	if len(out) != len(in) {
		t.Fatalf("expected %d messages, got %d", len(in), len(out))
	}
}

func TestCompactionPreservesSystemPromptAndRecent(t *testing.T) {
	sum := &stubSummarizer{out: "did earlier work"}
	c := New(testConfig(), sum)

	in := append([]memory.Message{msg("system", "OPERATING RULES")}, bulk("user", 60, 900)...)
	in = append(in, msg("user", "FINAL QUESTION"))

	out, res, err := c.Compact(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Compacted {
		t.Fatal("expected compaction")
	}
	if sum.calls != 1 {
		t.Fatalf("expected 1 summarizer call, got %d", sum.calls)
	}
	if out[0].Role != "system" || out[0].Content != "OPERATING RULES" {
		t.Fatalf("system prompt not preserved, got %+v", out[0])
	}
	if !strings.Contains(out[1].Content, "did earlier work") {
		t.Fatalf("summary not inserted, got %q", out[1].Content)
	}
	if out[len(out)-1].Content != "FINAL QUESTION" {
		t.Fatal("most recent message not preserved")
	}
	if res.TokensAfter >= res.TokensBefore {
		t.Fatalf("compaction did not reduce tokens: %d -> %d", res.TokensBefore, res.TokensAfter)
	}
}

// The retained suffix must never begin with a tool result, because the
// assistant message that requested it would be gone and the provider would
// reject the request.
func TestCompactionNeverOrphansToolResults(t *testing.T) {
	c := New(testConfig(), &stubSummarizer{out: "s"})

	in := []memory.Message{msg("system", "rules")}
	for i := 0; i < 40; i++ {
		in = append(in, msg("user", fmt.Sprintf("req%d:", i)+strings.Repeat("y", 700)))
		in = append(in, memory.Message{
			Role:      "assistant",
			ToolCalls: []memory.ToolCall{{ID: fmt.Sprintf("c%d", i), Name: "read_file", Arguments: `{"path":"a"}`}},
		})
		in = append(in, memory.Message{
			Role:       "tool",
			ToolCallID: fmt.Sprintf("c%d", i),
			Content:    strings.Repeat("z", 700),
		})
	}

	out, res, err := c.Compact(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Compacted {
		t.Fatal("expected compaction")
	}
	assertToolPairing(t, out)
}

func TestChooseCutSnapsPastToolResults(t *testing.T) {
	msgs := []memory.Message{
		msg("user", "a"),
		{Role: "assistant", ToolCalls: []memory.ToolCall{{ID: "c1", Name: "grep"}}},
		{Role: "tool", ToolCallID: "c1", Content: "result"},
		msg("assistant", "done"),
	}
	// A tiny budget would naively cut mid-group; it must snap forward.
	for budget := 0; budget < 40; budget++ {
		cut := chooseCut(msgs, budget, 0)
		if cut < len(msgs) && msgs[cut].Role == "tool" {
			t.Fatalf("budget %d produced orphaned tool result at index %d", budget, cut)
		}
	}
}

func TestSummarizerFailureFallsBack(t *testing.T) {
	sum := &stubSummarizer{err: fmt.Errorf("model unavailable")}
	c := New(testConfig(), sum)

	in := append([]memory.Message{msg("system", "rules"), msg("user", "BUILD THE THING")}, bulk("assistant", 60, 900)...)

	out, res, err := c.Compact(context.Background(), in)
	if err != nil {
		t.Fatalf("compaction must degrade, not fail: %v", err)
	}
	if !res.SummaryFailed {
		t.Fatal("expected SummaryFailed")
	}
	if !res.Compacted {
		t.Fatal("expected compaction despite summarizer failure")
	}
	joined := out[1].Content
	if !strings.Contains(joined, "BUILD THE THING") {
		t.Fatalf("fallback summary should retain original intent, got %q", joined)
	}
}

func TestShrinkToolResultsCapsLargeOutput(t *testing.T) {
	in := []memory.Message{
		msg("user", "go"),
		{Role: "tool", ToolCallID: "c1", Content: strings.Repeat("q", 50000)},
	}
	out := shrinkToolResults(in, 1000)
	if len(out) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(out))
	}
	if len(out[1].Content) > 1200 {
		t.Fatalf("tool result not shrunk: %d chars", len(out[1].Content))
	}
	if !strings.Contains(out[1].Content, "characters omitted") {
		t.Fatal("expected omission marker")
	}
	if len(in[1].Content) != 50000 {
		t.Fatal("shrinkToolResults must not mutate the input")
	}
}

func TestShrinkToolResultsLeavesSmallOutputAlone(t *testing.T) {
	in := []memory.Message{{Role: "tool", Content: "small"}}
	out := shrinkToolResults(in, 1000)
	if &out[0] != &in[0] {
		// Same backing array is expected when nothing changed.
		if out[0].Content != "small" {
			t.Fatal("unexpected mutation")
		}
	}
}

// A small-context model must still get a sane budget even when the configured
// output reserve is larger than its entire window.
func TestUsableHandlesReserveLargerThanWindow(t *testing.T) {
	c := New(Config{ContextLength: 3000, ReserveOutput: 8192}, nil)
	u := c.usable()
	if u > 3000 {
		t.Fatalf("usable %d exceeds the context window", u)
	}
	if u < minUsableTokens {
		t.Fatalf("usable %d below floor", u)
	}
	if c.triggerAt() >= 3000 {
		t.Fatalf("trigger %d would never fire inside a 3000 token window", c.triggerAt())
	}
	if c.targetAt() >= c.triggerAt() {
		t.Fatalf("target %d must be below trigger %d to avoid re-compacting every turn", c.targetAt(), c.triggerAt())
	}
}

func TestSmallContextModelActuallyCompacts(t *testing.T) {
	sum := &stubSummarizer{out: "compressed"}
	c := New(Config{
		ContextLength: 3000,
		ReserveOutput: 8192,
		TriggerRatio:  0.75,
		TargetRatio:   0.45,
		KeepRecent:    2,
		MaxToolResult: 2000,
	}, sum)

	in := append([]memory.Message{msg("system", "rules")}, bulk("user", 30, 400)...)
	_, res, err := c.Compact(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Compacted {
		t.Fatalf("expected compaction inside a 3000 token window (before=%d tokens)", res.TokensBefore)
	}
}

// Budgets must scale with the window. Fixed limits break small local models.
func TestDefaultConfigScalesWithWindow(t *testing.T) {
	small := DefaultConfig(3000)
	large := DefaultConfig(1000000)

	if small.MaxToolResult >= large.MaxToolResult {
		t.Fatalf("tool result cap must grow with the window: %d vs %d", small.MaxToolResult, large.MaxToolResult)
	}
	if small.KeepRecent > large.KeepRecent {
		t.Fatalf("recent retention must grow with the window: %d vs %d", small.KeepRecent, large.KeepRecent)
	}
	// A single tool result must never dominate a small window.
	if smallTokens := small.MaxToolResult / charsPerTokenApprox; smallTokens > 3000/3 {
		t.Fatalf("a single tool result may claim %d tokens of a 3000 token window", smallTokens)
	}
	if large.ReserveOutput > 8192 {
		t.Fatalf("reserve should cap at 8192, got %d", large.ReserveOutput)
	}
}

// A large tool result inside a small window must be shrunk enough that the
// conversation still fits, since dropping messages alone cannot help.
func TestSmallWindowShrinksLargeToolResultToFit(t *testing.T) {
	cfg := DefaultConfig(3000)
	cfg.FixedOverhead = 1252
	c := New(cfg, &stubSummarizer{out: "s"})

	in := []memory.Message{
		msg("system", strings.Repeat("r", 1500)),
		msg("user", "read the readme and summarize"),
		{Role: "assistant", ToolCalls: []memory.ToolCall{{ID: "c1", Name: "read_file", Arguments: `{"path":"README.md"}`}}},
		{Role: "tool", ToolCallID: "c1", Content: strings.Repeat("d", 40000)},
	}

	out, _, err := c.Compact(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	assertToolPairing(t, out)

	got := tokensOf(out)
	if got > 3000 {
		t.Fatalf("history still exceeds the 3000 token window: %d tokens", got)
	}
}

func assertToolPairing(t *testing.T, msgs []memory.Message) {
	t.Helper()
	seen := map[string]bool{}
	for _, m := range msgs {
		for _, tc := range m.ToolCalls {
			seen[tc.ID] = true
		}
	}
	for i, m := range msgs {
		if m.Role != "tool" {
			continue
		}
		if !seen[m.ToolCallID] {
			t.Fatalf("orphaned tool result at index %d (tool_call_id=%q)", i, m.ToolCallID)
		}
	}
}
