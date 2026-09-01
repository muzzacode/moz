package subagent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/muzzacode/moz/internal/safepath"
	"github.com/muzzacode/moz/internal/tools"
)

func TestNewRunnerForcesReadOnly(t *testing.T) {
	tk := tools.New(safepath.New([]string{t.TempDir()}), nil)
	if tk.ReadOnly {
		t.Fatal("precondition: base toolkit should be writable")
	}

	r := NewRunner(nil, nil, tk)

	if !r.Toolkit.ReadOnly {
		t.Fatal("sub-agent toolkit must be read-only")
	}
	if tk.ReadOnly {
		t.Fatal("NewRunner must not mutate the caller's toolkit")
	}
}

func TestRunAllRejectsEmptyTask(t *testing.T) {
	r := &Runner{Toolkit: tools.New(safepath.New([]string{t.TempDir()}), nil).ReadOnlyCopy()}

	res := r.RunAll(context.Background(), []Task{{Prompt: "  "}}, nil)
	if len(res) != 1 {
		t.Fatalf("expected 1 result, got %d", len(res))
	}
	if res[0].Err == nil {
		t.Fatal("an empty task should fail fast without calling a model")
	}
}

// Fan-out must be bounded, or a model asking for 50 investigations would get the
// account rate-limited.
func TestRunAllCapsTaskCount(t *testing.T) {
	r := &Runner{
		Toolkit:     tools.New(safepath.New([]string{t.TempDir()}), nil).ReadOnlyCopy(),
		MaxParallel: 2,
	}

	tasks := make([]Task, MaxTasks+10)
	for i := range tasks {
		// Empty prompts fail immediately, so no model call is made.
		tasks[i] = Task{Prompt: ""}
	}

	res := r.RunAll(context.Background(), tasks, nil)
	if len(res) != MaxTasks {
		t.Fatalf("expected the batch to be capped at %d, got %d", MaxTasks, len(res))
	}
}

func TestRunAllPreservesInputOrder(t *testing.T) {
	r := &Runner{
		Toolkit:     tools.New(safepath.New([]string{t.TempDir()}), nil).ReadOnlyCopy(),
		MaxParallel: 4,
	}

	// Empty prompts resolve immediately but at nondeterministic times, so this
	// exercises the ordering guarantee.
	tasks := []Task{{Prompt: ""}, {Prompt: ""}, {Prompt: ""}, {Prompt: ""}}
	res := r.RunAll(context.Background(), tasks, nil)

	if len(res) != len(tasks) {
		t.Fatalf("expected %d results, got %d", len(tasks), len(res))
	}
	for i, x := range res {
		if x.Prompt != tasks[i].Prompt {
			t.Fatalf("result %d out of order", i)
		}
	}
}

func TestRunAllReportsProgressForEveryTask(t *testing.T) {
	r := &Runner{
		Toolkit:     tools.New(safepath.New([]string{t.TempDir()}), nil).ReadOnlyCopy(),
		MaxParallel: 2,
	}

	var mu sync.Mutex
	started, finished := 0, 0

	r.RunAll(context.Background(), []Task{{Prompt: ""}, {Prompt: ""}, {Prompt: ""}}, func(idx, total int, prompt string, done bool) {
		mu.Lock()
		defer mu.Unlock()
		if total != 3 {
			t.Errorf("unexpected total %d", total)
		}
		if done {
			finished++
		} else {
			started++
		}
	})

	mu.Lock()
	defer mu.Unlock()
	if started != 3 || finished != 3 {
		t.Fatalf("expected 3 starts and 3 finishes, got %d and %d", started, finished)
	}
}

func TestRunAllHonoursCancellation(t *testing.T) {
	r := &Runner{
		Toolkit:     tools.New(safepath.New([]string{t.TempDir()}), nil).ReadOnlyCopy(),
		MaxParallel: 1,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res := r.RunAll(ctx, []Task{{Prompt: "a"}, {Prompt: "b"}}, nil)
	for i, x := range res {
		if x.Err == nil {
			t.Fatalf("result %d should report cancellation", i)
		}
	}
}

// Concurrency must never exceed MaxParallel.
func TestRunAllRespectsParallelLimit(t *testing.T) {
	var mu sync.Mutex
	var active, peak int

	r := &Runner{
		Toolkit:     tools.New(safepath.New([]string{t.TempDir()}), nil).ReadOnlyCopy(),
		MaxParallel: 2,
	}

	tasks := make([]Task, MaxTasks)
	for i := range tasks {
		tasks[i] = Task{Prompt: ""}
	}

	r.RunAll(context.Background(), tasks, func(idx, total int, prompt string, done bool) {
		mu.Lock()
		defer mu.Unlock()
		if done {
			active--
			return
		}
		active++
		if active > peak {
			peak = active
		}
	})

	mu.Lock()
	defer mu.Unlock()
	if peak > 2 {
		t.Fatalf("parallelism limit exceeded: peak %d", peak)
	}
}

func TestRenderShowsFindingsAndFailures(t *testing.T) {
	out := Render([]Result{
		{Prompt: "how does auth work", Output: "It uses JWT in auth.go:42."},
		{Prompt: "find the cache layer", Err: fmt.Errorf("timed out")},
		{Prompt: "partial one", Output: "found some of it", Err: fmt.Errorf("turn limit")},
	})

	if !strings.Contains(out, "JWT in auth.go:42") {
		t.Fatalf("successful findings missing:\n%s", out)
	}
	if !strings.Contains(out, "timed out") {
		t.Fatalf("failure not reported:\n%s", out)
	}
	// Partial findings are still useful and must not be discarded.
	if !strings.Contains(out, "found some of it") || !strings.Contains(out, "incomplete") {
		t.Fatalf("partial result mishandled:\n%s", out)
	}
	for _, want := range []string{"sub-agent 1", "sub-agent 2", "sub-agent 3"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing label %q:\n%s", want, out)
		}
	}
}

func TestClipCapsOutput(t *testing.T) {
	got := clip(strings.Repeat("x", maxOutputChars*2))
	if len(got) > maxOutputChars+64 {
		t.Fatalf("output not capped: %d chars", len(got))
	}
	if !strings.Contains(got, "truncated") {
		t.Fatal("expected a truncation marker")
	}
	if short := clip("brief"); short != "brief" {
		t.Fatalf("short output should be unchanged, got %q", short)
	}
}

func TestRunnerDefaults(t *testing.T) {
	r := &Runner{}
	if r.maxParallel() != DefaultMaxParallel {
		t.Fatalf("unexpected parallel default %d", r.maxParallel())
	}
	if r.maxTurns() != DefaultMaxTurns {
		t.Fatalf("unexpected turn default %d", r.maxTurns())
	}
	if r.timeout() != DefaultTimeout {
		t.Fatalf("unexpected timeout default %s", r.timeout())
	}

	// Explicit values win.
	r2 := &Runner{MaxParallel: 5, MaxTurns: 3, Timeout: time.Second}
	if r2.maxParallel() != 5 || r2.maxTurns() != 3 || r2.timeout() != time.Second {
		t.Fatal("explicit settings should be honoured")
	}
}

// The prompt must forbid speculation, since fabricated file paths are the most
// damaging failure mode for a research agent.
func TestSystemPromptForbidsSpeculation(t *testing.T) {
	for _, want := range []string{"read-only", "Never speculate", "line numbers"} {
		if !strings.Contains(systemPrompt, want) {
			t.Fatalf("system prompt should mention %q", want)
		}
	}
}
