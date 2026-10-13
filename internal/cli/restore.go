package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ahaltindis/wipsync/internal/app"
)

func restoreCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restore <snapshot> [folder]",
		Short: "Make a folder match one of its snapshots",
		Long: `Make a folder match one of its snapshots, whatever state it is in.

The snapshot is named by the start of its ID, as "wipsync log" shows it, or
is "master". The folder is the one the current directory is in, unless
another is given.

Restore first saves the folder's current state as a snapshot and prints its
ID, so a restore can itself be undone. Use it to undo a pull, or to give up
local changes and take master after a pull was refused.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := open(cmd)
			if err != nil {
				return err
			}
			f, err := folderArg(a, args, 1)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			res, err := a.Restore(cmd.Context(), f, args[0])
			if res.Saved != "" {
				fmt.Fprintf(out, "%s: saved its current state as snapshot %s\n", f, res.Saved[:8])
			}
			if err != nil {
				return err
			}
			if res.Already {
				fmt.Fprintf(out, "%s: already at %s\n", f, app.Describe(res.Target))
				return nil
			}
			if res.IgnoreChanged {
				fmt.Fprintf(out, "%s: its ignore rules are now %s\n", f, strings.Join(res.Ignore, ", "))
			}
			fmt.Fprintf(out, "restored %s to %s\n", f, app.Describe(res.Target))
			if res.Undo != "" {
				fmt.Fprintf(out, "to undo: wipsync restore %s\n", res.Undo[:8])
			}
			return nil
		},
	}
}
