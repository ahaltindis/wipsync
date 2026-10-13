package cli

import (
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ahaltindis/wipsync/internal/restic"
)

func logCmd() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "log [folder]",
		Short: "List a folder's snapshots from all clients, newest first",
		Long: `List a folder's snapshots from all clients, newest first.

The folder is the one the current directory is in, or the one given as a
directory or as "Root/path". "master" marks the snapshot the server has as
master right now, and "base" the one this client's folder is based on.`,
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
			h, err := a.Log(cmd.Context(), f)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			list := h.Snapshots
			if len(list) == 0 {
				fmt.Fprintf(out, "%s: no snapshots yet\n", f)
				return nil
			}
			masterAt := slices.IndexFunc(list, func(s restic.Snapshot) bool { return s.ID == h.Master })
			var masterSnap restic.Snapshot
			if masterAt >= 0 {
				masterSnap = list[masterAt]
			}
			hidden := 0
			if limit > 0 && len(list) > limit {
				hidden = len(list) - limit
				list = list[:limit]
			}
			fmt.Fprintln(out, f)
			width := 0
			for _, s := range list {
				width = max(width, len(s.Host))
			}
			for _, s := range list {
				line := fmt.Sprintf("%s  %s  %s", s.ID[:8], s.Time.Local().Format("2006-01-02 15:04"), s.Host)
				var marks []string
				if s.ID == h.Master {
					marks = append(marks, "master")
				}
				if s.ID == h.Base {
					marks = append(marks, "base")
				}
				if len(marks) > 0 {
					line += fmt.Sprintf("%*s  %s", width-len(s.Host), "", strings.Join(marks, ", "))
				}
				fmt.Fprintln(out, line)
			}
			if hidden > 0 {
				fmt.Fprintf(out, "%d older not shown, see --limit\n", hidden)
			}
			// Master is always accounted for, also when its snapshot is not
			// among the lines above.
			switch {
			case h.MasterErr != nil:
				fmt.Fprintf(out, "master is unknown: %v\n", h.MasterErr)
			case h.Master == "":
				fmt.Fprintln(out, "master is not set")
			case masterAt < 0:
				fmt.Fprintf(out, "master is %s, which is not among this folder's snapshots\n", h.Master[:8])
			case masterAt >= len(list):
				fmt.Fprintf(out, "master is %s  %s  %s  (older, not shown above)\n",
					masterSnap.ID[:8], masterSnap.Time.Local().Format("2006-01-02 15:04"), masterSnap.Host)
			}
			return nil
		},
	}
	cmd.Flags().IntVarP(&limit, "limit", "n", 20, "how many snapshots to show, 0 for all")
	return cmd
}
