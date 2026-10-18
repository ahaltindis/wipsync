// Package cli holds the wipsync commands.
package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ahaltindis/wipsync/internal/app"
	"github.com/ahaltindis/wipsync/internal/db"
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
	root.AddCommand(initCmd(), addCmd(), listCmd(), snapshotCmd(), pushCmd(), pullCmd(), restoreCmd(), logCmd(), tuiCmd(), watchCmd(), doctorCmd())
	return root
}

// open loads the client the command runs as.
func open(cmd *cobra.Command) (*app.App, error) {
	path, err := cmd.Flags().GetString("config")
	if err != nil {
		return nil, err
	}
	a, err := app.Open(path)
	if err != nil {
		return nil, err
	}
	for _, note := range a.Notes {
		fmt.Fprintln(cmd.ErrOrStderr(), "wipsync:", note)
	}
	return a, nil
}

// folderArg finds the folder a command is about: the one named by its
// optional argument at position i, or the one the current directory is in.
func folderArg(a *app.App, args []string, i int) (db.Folder, error) {
	arg := ""
	if len(args) > i {
		arg = args[i]
	}
	return a.Folder(arg)
}
