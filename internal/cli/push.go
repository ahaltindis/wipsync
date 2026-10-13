package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ahaltindis/wipsync/internal/app"
)

func pushCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "push [folder]",
		Short: "Mark a folder's latest snapshot as master",
		Long: `Mark a folder's latest snapshot as master, its known good state.

Push takes no snapshot and uploads nothing: it moves master to the snapshot
this client last took of the folder. Changes made since that snapshot are
not part of it; run "wipsync snapshot" first to include them.

The server accepts the push only if master is still where this client last
saw it. If another client pushed in between, pull first.`,
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
			res, err := a.Push(cmd.Context(), f)
			if err != nil {
				return err
			}
			state := "now"
			if res.Already {
				state = "already"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: master is %s %s\n", f, state, app.Describe(res.Snapshot))
			return nil
		},
	}
}
