package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ahaltindis/wipsync/internal/app"
)

func snapshotCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "snapshot [folder]",
		Short: "Save a folder's current state to the server as WIP",
		Long: `Save a folder's current state to the server as WIP.

The folder is the one the current directory is in, or the one given as a
directory or as "Root/path". Nothing is saved when nothing changed.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := open(cmd)
			if err != nil {
				return err
			}
			f, err := folderArg(a, args, 0)
			if err != nil {
				return err
			}
			res, err := a.Snapshot(cmd.Context(), f)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if res.SnapshotID == "" {
				fmt.Fprintf(out, "%s: no changes\n", f)
				return nil
			}
			fmt.Fprintf(out, "%s: snapshot %s, %d new, %d changed, %s stored\n",
				f, res.SnapshotID[:8], res.FilesNew, res.FilesChanged, app.FormatBytes(res.BytesStored))
			return nil
		},
	}
}
