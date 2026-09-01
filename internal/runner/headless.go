package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/muzzacode/moz/internal/agent"
	"github.com/muzzacode/moz/internal/approval"
	"github.com/muzzacode/moz/internal/checkpoint"
	"github.com/muzzacode/moz/internal/config"
	"github.com/muzzacode/moz/internal/llm"
	"github.com/muzzacode/moz/internal/memory"
	"github.com/muzzacode/moz/internal/models"
	"github.com/muzzacode/moz/internal/safepath"
	"github.com/muzzacode/moz/internal/todo"
	"github.com/muzzacode/moz/internal/tools"
)

// riskyExec reports whether a tool call is a shell command that reaches outside
// the project.
func riskyExec(tc *llm.ToolCall) (approval.Risk, bool) {
	if tools.ResolveName(tc.Name) != "exec" {
		return approval.Risk{}, false
	}
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(tc.Arguments, &args); err != nil {
		return approval.Risk{}, false
	}
	return approval.ClassifyCommand(args.Command)
}

// RunTask executes a single agent task without the TUI. If autoApprove is true,
// all tool calls are approved automatically. It prints progress to stderr and the
// final answer to stdout.
func RunTask(ctx context.Context, cfg *config.Config, reg *models.Registry, store *memory.Store, task string, files []string, autoApprove bool) error {
	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()
	allowed := []string{cwd, home}
	if cfg.Workspace != "" {
		allowed = append(allowed, cfg.Workspace)
	}

	safe := safepath.New(allowed)
	todoStore := todo.NewStore(cfg)
	todos, _ := todoStore.Load()
	tk := tools.New(safe, todos)

	// Snapshots are recorded even in headless mode so a failed run can be
	// inspected and reversed from the reported file list.
	checkpoints := checkpoint.New()
	checkpoints.Begin(task)
	tk.Checkpoints = checkpoints

	if len(files) > 0 {
		var ctxB strings.Builder
		for _, p := range files {
			resolved, err := safe.Resolve(p)
			if err != nil {
				return fmt.Errorf("file not allowed: %s", p)
			}
			data, err := os.ReadFile(resolved)
			if err != nil {
				return fmt.Errorf("failed to read %s: %w", p, err)
			}
			ctxB.WriteString("\n--- ")
			ctxB.WriteString(p)
			ctxB.WriteString(" ---\n")
			ctxB.Write(data)
		}
		ctxB.WriteString("\n--- task ---\n")
		ctxB.WriteString(task)
		task = ctxB.String()
	}

	runner := agent.New(cfg, reg, tk)

	mode := cfg.Mode
	if mode == "" {
		mode = "adaptive"
	}

	var profile *models.Profile
	if mode == "adaptive" {
		decision, err := runner.Router.Select(task)
		if err != nil {
			return fmt.Errorf("no model available: %w", err)
		}
		profile = decision.Profile
		// Show why this model was chosen. In a cost-tiered setup the reasoning
		// matters as much as the choice.
		fmt.Fprintf(os.Stderr, "[routing] %s\n", decision.Reason)
		if decision.Downgraded {
			fmt.Fprintln(os.Stderr, "[routing] using a cheaper model than the task warranted")
		}
	} else {
		p, err := reg.Find(cfg.DefaultModel)
		if err != nil {
			return fmt.Errorf("model not found: %w", err)
		}
		profile = p
	}

	fmt.Fprintf(os.Stderr, "[model] %s (%s)\n", profile.Name, profile.ID)

	session := memory.NewSession()
	out := make(chan agent.Event)
	approvalCh := make(chan bool)

	go runner.Run(ctx, profile, task, session, out, approvalCh)

	scanner := bufio.NewScanner(os.Stdin)

	for ev := range out {
		switch ev.Type {
		case "step":
			if ev.Step != "" {
				fmt.Fprintln(os.Stderr, "["+ev.Step+"]")
			}
		case "tool_call":
			if ev.ToolCall != nil {
				fmt.Fprintf(os.Stderr, "[tool] %s(%s)\n", ev.ToolCall.Name, string(ev.ToolCall.Arguments))

				// --yes covers project work, not changes to the machine. A
				// command that reaches outside the workspace is always refused
				// in headless mode, because there is nobody to ask.
				if risk, flagged := riskyExec(ev.ToolCall); flagged {
					fmt.Fprintf(os.Stderr, "[refused] reaches outside this project: %s (%s)\n", risk.Reason, risk.Detail)
					fmt.Fprintln(os.Stderr, "[refused] rerun without --yes to approve it interactively")
					approvalCh <- false
					continue
				}

				if autoApprove {
					approvalCh <- true
				} else {
					fmt.Fprint(os.Stderr, "[approve y/n] ")
					if !scanner.Scan() {
						approvalCh <- false
						return fmt.Errorf("approval input closed")
					}
					approvalCh <- strings.HasPrefix(strings.ToLower(scanner.Text()), "y")
				}
			}
		case "tool_result":
			if ev.ToolResult != nil {
				if ev.ToolResult.Error != "" {
					fmt.Fprintf(os.Stderr, "[result] error: %s\n", ev.ToolResult.Error)
				} else {
					fmt.Fprintf(os.Stderr, "[result] %s\n", ev.ToolResult.Content)
				}
			}
		case "message":
			fmt.Print(ev.Content)
		case "message_end":
			fmt.Println()
		case "warning", "compacted", "verified":
			fmt.Fprintf(os.Stderr, "[%s] %s\n", ev.Type, ev.Step)
		case "cancelled":
			fmt.Fprintln(os.Stderr, "[cancelled]")
		case "error":
			fmt.Fprintf(os.Stderr, "[error] %s\n", ev.Error)
		case "usage":
			fmt.Fprintf(os.Stderr, "[tokens] prompt: %d, completion: %d, total: %d\n", ev.Usage.PromptTokens, ev.Usage.CompletionTokens, ev.Usage.TotalTokens)
		}
	}

	fmt.Println()
	return nil
}
