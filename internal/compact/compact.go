// Package compact keeps a conversation inside a model's context window.
//
// Strategy: preserve the leading system prompt verbatim, preserve the most
// recent turns verbatim, and replace the middle with an LLM-generated summary.
//
// The critical invariant is tool-call pairing. A "tool" message is only valid
// if the assistant message that requested it is still present. Because we keep
// a contiguous suffix, the only place pairing can break is the leading edge of
// that suffix, so every cut point is snapped forward past orphaned tool
// results.
package compact

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/muzzacode/moz/internal/memory"
	"github.com/muzzacode/moz/internal/tokens"
)

// minUsableTokens is a floor so a misconfigured tiny window cannot make the
// budget zero and starve the conversation entirely.
const minUsableTokens = 512

// Summarizer turns a slice of history into a compact prose summary.
type Summarizer interface {
	Summarize(ctx context.Context, msgs []memory.Message) (string, error)
}

type Config struct {
	// ContextLength is the model's total context window in tokens.
	ContextLength int
	// ReserveOutput is how many tokens to leave available for the reply.
	ReserveOutput int
	// TriggerRatio is the fraction of usable context that triggers compaction.
	TriggerRatio float64
	// TargetRatio is the fraction of usable context to aim for afterwards, so
	// we do not re-compact on every single turn.
	TargetRatio float64
	// KeepRecent is the minimum number of trailing messages kept verbatim.
	KeepRecent int
	// MaxToolResult caps how many characters of a single tool result are
	// retained in history.
	MaxToolResult int
	// FixedOverhead accounts for tokens consumed by every request but absent
	// from the message list, principally the JSON Schema for each tool. This is
	// over 1200 tokens for Moz's toolset, so ignoring it makes compaction fire
	// far too late.
	FixedOverhead int
}

// DefaultConfig derives a budget from the model's context window.
//
// Every limit scales with the window. Fixed limits break at both extremes: a
// 6000-character tool result is two thirds of an 8k window, while keeping only
// 6 recent messages wastes a 1M window.
func DefaultConfig(contextLength int) Config {
	if contextLength <= 0 {
		contextLength = 128000
	}

	reserve := contextLength / 4
	if reserve > 8192 {
		reserve = 8192
	}
	approxUsable := contextLength - reserve

	// A single tool result may claim at most a fifth of the history budget.
	// The *charsPerTokenApprox factor converts the token budget into characters.
	maxToolResult := clampInt(approxUsable*charsPerTokenApprox/5, 800, 24000)

	// Roughly one retained turn per 2k tokens of budget.
	keepRecent := clampInt(approxUsable/2000, 2, 10)

	return Config{
		ContextLength: contextLength,
		ReserveOutput: reserve,
		TriggerRatio:  0.75,
		TargetRatio:   0.45,
		KeepRecent:    keepRecent,
		MaxToolResult: maxToolResult,
	}
}

// charsPerTokenApprox mirrors the estimator in the tokens package and is used
// to convert token budgets into character limits.
const charsPerTokenApprox = 3

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

type Result struct {
	Compacted      bool
	TokensBefore   int
	TokensAfter    int
	MessagesBefore int
	MessagesAfter  int
	Summarized     int
	SummaryFailed  bool
}

type Compactor struct {
	Config     Config
	Summarizer Summarizer
}

func New(cfg Config, s Summarizer) *Compactor {
	return &Compactor{Config: cfg, Summarizer: s}
}

// usable is how many tokens of history may be sent.
//
// ReserveOutput is treated as a request, not a guarantee: a small-context model
// must never have its reserve exceed the window itself, or the budget would be
// negative and compaction would either never fire or fire forever.
func (c *Compactor) usable() int {
	ctxLen := c.Config.ContextLength
	if ctxLen <= 0 {
		ctxLen = 128000
	}

	reserve := c.Config.ReserveOutput
	if reserve < 0 {
		reserve = 0
	}
	if maxReserve := ctxLen / 2; reserve > maxReserve {
		reserve = maxReserve
	}

	overhead := c.Config.FixedOverhead
	if overhead < 0 {
		overhead = 0
	}
	// Overhead is also capped so a large toolset on a small model cannot make
	// the history budget vanish.
	if maxOverhead := ctxLen / 4; overhead > maxOverhead {
		overhead = maxOverhead
	}

	u := ctxLen - reserve - overhead
	if u < minUsableTokens {
		u = minUsableTokens
	}
	if u > ctxLen {
		u = ctxLen
	}
	return u
}

func (c *Compactor) triggerAt() int {
	r := c.Config.TriggerRatio
	if r <= 0 || r > 1 {
		r = 0.75
	}
	return int(float64(c.usable()) * r)
}

func (c *Compactor) targetAt() int {
	r := c.Config.TargetRatio
	if r <= 0 || r > 1 {
		r = 0.45
	}
	return int(float64(c.usable()) * r)
}

// Compact returns a conversation that fits the configured budget.
func (c *Compactor) Compact(ctx context.Context, msgs []memory.Message) ([]memory.Message, Result, error) {
	res := Result{
		MessagesBefore: len(msgs),
		TokensBefore:   tokens.EstimateMessages(msgs),
	}

	work := shrinkToolResults(msgs, c.Config.MaxToolResult)

	if tokens.EstimateMessages(work) <= c.triggerAt() {
		res.TokensAfter = tokens.EstimateMessages(work)
		res.MessagesAfter = len(work)
		return work, res, nil
	}

	head, rest := splitLeadingSystem(work)
	headTokens := tokens.EstimateMessages(head)

	cut := chooseCut(rest, c.targetAt()-headTokens, c.Config.KeepRecent)
	if cut <= 0 {
		// Nothing safe to summarize; return the shrunk history as the best
		// available option rather than failing the turn.
		res.TokensAfter = tokens.EstimateMessages(work)
		res.MessagesAfter = len(work)
		return work, res, nil
	}

	older := rest[:cut]
	recent := rest[cut:]

	summary, err := c.summarize(ctx, older)
	if err != nil {
		res.SummaryFailed = true
		summary = fallbackSummary(older)
	}

	out := make([]memory.Message, 0, len(head)+1+len(recent))
	out = append(out, head...)
	out = append(out, memory.Message{
		Role:      "system",
		Content:   summary,
		Timestamp: time.Now().UTC(),
	})
	out = append(out, recent...)

	res.Compacted = true
	res.Summarized = len(older)
	res.TokensAfter = tokens.EstimateMessages(out)
	res.MessagesAfter = len(out)
	return out, res, nil
}

func (c *Compactor) summarize(ctx context.Context, older []memory.Message) (string, error) {
	if c.Summarizer == nil {
		return "", fmt.Errorf("no summarizer configured")
	}
	s, err := c.Summarizer.Summarize(ctx, older)
	if err != nil {
		return "", err
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("empty summary")
	}
	return "Summary of earlier conversation:\n" + s, nil
}

// splitLeadingSystem separates the leading run of system messages, which always
// contains the agent's operating instructions and must survive compaction.
func splitLeadingSystem(msgs []memory.Message) (head, rest []memory.Message) {
	i := 0
	for i < len(msgs) && msgs[i].Role == "system" {
		i++
	}
	return msgs[:i], msgs[i:]
}

// chooseCut returns the index in msgs where the retained suffix should begin.
//
// It walks backwards accumulating tokens until the recent budget is spent, then
// snaps the boundary forward so the suffix never starts with an orphaned tool
// result.
func chooseCut(msgs []memory.Message, recentBudget, keepRecent int) int {
	if len(msgs) == 0 {
		return 0
	}
	if recentBudget < 0 {
		recentBudget = 0
	}

	cut := len(msgs)
	used := 0
	kept := 0

	for i := len(msgs) - 1; i >= 0; i-- {
		t := tokens.EstimateMessage(msgs[i])
		// Always honour keepRecent even if it exceeds the budget, otherwise the
		// model loses the turn it is currently working on.
		if kept >= keepRecent && used+t > recentBudget {
			break
		}
		used += t
		kept++
		cut = i
	}

	return snapForward(msgs, cut)
}

// snapForward advances idx past any message that cannot legally start a
// conversation suffix. A "tool" message is meaningless without the assistant
// message that requested it.
func snapForward(msgs []memory.Message, idx int) int {
	for idx < len(msgs) && msgs[idx].Role == "tool" {
		idx++
	}
	return idx
}

// shrinkToolResults caps oversized tool output. A single web_fetch or large
// file read can otherwise dominate the entire window.
func shrinkToolResults(msgs []memory.Message, max int) []memory.Message {
	if max <= 0 {
		return msgs
	}

	var out []memory.Message
	copied := false

	for i, m := range msgs {
		if m.Role != "tool" || len(m.Content) <= max {
			if copied {
				out = append(out, m)
			}
			continue
		}
		if !copied {
			out = append(out, msgs[:i]...)
			copied = true
		}
		shrunk := m
		shrunk.Content = clip(m.Content, max)
		out = append(out, shrunk)
	}

	if !copied {
		return msgs
	}
	return out
}

// clip keeps the head and tail of a string, which preserves both the shape of
// the output and any trailing error or summary lines.
func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max < 200 {
		return s[:max] + "\n... (truncated)"
	}
	head := max * 2 / 3
	tail := max - head
	omitted := len(s) - head - tail
	return fmt.Sprintf("%s\n... (%d characters omitted) ...\n%s", s[:head], omitted, s[len(s)-tail:])
}

func fallbackSummary(older []memory.Message) string {
	var b strings.Builder
	b.WriteString("Earlier conversation was trimmed to fit the context window. ")
	fmt.Fprintf(&b, "%d messages were dropped. Recent turns follow.", len(older))

	// Surface the earliest user intent so the agent does not lose the goal.
	for _, m := range older {
		if m.Role == "user" && strings.TrimSpace(m.Content) != "" {
			b.WriteString("\nOriginal request: ")
			b.WriteString(clip(strings.Join(strings.Fields(m.Content), " "), 500))
			break
		}
	}
	return b.String()
}
