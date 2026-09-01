// Package subagent runs independent read-only investigations in parallel.
//
// Broad questions such as "how is auth wired up" require reading many files.
// Doing that inline burns the parent's context window on material it will never
// need again. A sub-agent investigates in its own isolated conversation and
// returns only its conclusion.
//
// Sub-agents are deliberately read-only. Several agents writing concurrently
// would corrupt files and produce unreviewable changes, and parallel approval
// prompts are unusable in a terminal.
package subagent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/muzzacode/moz/internal/compact"
	"github.com/muzzacode/moz/internal/credentials"
	"github.com/muzzacode/moz/internal/llm"
	"github.com/muzzacode/moz/internal/memory"
	"github.com/muzzacode/moz/internal/models"
	"github.com/muzzacode/moz/internal/tokens"
	"github.com/muzzacode/moz/internal/tools"
)

// Limits. Fan-out is bounded because every sub-agent costs tokens and provider
// rate limit, and an unbounded swarm is the fastest way to get throttled.
const (
	DefaultMaxParallel = 3
	DefaultMaxTurns    = 12
	DefaultTimeout     = 4 * time.Minute
	MaxTasks           = 6
	// maxOutputChars caps a single sub-agent's contribution to the parent's
	// context, which is the whole reason this package exists.
	maxOutputChars = 4000
)

const systemPrompt = `You are a focused research sub-agent inside a coding assistant.

You investigate one question and report back. You cannot modify anything: you have read-only tools only.

Rules:
1. Investigate efficiently. Use find_files to locate files, outline to understand a large file's shape, and grep to find usages.
2. Base every statement on what you actually read. Never speculate, and never invent file paths, symbols, or behaviour.
3. Report concisely: the answer first, then the specific files and line numbers that support it.
4. If you cannot determine the answer, say so plainly and describe what you checked.

Your reply is consumed by another agent, not a human. Omit pleasantries.`

type Task struct {
	// Prompt is the question the sub-agent must answer.
	Prompt string
}

type Result struct {
	Prompt  string
	Output  string
	Turns   int
	Elapsed time.Duration
	Err     error
}

type Runner struct {
	Profile *models.Profile
	Creds   *credentials.Manager
	// Toolkit must be read-only. NewRunner enforces this.
	Toolkit     *tools.Toolkit
	MaxParallel int
	MaxTurns    int
	Timeout     time.Duration
}

// NewRunner builds a runner with a read-only view of the toolkit.
func NewRunner(p *models.Profile, creds *credentials.Manager, tk *tools.Toolkit) *Runner {
	return &Runner{
		Profile:     p,
		Creds:       creds,
		Toolkit:     tk.ReadOnlyCopy(),
		MaxParallel: DefaultMaxParallel,
		MaxTurns:    DefaultMaxTurns,
		Timeout:     DefaultTimeout,
	}
}

func (r *Runner) maxParallel() int {
	if r.MaxParallel <= 0 {
		return DefaultMaxParallel
	}
	return r.MaxParallel
}

func (r *Runner) maxTurns() int {
	if r.MaxTurns <= 0 {
		return DefaultMaxTurns
	}
	return r.MaxTurns
}

func (r *Runner) timeout() time.Duration {
	if r.Timeout <= 0 {
		return DefaultTimeout
	}
	return r.Timeout
}

// Progress reports a sub-agent lifecycle event so the UI can show activity.
type Progress func(index int, total int, prompt string, done bool)

// RunAll executes tasks concurrently and returns results in input order.
//
// A failing sub-agent never fails the batch: its error is reported in its own
// Result so the parent can decide what to do.
func (r *Runner) RunAll(ctx context.Context, tasks []Task, progress Progress) []Result {
	if len(tasks) > MaxTasks {
		tasks = tasks[:MaxTasks]
	}

	results := make([]Result, len(tasks))
	sem := make(chan struct{}, r.maxParallel())
	var wg sync.WaitGroup

	for i, task := range tasks {
		wg.Add(1)
		go func(idx int, t Task) {
			defer wg.Done()

			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				results[idx] = Result{Prompt: t.Prompt, Err: ctx.Err()}
				return
			}

			if progress != nil {
				progress(idx, len(tasks), t.Prompt, false)
			}
			results[idx] = r.runOne(ctx, t)
			if progress != nil {
				progress(idx, len(tasks), t.Prompt, true)
			}
		}(i, task)
	}

	wg.Wait()
	return results
}

// runOne executes a single sub-agent in an isolated conversation.
func (r *Runner) runOne(ctx context.Context, task Task) Result {
	start := time.Now()
	res := Result{Prompt: task.Prompt}

	if strings.TrimSpace(task.Prompt) == "" {
		res.Err = fmt.Errorf("empty task")
		return res
	}
	// Check before building anything: there is no point constructing a client
	// and compactor for a task that has already been cancelled.
	if err := ctx.Err(); err != nil {
		res.Err = summariseCtxErr(err)
		return res
	}
	if r.Profile == nil {
		res.Err = fmt.Errorf("no model profile configured for sub-agents")
		return res
	}

	runCtx, cancel := context.WithTimeout(ctx, r.timeout())
	defer cancel()

	client := llm.New(r.Profile, r.Creds)
	toolDefs := tools.ReadOnlyDefinitions()

	compactCfg := compact.DefaultConfig(r.Profile.ContextLength)
	compactCfg.FixedOverhead = tokens.EstimateJSON(toolDefs)
	compactor := compact.New(compactCfg, client)

	messages := []memory.Message{
		{Role: "system", Content: systemPrompt, Timestamp: time.Now().UTC()},
		{Role: "user", Content: task.Prompt, Timestamp: time.Now().UTC()},
	}

	for turn := 0; turn < r.maxTurns(); turn++ {
		if err := runCtx.Err(); err != nil {
			res.Err = summariseCtxErr(err)
			res.Elapsed = time.Since(start)
			return res
		}

		if compacted, _, err := compactor.Compact(runCtx, messages); err == nil {
			messages = compacted
		}

		resp, err := client.Chat(runCtx, messages, toolDefs)
		if err != nil {
			res.Err = err
			res.Turns = turn
			res.Elapsed = time.Since(start)
			return res
		}
		res.Turns = turn + 1

		if len(resp.ToolCalls) == 0 {
			res.Output = clip(strings.TrimSpace(resp.Content))
			res.Elapsed = time.Since(start)
			if res.Output == "" {
				res.Err = fmt.Errorf("sub-agent returned no findings")
			}
			return res
		}

		assistant := memory.Message{Role: "assistant", Content: resp.Content, Timestamp: time.Now().UTC()}
		for _, tc := range resp.ToolCalls {
			assistant.ToolCalls = append(assistant.ToolCalls, memory.ToolCall{
				ID: tc.ID, Name: tc.Name, Arguments: string(tc.Arguments),
			})
		}
		messages = append(messages, assistant)

		for _, tc := range resp.ToolCalls {
			// No approval prompt: the toolkit is read-only, so there is nothing
			// to approve.
			out := r.Toolkit.Execute(tools.ToolCall{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments})
			content := out.Content
			if out.Error != "" {
				content = "error: " + out.Error
			}
			messages = append(messages, memory.Message{
				Role:       "tool",
				Content:    content,
				ToolCallID: tc.ID,
				Timestamp:  time.Now().UTC(),
			})
		}
	}

	res.Err = fmt.Errorf("reached the %d turn limit without concluding", r.maxTurns())
	res.Elapsed = time.Since(start)
	return res
}

func summariseCtxErr(err error) error {
	if err == context.DeadlineExceeded {
		return fmt.Errorf("timed out")
	}
	return err
}

func clip(s string) string {
	if len(s) <= maxOutputChars {
		return s
	}
	return s[:maxOutputChars] + "\n... (findings truncated)"
}

// Render formats results for the parent agent.
func Render(results []Result) string {
	var b strings.Builder
	for i, r := range results {
		fmt.Fprintf(&b, "--- sub-agent %d: %s ---\n", i+1, oneLine(r.Prompt, 200))
		switch {
		case r.Err != nil && r.Output == "":
			fmt.Fprintf(&b, "failed: %v\n", r.Err)
		case r.Err != nil:
			fmt.Fprintf(&b, "%s\n(incomplete: %v)\n", r.Output, r.Err)
		default:
			fmt.Fprintf(&b, "%s\n", r.Output)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
