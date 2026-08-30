package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/muzzacode/moz/internal/config"
	"github.com/muzzacode/moz/internal/memory"
	"github.com/muzzacode/moz/internal/models"
	"github.com/muzzacode/moz/internal/runner"
	"github.com/muzzacode/moz/internal/tui"
	"github.com/muzzacode/moz/internal/version"
)

var (
	modelID     string
	mode        string
	task        string
	autoApprove bool
	files       []string
)

func main() {
	rootCmd := &cobra.Command{
		Use:     "moz",
		Short:   "Moz — a personal, model-agnostic, agentic terminal",
		Version: version.Version,
		RunE:    run,
	}

	rootCmd.Flags().StringVar(&modelID, "model", "", "model profile to use (overrides adaptive)")
	rootCmd.Flags().StringVar(&mode, "mode", "", "mode: adaptive, manual, or a profile id")
	rootCmd.Flags().StringVar(&task, "task", "", "run a single task in headless mode and exit")
	rootCmd.Flags().BoolVar(&autoApprove, "yes", false, "auto-approve all tool calls in headless mode")
	rootCmd.Flags().StringSliceVar(&files, "files", nil, "file paths to include as context for --task")

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
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
		if cfg.Agent {
			cfg.Agent = false
		}
		return runner.RunTask(context.Background(), cfg, registry, store, task, files, autoApprove)
	}

	return tui.Run(cfg, registry, store)
}
