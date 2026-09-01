package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/muzzacode/moz/internal/models"
	"github.com/muzzacode/moz/internal/subagent"
	"github.com/muzzacode/moz/internal/tools"
)

// spawnArgs is the payload for the spawn_agents tool.
type spawnArgs struct {
	Tasks []string `json:"tasks"`
	// Some models emit a single string instead of an array; accepted so a
	// well-intentioned call is not wasted.
	Task string `json:"task"`
}

func (a spawnArgs) tasks() []subagent.Task {
	raw := a.Tasks
	if len(raw) == 0 && a.Task != "" {
		raw = []string{a.Task}
	}
	out := make([]subagent.Task, 0, len(raw))
	for _, p := range raw {
		if p != "" {
			out = append(out, subagent.Task{Prompt: p})
		}
	}
	return out
}

// runSpawnAgents handles the spawn_agents tool.
//
// It lives in the agent rather than the toolkit because it needs a model
// client, and tools must not depend on the llm package.
func (r *Runner) runSpawnAgents(
	ctx context.Context,
	profile *models.Profile,
	call tools.ToolCall,
	out chan<- Event,
	start time.Time,
) tools.ToolResult {
	tr := tools.ToolResult{ID: call.ID, Name: call.Name}

	var args spawnArgs
	if err := json.Unmarshal(call.Arguments, &args); err != nil {
		tr.Error = fmt.Sprintf("invalid arguments for spawn_agents: %v", err)
		return tr
	}

	tasks := args.tasks()
	if len(tasks) == 0 {
		tr.Error = "spawn_agents requires at least one non-empty task"
		return tr
	}
	if len(tasks) > subagent.MaxTasks {
		tasks = tasks[:subagent.MaxTasks]
		out <- Event{
			Type:    "warning",
			Step:    fmt.Sprintf("limiting to %d sub-agents", subagent.MaxTasks),
			Elapsed: time.Since(start),
		}
	}

	runner := subagent.NewRunner(profile, r.Creds, r.Toolkit)

	out <- Event{
		Type:    "step",
		Step:    fmt.Sprintf("running %d sub-agents (read-only, up to %d at a time)", len(tasks), runner.MaxParallel),
		Model:   profile.Name,
		Elapsed: time.Since(start),
	}

	// Progress events are emitted from sub-agent goroutines, so they are
	// funnelled through a collector rather than written to out directly. The
	// event channel is consumed by a single reader.
	type note struct {
		idx  int
		done bool
	}
	notes := make(chan note, len(tasks)*2)

	results := make(chan []subagent.Result, 1)
	go func() {
		defer close(notes)
		results <- runner.RunAll(ctx, tasks, func(idx, total int, prompt string, done bool) {
			notes <- note{idx: idx, done: done}
		})
	}()

	completed := 0
	for n := range notes {
		if !n.done {
			continue
		}
		completed++
		out <- Event{
			Type:    "step",
			Step:    fmt.Sprintf("sub-agent %d/%d finished", completed, len(tasks)),
			Model:   profile.Name,
			Elapsed: time.Since(start),
		}
	}

	res := <-results

	failed := 0
	for _, x := range res {
		if x.Err != nil && x.Output == "" {
			failed++
		}
	}
	if failed > 0 {
		out <- Event{
			Type:    "warning",
			Step:    fmt.Sprintf("%d of %d sub-agents failed", failed, len(res)),
			Elapsed: time.Since(start),
		}
	}

	tr.Content = subagent.Render(res)
	// Only a total failure is an error; partial findings are still useful.
	if failed == len(res) {
		tr.Error = "all sub-agents failed"
	}
	return tr
}
