package cli

import (
	"fmt"
	"slices"

	"github.com/spf13/cobra"

	"github.com/ahaltindis/wipsync/internal/restic"
	"github.com/ahaltindis/wipsync/internal/state"
)

func logCmd() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "log [folder]",
		Short: "List a folder's snapshots from all clients, newest first",
		Long: `List a folder's snapshots from all clients, newest first.

The folder is the one the current directory is in, or the one given as a
directory or as "Root/path". The snapshot this client's folder is based on
is marked "base".`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath, cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			arg := ""
			if len(args) == 1 {
				arg = args[0]
			}
			f, err := resolveFolder(cfg, arg)
			if err != nil {
				return err
			}
			client, err := resticClient(cfgPath, cfg)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			if err := restic.CheckVersion(ctx); err != nil {
				return err
			}
			store, err := state.Default()
			if err != nil {
				return err
			}
			base, err := store.Base(f.Root, f.Path)
			if err != nil {
				return err
			}
			list, err := client.Snapshots(ctx, folderTags(f))
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if len(list) == 0 {
				fmt.Fprintf(out, "%s: no snapshots yet\n", f)
				return nil
			}
			slices.Reverse(list)
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
				if s.ID == base {
					line += fmt.Sprintf("%*s  base", width-len(s.Host), "")
				}
				fmt.Fprintln(out, line)
			}
			if hidden > 0 {
				fmt.Fprintf(out, "%d older not shown, see --limit\n", hidden)
			}
			return nil
		},
	}
	cmd.Flags().IntVarP(&limit, "limit", "n", 20, "how many snapshots to show, 0 for all")
	return cmd
}
