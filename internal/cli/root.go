// Package cli holds the wipsync commands.
package cli

import (
	"github.com/spf13/cobra"

	"github.com/ahaltindis/wipsync/internal/config"
)

func Root() *cobra.Command {
	root := &cobra.Command{
		Use:           "wipsync",
		Short:         "Sync work-in-progress folders between machines",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().String("config", "", "config file (default ~/.config/wipsync/config.toml)")
	root.AddCommand(initCmd(), addCmd(), listCmd(), snapshotCmd(), pushCmd(), pullCmd(), logCmd(), doctorCmd())
	return root
}

func loadConfig(cmd *cobra.Command) (string, *config.Config, error) {
	path, err := cmd.Flags().GetString("config")
	if err != nil {
		return "", nil, err
	}
	if path == "" {
		if path, err = config.DefaultPath(); err != nil {
			return "", nil, err
		}
	}
	cfg, err := config.Load(path)
	if err != nil {
		return "", nil, err
	}
	return cfg.Path, cfg, nil
}
