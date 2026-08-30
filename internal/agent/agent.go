package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/muzzacode/moz/internal/adaptive"
	"github.com/muzzacode/moz/internal/config"
	"github.com/muzzacode/moz/internal/credentials"
	"github.com/muzzacode/moz/internal/llm"
	"github.com/muzzacode/moz/internal/memory"
	"github.com/muzzacode/moz/internal/models"
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

	// Build messages: system prompt + session + user task.
	messages := append([]memory.Message{}, session.Messages...)
	messages = append(messages, memory.Message{Role: "user", Content: task})

	systemPrompt := buildSystemPrompt()
	messages = prependMessage(messages, memory.Message{Role: "system", Content: systemPrompt})

	client := llm.New(profile, r.Creds)

	maxTurns := 15
	for turn := 0; turn < maxTurns; turn++ {
		turnStart := time.Now()
		out <- Event{Type: "step", Step: fmt.Sprintf("turn %d: reasoning", turn+1), Model: profile.Name, Elapsed: time.Since(start)}

		resp, err := client.Chat(ctx, messages, tools.Definitions())
		if err != nil {
			out <- Event{Type: "error", Error: err.Error(), Elapsed: time.Since(start)}
			return
		}

		out <- Event{Type: "usage", Usage: resp.Usage, Elapsed: time.Since(start)}

		if len(resp.ToolCalls) == 0 {
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
				out <- Event{Type: "error", Error: "cancelled", Elapsed: time.Since(start)}
				return
			}

			out <- Event{Type: "step", Step: fmt.Sprintf("executing %s", tc.Name), Model: profile.Name, Elapsed: time.Since(start)}

			result := r.Toolkit.Execute(tools.ToolCall{
				ID:        tc.ID,
				Name:      tc.Name,
				Arguments: tc.Arguments,
			})
			out <- Event{Type: "tool_result", ToolResult: &result, Elapsed: time.Since(start)}

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

func buildSystemPrompt() string {
	return `You are Moz, an agentic coding assistant running in a terminal. You have access to these tools:
- read_file(path)
- list_dir(path)
- grep(pattern, path)
- web_search(query)
- exec(command)
- git_status(cwd)
- git_diff(cwd)
- write_file(path, content)
- edit_file(path, old_string, new_string)
- add_todo(text)
- list_todos()
- mark_done(id)

Rules:
1. For multi-step tasks, first create a plan using add_todo. As you finish each step, call mark_done. Call list_todos if you need to see the current plan.
2. Use the fewest tools possible.
3. To create or overwrite a file, use write_file. NEVER use exec with echo or redirection.
4. To change a file, use edit_file with old_string and new_string. NEVER use sed or sed inside exec.
5. exec is only for commands like git, go, ls, make, tests, etc.
6. Prefer reading/grepping before running commands.

When you need a tool, you MUST respond with ONLY a JSON object in one of these exact forms, with no explanation before or after:

Single tool:
{"name": "list_dir", "arguments": {"path": "."}}

Multiple tools:
{"tool_calls": [{"name": "grep", "arguments": {"pattern": "func", "path": "."}}, {"name": "read_file", "arguments": {"path": "main.go"}}]}

Use the exact tool names listed above. When you have enough information to answer, respond in plain text with a concise answer.`
}

func prependMessage(msgs []memory.Message, m memory.Message) []memory.Message {
	return append([]memory.Message{m}, msgs...)
}
