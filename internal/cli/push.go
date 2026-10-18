package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ahaltindis/wipsync/internal/app"
)

func pushCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "push [folder]",
		Short: "Mark a folder's latest snapshot as master",
		Long: `Mark a folder's latest snapshot as master, its known good state.

Push takes no snapshot and uploads nothing: it moves master to the snapshot
this client last took of the folder. Changes made since that snapshot are
not part of it; run "wipsync snapshot" first to include them.

The server accepts the push only if master is still where this client last
saw it. If another client pushed in between, pull first.

With --all, push does this for every folder on this client that has a
snapshot master does not. A folder that changed since its latest snapshot
fails, and so does one whose master another client moved; the others are
pushed all the same. When wipsync watch runs, it does the work, so that its
tray icon shows it and the desktop is told how it went.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if all && len(args) > 0 {
				return fmt.Errorf("--all pushes every folder; leave out %s", args[0])
			}
			a, err := open(cmd)
			if err != nil {
				return err
			}
			if all {
				res, err := service{a}.PushAll(cmd.Context(), app.Scope{})
				return printAll(cmd, res, err)
			}
			f, err := folderArg(a, args, 0)
			if err != nil {
				return err
			}
			res, err := a.Push(cmd.Context(), f)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), res.Line(f))
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "push every folder on this client that has something to push")
	return cmd
}

// printAll prints how a pull or push of many folders went, a line per
// folder and then the summary. It fails if any folder did.
func printAll(cmd *cobra.Command, res app.AllResult, err error) error {
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	for _, l := range res.Lines {
		fmt.Fprintln(out, l)
	}
	fmt.Fprintln(out, res.Summary())
	return res.Err()
}
