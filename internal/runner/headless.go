package runner

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/muzzacode/moz/internal/agent"
	"github.com/muzzacode/moz/internal/config"
	"github.com/muzzacode/moz/internal/memory"
	"github.com/muzzacode/moz/internal/models"
	"github.com/muzzacode/moz/internal/safepath"
	"github.com/muzzacode/moz/internal/todo"
	"github.com/muzzacode/moz/internal/tools"
)

// RunTask executes a single agent task without the TUI. If autoApprove is true,
// all tool calls are approved automatically. It prints progress to stderr and the
// final answer to stdout.
func RunTask(ctx context.Context, cfg *config.Config, reg *models.Registry, store *memory.Store, task string, autoApprove bool) error {
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

	var final strings.Builder
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
			final.WriteString(ev.Content)
		case "error":
			fmt.Fprintf(os.Stderr, "[error] %s\n", ev.Error)
		case "usage":
			fmt.Fprintf(os.Stderr, "[tokens] prompt: %d, completion: %d, total: %d\n", ev.Usage.PromptTokens, ev.Usage.CompletionTokens, ev.Usage.TotalTokens)
		}
	}

	if final.Len() > 0 {
		fmt.Println(final.String())
	}
	return nil
}
