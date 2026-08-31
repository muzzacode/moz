package agent

import (
	"context"
	"fmt"
	"os"
	"strings"
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
	// Budget accumulates spend so routing can stop escalating once a session
	// has cost enough.
	Budget *adaptive.Budget
}

func New(cfg *config.Config, reg *models.Registry, tk *tools.Toolkit) *Runner {
	return NewWithDeps(cfg, reg, tk, credentials.New(), nil)
}

// NewWithDeps builds a runner sharing credentials and a spend budget with the
// caller, so cost accounting spans the whole session rather than one task.
func NewWithDeps(
	cfg *config.Config,
	reg *models.Registry,
	tk *tools.Toolkit,
	creds *credentials.Manager,
	budget *adaptive.Budget,
) *Runner {
	if creds == nil {
		creds = credentials.New()
	}
	router := adaptive.NewWithOptions(reg, creds, RoutingOptions(cfg), budget)
	return &Runner{
		Config:   cfg,
		Registry: reg,
		Toolkit:  tk,
		Creds:    creds,
		Router:   router,
		Budget:   router.Budget,
	}
}

// RoutingOptions translates configuration into router settings.
func RoutingOptions(cfg *config.Config) adaptive.Options {
	if cfg == nil {
		return adaptive.Options{PreferLocal: true}
	}
	return adaptive.Options{
		PreferLocal:      cfg.Adaptive.PreferLocal,
		CloudThreshold:   cfg.Adaptive.CloudThreshold,
		PremiumThreshold: cfg.Adaptive.PremiumThreshold,
		MaxSessionCost:   cfg.Adaptive.MaxSessionCost,
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

	newClient := func(p *models.Profile) *llm.Client {
		c := llm.New(p, r.Creds)
		if r.Config != nil {
			c = c.WithTimeout(r.Config.RequestTimeout())
		}
		// Surface retries so a rate-limited provider looks like progress rather
		// than a hang.
		return c.WithRetry(0, func(n llm.RetryNotice) {
			out <- Event{
				Type:    "warning",
				Step:    fmt.Sprintf("provider error, retrying in %s (attempt %d/%d)", n.Delay.Round(time.Millisecond), n.Attempt, n.Max),
				Elapsed: time.Since(start),
			}
		})
	}

	client := newClient(profile)
	toolDefs := tools.Definitions()
	compactCfg := compact.DefaultConfig(profile.ContextLength)
	// Tool schemas are re-sent on every request and are invisible in the
	// message list, so they must be charged against the budget explicitly.
	compactCfg.FixedOverhead = tokens.EstimateJSON(toolDefs)
	compactor := compact.New(compactCfg, client)

	// escalated guards against repeatedly upgrading within one task.
	escalated := false
	taskClass := classOf(task)
	// stalls counts consecutive empty replies before escalating.
	stalls := 0

	// switchTo moves the task onto another model mid-flight.
	switchTo := func(next *models.Profile, cause, verb string) bool {
		escalated = true
		out <- Event{
			Type:    "warning",
			Step:    fmt.Sprintf("%s on %s; %s to %s", cause, profile.Name, verb, next.Name),
			Elapsed: time.Since(start),
		}
		profile = next
		client = newClient(profile)
		cfg := compact.DefaultConfig(profile.ContextLength)
		cfg.FixedOverhead = tokens.EstimateJSON(toolDefs)
		compactor = compact.New(cfg, client)
		// The replacement may have a different tool-calling style.
		messages = replaceSystemPrompt(messages, appendProjectContext(
			buildSystemPrompt(profile.SupportsNativeTools()),
			verifyState.command,
			instructions,
		))
		return true
	}

	// tryEscalate moves up a tier when the current model is not capable enough.
	tryEscalate := func(cause string) bool {
		if escalated || r.Router == nil {
			return false
		}
		if next := r.Router.Escalate(profile, taskClass); next != nil {
			return switchTo(next, cause, "escalating")
		}
		return false
	}

	// tryRecover handles a model that cannot run at all, such as a provider with
	// no credit left. Escalation cannot help there, but another provider can.
	tryRecover := func(cause string) bool {
		if escalated || r.Router == nil {
			return false
		}
		if next := r.Router.Escalate(profile, taskClass); next != nil {
			return switchTo(next, cause, "escalating")
		}
		if next := r.Router.Fallback(profile, taskClass); next != nil {
			return switchTo(next, cause, "falling back")
		}
		return false
	}

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
			// Retries inside the client are already exhausted, so this model is
			// not going to succeed. Another one might, at any tier: the failure
			// may be the provider rather than the model's ability.
			//
			// The cause is carried into the message: silently swapping models
			// leaves no way to tell a broken key from a broken request.
			if tryRecover(fmt.Sprintf("failed (%s)", shortErr(err))) {
				continue
			}
			out <- Event{Type: "error", Error: err.Error(), Elapsed: time.Since(start)}
			return
		}

		// An empty reply means the model produced neither an answer nor a tool
		// call, which is a hallmark of a model that cannot follow the protocol.
		if len(resp.ToolCalls) == 0 && strings.TrimSpace(resp.Content) == "" {
			stalls++
			if stalls >= maxStalls && tryEscalate("model returned nothing usable") {
				stalls = 0
				continue
			}
			if stalls >= maxStalls {
				out <- Event{Type: "error", Error: "model returned no usable output", Elapsed: time.Since(start)}
				return
			}
			continue
		}
		stalls = 0

		// Record spend before the next routing decision so a budget ceiling is
		// enforced against actual usage rather than an estimate.
		if r.Budget != nil {
			r.Budget.Add(profile.ID, resp.Usage)
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

	out <- Event{Type: "error", Error: fmt.Sprintf("reached the %d turn limit without finishing", maxTurns), Elapsed: time.Since(start)}
}

// maxStalls is how many consecutive empty replies are tolerated before the model
// is considered incapable of following the protocol.
const maxStalls = 2

// shortErr condenses a provider error for a one-line status message, keeping the
// part that identifies the cause.
func shortErr(err error) string {
	s := strings.Join(strings.Fields(err.Error()), " ")
	// Provider errors often wrap a JSON body; the message field is the useful part.
	if i := strings.Index(s, `"message":"`); i >= 0 {
		rest := s[i+len(`"message":"`):]
		if j := strings.Index(rest, `"`); j > 0 {
			s = rest[:j]
		}
	}
	const max = 160
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

// classOf classifies a task so escalation searches the right stack.
func classOf(task string) models.TaskClass {
	return adaptive.Classify(task).Class
}

// replaceSystemPrompt swaps the leading system message.
//
// Escalating between providers can change the tool-calling style, so the
// operating instructions must be rewritten rather than appended to.
func replaceSystemPrompt(msgs []memory.Message, prompt string) []memory.Message {
	out := make([]memory.Message, 0, len(msgs)+1)
	replaced := false
	for _, m := range msgs {
		if !replaced && m.Role == "system" {
			m.Content = prompt
			replaced = true
		}
		out = append(out, m)
	}
	if !replaced {
		return prependMessage(out, memory.Message{Role: "system", Content: prompt, Timestamp: time.Now().UTC()})
	}
	return out
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
