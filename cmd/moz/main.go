package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/muzzacode/moz/internal/config"
	"github.com/muzzacode/moz/internal/memory"
	"github.com/muzzacode/moz/internal/models"
	"github.com/muzzacode/moz/internal/runner"
	"github.com/muzzacode/moz/internal/tui"
	"github.com/muzzacode/moz/internal/version"
	"github.com/spf13/cobra"
)

var (
	modelID     string
	mode        string
	task        string
	autoApprove bool
	files       []string
	resumeID    string
)

func main() {
	rootCmd := &cobra.Command{
		Use:               "moz",
		Short:             "Moz — a personal, model-agnostic, agentic terminal",
		Version:           version.Version,
		RunE:              run,
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	}

	rootCmd.Flags().StringVar(&modelID, "model", "", "model profile to use (overrides adaptive)")
	rootCmd.Flags().StringVar(&mode, "mode", "", "mode: adaptive, manual, or a profile id")
	rootCmd.Flags().StringVar(&task, "task", "", "run a single task in headless mode and exit")
	rootCmd.Flags().BoolVar(&autoApprove, "yes", false, "auto-approve all tool calls in headless mode")
	rootCmd.Flags().StringSliceVar(&files, "files", nil, "file paths to include as context for --task")
	rootCmd.Flags().StringVar(&resumeID, "resume", "", "resume a saved TUI session by ID or latest")

	_ = rootCmd.RegisterFlagCompletionFunc("model", completeProfiles)
	_ = rootCmd.RegisterFlagCompletionFunc("mode", completeModes)
	_ = rootCmd.RegisterFlagCompletionFunc("resume", completeSessions)
	_ = rootCmd.MarkFlagFilename("files")

	rootCmd.AddCommand(completionCmd())

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func completionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Generate shell completion scripts",
		Long: `Generate completion scripts for Moz.

Bash:
  source <(moz completion bash)

Zsh:
  moz completion zsh > "${fpath[1]}/_moz"

Fish:
  moz completion fish | source
`,
		DisableFlagsInUseLine: true,
		ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
		Args:                  cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return cmd.Root().GenBashCompletion(os.Stdout)
			case "zsh":
				return cmd.Root().GenZshCompletion(os.Stdout)
			case "fish":
				return cmd.Root().GenFishCompletion(os.Stdout, true)
			case "powershell":
				return cmd.Root().GenPowerShellCompletionWithDesc(os.Stdout)
			default:
				return fmt.Errorf("unsupported shell: %s", args[0])
			}
		},
	}
	return cmd
}

func completeProfiles(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	reg := loadRegistryForCompletion()
	ids := make([]string, 0, len(reg.Profiles))
	for _, p := range reg.Profiles {
		ids = append(ids, p.ID)
	}
	return ids, cobra.ShellCompDirectiveNoFileComp
}

func completeModes(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	out := []string{"adaptive", "manual"}
	reg := loadRegistryForCompletion()
	for _, p := range reg.Profiles {
		out = append(out, p.ID)
	}
	_ = toComplete
	return out, cobra.ShellCompDirectiveNoFileComp
}

func completeSessions(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	cfgPath := filepath.Join(config.Dir(), "config.yaml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return []string{"latest"}, cobra.ShellCompDirectiveNoFileComp
	}
	store := memory.New(cfg)
	ids, err := store.ListSessions()
	if err != nil {
		return []string{"latest"}, cobra.ShellCompDirectiveNoFileComp
	}
	return append([]string{"latest"}, ids...), cobra.ShellCompDirectiveNoFileComp
}

func loadRegistryForCompletion() *models.Registry {
	path := filepath.Join(config.Dir(), "models.yaml")
	reg, err := models.Load(path)
	if err != nil {
		return models.DefaultProfiles()
	}
	return reg
}

func run(cmd *cobra.Command, args []string) error {
	cfgDir := config.Dir()
	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		return fmt.Errorf("failed to create config dir: %w", err)
	}

	cfgPath := filepath.Join(cfgDir, "config.yaml")
	modelsPath := filepath.Join(cfgDir, "models.yaml")

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	if err := cfg.Save(cfgPath); err != nil {
		return fmt.Errorf("failed to save default config: %w", err)
	}

	registry, err := models.Load(modelsPath)
	if err != nil {
		return fmt.Errorf("failed to load models: %w", err)
	}
	if err := registry.Save(modelsPath); err != nil {
		return fmt.Errorf("failed to save default models: %w", err)
	}

	if modelID != "" {
		cfg.DefaultModel = modelID
		cfg.Mode = "manual"
	} else if mode != "" {
		if mode == "adaptive" || mode == "manual" {
			cfg.Mode = mode
		} else {
			cfg.DefaultModel = mode
			cfg.Mode = "manual"
		}
	}

	store := memory.New(cfg)
	if err := store.Ensure(); err != nil {
		return fmt.Errorf("failed to ensure memory dir: %w", err)
	}

	if task != "" {
		if resumeID != "" {
			return fmt.Errorf("--resume cannot be used with --task")
		}
		if cfg.Agent {
			cfg.Agent = false
		}
		return runner.RunTask(context.Background(), cfg, registry, store, task, files, autoApprove)
	}

	var initial *memory.Session
	if resumeID == "latest" {
		initial, err = store.LatestSession()
	} else if resumeID != "" {
		initial, err = store.LoadSession(resumeID)
	}
	if err != nil {
		return fmt.Errorf("failed to resume session: %w", err)
	}
	return tui.Run(cfg, registry, store, initial)
}
