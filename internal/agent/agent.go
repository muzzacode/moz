package agent

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/muzzacode/moz/internal/adaptive"
	"github.com/muzzacode/moz/internal/compact"
	"github.com/muzzacode/moz/internal/config"
	"github.com/muzzacode/moz/internal/credentials"
	"github.com/muzzacode/moz/internal/llm"
	"github.com/muzzacode/moz/internal/memory"
	"github.com/muzzacode/moz/internal/models"
	"github.com/muzzacode/moz/internal/project"
	"github.com/muzzacode/moz/internal/tokens"
	"github.com/muzzacode/moz/internal/tools"
	openai "github.com/sashabaranov/go-openai"
)

type Event struct {
	Type       string
	Step       string
	Content    string
	ToolCall   *llm.ToolCall
	ToolResult *tools.ToolResult
	Model      string
	Elapsed    time.Duration
	Usage      openai.Usage
	Error      string
}

type Runner struct {
	Config   *config.Config
	Registry *models.Registry
	Toolkit  *tools.Toolkit
	Creds    *credentials.Manager
	Router   *adaptive.Router
}

func New(cfg *config.Config, reg *models.Registry, tk *tools.Toolkit) *Runner {
	creds := credentials.New()
	router := adaptive.New(reg, creds)
	router.PreferLocal = cfg.Adaptive.PreferLocal
	return &Runner{
		Config:   cfg,
		Registry: reg,
		Toolkit:  tk,
		Creds:    creds,
		Router:   router,
	}
}

func (r *Runner) Run(ctx context.Context, profile *models.Profile, task string, session *memory.Session, out chan<- Event, approvalCh <-chan bool) {
	defer close(out)

	start := time.Now()

	if profile == nil {
		decision, err := r.Router.Select(task)
		if err != nil {
			out <- Event{Type: "error", Error: err.Error(), Elapsed: time.Since(start)}
			return
		}
		profile = decision.Profile
	}

	out <- Event{Type: "step", Step: "planning", Model: profile.Name, Elapsed: time.Since(start)}

	verifyState := newVerifyState(r.Config)

	// Build messages: system prompt + session + user task.
	messages := append([]memory.Message{}, session.Messages...)
	messages = append(messages, memory.Message{Role: "user", Content: task})

	// Load the project's own instructions, which carry knowledge that cannot be
	// inferred from source, such as a required toolchain version.
	var instructions *project.Instructions
	if cwd, err := os.Getwd(); err == nil {
		if ins, ok := project.Load(cwd); ok {
			instructions = ins
			out <- Event{
				Type:    "step",
				Step:    "loaded project instructions from " + ins.Source,
				Model:   profile.Name,
				Elapsed: time.Since(start),
			}
		}
	}

	systemPrompt := appendProjectContext(
		buildSystemPrompt(profile.SupportsNativeTools()),
		verifyState.command,
		instructions,
	)
	messages = prependMessage(messages, memory.Message{Role: "system", Content: systemPrompt})

	client := llm.New(profile, r.Creds)
	if r.Config != nil {
		client = client.WithTimeout(r.Config.RequestTimeout())
	}
	// Surface retries so a rate-limited provider looks like progress rather
	// than a hang.
	client = client.WithRetry(0, func(n llm.RetryNotice) {
		out <- Event{
			Type:    "warning",
			Step:    fmt.Sprintf("provider error, retrying in %s (attempt %d/%d)", n.Delay.Round(time.Millisecond), n.Attempt, n.Max),
			Elapsed: time.Since(start),
		}
	})
	toolDefs := tools.Definitions()
	compactCfg := compact.DefaultConfig(profile.ContextLength)
	// Tool schemas are re-sent on every request and are invisible in the
	// message list, so they must be charged against the budget explicitly.
	compactCfg.FixedOverhead = tokens.EstimateJSON(toolDefs)
	compactor := compact.New(compactCfg, client)

	maxTurns := r.maxTurns()
	for turn := 0; turn < maxTurns; turn++ {
		if err := ctx.Err(); err != nil {
			out <- Event{Type: "cancelled", Step: "cancelled", Elapsed: time.Since(start)}
			return
		}

		turnStart := time.Now()

		var compRes compact.Result
		messages, compRes = r.compact(ctx, compactor, messages, out, start)
		if compRes.Compacted {
			out <- Event{
				Type:    "compacted",
				Step:    fmt.Sprintf("compacted context: %d→%d tokens", compRes.TokensBefore, compRes.TokensAfter),
				Model:   profile.Name,
				Elapsed: time.Since(start),
			}
		}

		out <- Event{Type: "step", Step: fmt.Sprintf("turn %d/%d: reasoning", turn+1, maxTurns), Model: profile.Name, Elapsed: time.Since(start)}

		resp, err := client.Chat(ctx, messages, toolDefs)
		if err != nil {
			if ctx.Err() != nil {
				out <- Event{Type: "cancelled", Step: "cancelled", Elapsed: time.Since(start)}
				return
			}
			out <- Event{Type: "error", Error: err.Error(), Elapsed: time.Since(start)}
			return
		}

		out <- Event{Type: "usage", Usage: resp.Usage, Elapsed: time.Since(start)}

		if len(resp.ToolCalls) == 0 {
			// The model believes it is finished. If it changed files, run the
			// project's own verification and hand back any failure so it can
			// fix the problem rather than reporting false success.
			if feedback, ok := r.runVerification(ctx, &verifyState, out, start, profile); ok {
				messages = append(messages, memory.Message{
					Role:      "user",
					Content:   feedback,
					Timestamp: time.Now().UTC(),
				})
				continue
			}

			// Final answer. Stream it for nicer UI.
			out <- Event{Type: "step", Step: "finalizing", Model: profile.Name, Elapsed: time.Since(start)}
			streamOut := make(chan llm.StreamEvent)
			go client.ChatStream(ctx, messages, streamOut)
			for ev := range streamOut {
				if ev.Err != nil {
					out <- Event{Type: "error", Error: ev.Err.Error(), Elapsed: time.Since(start)}
					return
				}
				if ev.Done {
					break
				}
				out <- Event{Type: "message", Content: ev.Content, Model: profile.Name, Elapsed: time.Since(start)}
			}
			out <- Event{Type: "done", Elapsed: time.Since(start)}
			return
		}

		// Add the assistant message with tool calls to the conversation.
		assistantMsg := memory.Message{
			Role:      "assistant",
			Content:   resp.Content,
			Timestamp: time.Now().UTC(),
		}
		for _, tc := range resp.ToolCalls {
			assistantMsg.ToolCalls = append(assistantMsg.ToolCalls, memory.ToolCall{
				ID:        tc.ID,
				Name:      tc.Name,
				Arguments: string(tc.Arguments),
			})
		}
		messages = append(messages, assistantMsg)

		// Execute tool calls.
		for _, tc := range resp.ToolCalls {
			out <- Event{Type: "tool_call", ToolCall: &tc, Model: profile.Name, Elapsed: time.Since(start)}

			// Wait for approval.
			select {
			case approved := <-approvalCh:
				if !approved {
					out <- Event{Type: "tool_result", ToolResult: &tools.ToolResult{ID: tc.ID, Name: tc.Name, Error: "user denied"}, Elapsed: time.Since(start)}
					// Still append a tool result so the conversation is valid.
					messages = append(messages, memory.Message{
						Role:       "tool",
						Content:    "user denied",
						ToolCallID: tc.ID,
						Timestamp:  time.Now().UTC(),
					})
					continue
				}
			case <-ctx.Done():
				out <- Event{Type: "cancelled", Step: "cancelled", Elapsed: time.Since(start)}
				return
			}

			out <- Event{Type: "step", Step: fmt.Sprintf("executing %s", tc.Name), Model: profile.Name, Elapsed: time.Since(start)}

			call := tools.ToolCall{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments}

			// spawn_agents needs a model client, which the toolkit does not
			// have, so the agent handles it.
			var result tools.ToolResult
			if tools.ResolveName(tc.Name) == "spawn_agents" {
				result = r.runSpawnAgents(ctx, profile, call, out, start)
			} else {
				result = r.Toolkit.Execute(call)
			}
			out <- Event{Type: "tool_result", ToolResult: &result, Elapsed: time.Since(start)}

			// Only a successful mutation makes verification worthwhile.
			if result.Error == "" && mutatesFiles(tc.Name) {
				verifyState.markDirty()
			}

			// Add tool result to conversation.
			content := result.Content
			if result.Error != "" {
				content = fmt.Sprintf("error: %s", result.Error)
			}
			messages = append(messages, memory.Message{
				Role:       "tool",
				Content:    content,
				ToolCallID: tc.ID,
				Timestamp:  time.Now().UTC(),
			})
		}

		// Re-add system prompt if needed? The conversation already has it at start.
		_ = turnStart
	}

	out <- Event{Type: "error", Error: "reached maximum agent turns", Elapsed: time.Since(start)}
}

func prependMessage(msgs []memory.Message, m memory.Message) []memory.Message {
	return append([]memory.Message{m}, msgs...)
}

// maxTurns resolves the per-task tool-call budget. Complex refactors need many
// more turns than a one-line edit, so this is configurable rather than fixed.
func (r *Runner) maxTurns() int {
	if r.Config != nil && r.Config.AgentOpts.MaxTurns > 0 {
		return r.Config.AgentOpts.MaxTurns
	}
	return config.DefaultMaxTurns
}

// compact shrinks history to fit the model's context window. Compaction failure
// is never fatal: the agent proceeds with the uncompacted history and lets the
// provider decide, which is strictly better than aborting the user's task.
func (r *Runner) compact(
	ctx context.Context,
	c *compact.Compactor,
	messages []memory.Message,
	out chan<- Event,
	start time.Time,
) ([]memory.Message, compact.Result) {
	compacted, res, err := c.Compact(ctx, messages)
	if err != nil {
		out <- Event{Type: "warning", Step: fmt.Sprintf("context compaction failed: %v", err), Elapsed: time.Since(start)}
		return messages, compact.Result{}
	}
	if res.SummaryFailed {
		out <- Event{Type: "warning", Step: "context summary unavailable; trimmed oldest turns", Elapsed: time.Since(start)}
	}
	return compacted, res
}
