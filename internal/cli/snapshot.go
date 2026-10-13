package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ahaltindis/wipsync/internal/config"
	"github.com/ahaltindis/wipsync/internal/fingerprint"
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
			store, err := state.Default()
			if err != nil {
				return err
			}
			res, err := takeSnapshot(cmd.Context(), cfgPath, cfg, store, f)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if res.SnapshotID == "" {
				fmt.Fprintf(out, "%s: no changes\n", f)
				return nil
			}
			fmt.Fprintf(out, "%s: snapshot %s, %d new, %d changed, %s stored\n",
				f, res.SnapshotID[:8], res.FilesNew, res.FilesChanged, formatBytes(res.BytesStored))
			return nil
		},
	}
}

// takeSnapshot saves the folder's current state, unless nothing changed
// since its last snapshot; the result then has no snapshot ID. It keeps the
// folder's base and fingerprint up to date.
func takeSnapshot(ctx context.Context, cfgPath string, cfg *config.Config, store *state.Store, f config.Folder) (restic.BackupResult, error) {
	base, err := store.Base(f.Root, f.Path)
	if err != nil {
		return restic.BackupResult{}, err
	}

	// The fingerprint answers "did anything change?" without restic, which
	// would save a snapshot for as little as a directory's new timestamp.
	// If it cannot be taken, restic decides.
	print, printErr := fingerprint.Of(ctx, cfg.Dir(f), f.Ignore)
	if printErr == nil && base != "" {
		last, err := store.Fingerprint(f.Root, f.Path)
		if err != nil {
			return restic.BackupResult{}, err
		}
		if print == last {
			return restic.BackupResult{}, nil
		}
	}

	client, err := resticClient(cfgPath, cfg)
	if err != nil {
		return restic.BackupResult{}, err
	}
	if err := restic.CheckVersion(ctx); err != nil {
		return restic.BackupResult{}, err
	}
	afterPull, err := store.AfterPull(f.Root, f.Path)
	if err != nil {
		return restic.BackupResult{}, err
	}
	res, err := client.Backup(ctx, restic.BackupOptions{
		Dir:       cfg.Dir(f),
		Host:      cfg.Client,
		Tags:      folderTags(f),
		Excludes:  f.Ignore,
		Parent:    base,
		AfterPull: afterPull,
	})
	if err != nil {
		return restic.BackupResult{}, err
	}
	if res.SnapshotID != "" {
		if err := store.SetBase(f.Root, f.Path, res.SnapshotID); err != nil {
			return res, fmt.Errorf("snapshot %s was saved, but not recorded as the base: %w", res.SnapshotID[:8], err)
		}
		if err := store.SetAfterPull(f.Root, f.Path, false); err != nil {
			return res, err
		}
	}
	// The fingerprint from before the backup: a file that changed while
	// restic ran makes the next look differ, and is picked up then.
	if printErr == nil {
		if err := store.SetFingerprint(f.Root, f.Path, print); err != nil {
			return res, err
		}
	}
	return res, nil
}
