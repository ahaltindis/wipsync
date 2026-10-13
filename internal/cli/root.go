// Package cli holds the wipsync commands.
package cli

import (
	"github.com/spf13/cobra"

	"github.com/ahaltindis/wipsync/internal/app"
	"github.com/ahaltindis/wipsync/internal/config"
)

// Version is what "wipsync --version" prints.
var Version = "dev"

func Root() *cobra.Command {
	root := &cobra.Command{
		Use:           "wipsync",
		Version:       Version,
		Short:         "Sync work-in-progress folders between machines",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().String("config", "", "config file (default ~/.config/wipsync/config.toml)")
	root.AddCommand(initCmd(), addCmd(), listCmd(), snapshotCmd(), pushCmd(), pullCmd(), restoreCmd(), logCmd(), doctorCmd())
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

// open loads the client the command runs as.
func open(cmd *cobra.Command) (*app.App, error) {
	path, err := cmd.Flags().GetString("config")
	if err != nil {
		return nil, err
	}
	return app.Open(path)
}

// folderArg finds the folder a command is about: the one named by its
// optional argument at position i, or the one the current directory is in.
func folderArg(a *app.App, args []string, i int) (config.Folder, error) {
	arg := ""
	if len(args) > i {
		arg = args[i]
	}
	return a.Folder(arg)
}
