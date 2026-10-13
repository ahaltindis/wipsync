package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ahaltindis/wipsync/internal/restic"
	"github.com/ahaltindis/wipsync/internal/state"
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

			res, err := client.Backup(ctx, restic.BackupOptions{
				Dir:      cfg.Dir(f),
				Host:     cfg.Client,
				Tags:     folderTags(f),
				Excludes: f.Ignore,
				Parent:   base,
			})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if res.SnapshotID == "" {
				fmt.Fprintf(out, "%s: no changes\n", f)
				return nil
			}
			if err := store.SetBase(f.Root, f.Path, res.SnapshotID); err != nil {
				return fmt.Errorf("snapshot %s was saved, but not recorded as the base: %w", res.SnapshotID[:8], err)
			}
			fmt.Fprintf(out, "%s: snapshot %s, %d new, %d changed, %s stored\n",
				f, res.SnapshotID[:8], res.FilesNew, res.FilesChanged, formatBytes(res.BytesStored))
			return nil
		},
	}
}
