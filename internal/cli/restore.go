package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ahaltindis/wipsync/internal/config"
	"github.com/ahaltindis/wipsync/internal/restic"
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
			cfgPath, cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			arg := ""
			if len(args) == 2 {
				arg = args[1]
			}
			f, err := resolveFolder(cfg, arg)
			if err != nil {
				return err
			}
			p, err := newPuller(cmd, cfgPath, cfg)
			if err != nil {
				return err
			}

			// Looked up before anything is saved, so a mistyped ID costs nothing.
			list, err := p.client.Snapshots(p.ctx, folderTags(f))
			if err != nil {
				return err
			}
			masterID, err := p.pointers.Master(p.ctx, f.Root, f.Path)
			if err != nil {
				return err
			}
			target, err := findSnapshot(list, f, args[0], masterID)
			if err != nil {
				return err
			}

			saved, err := takeSnapshot(p.ctx, cfgPath, cfg, p.store, f)
			if err != nil {
				return err
			}
			if saved.SnapshotID != "" {
				fmt.Fprintf(p.out, "%s: saved its current state as snapshot %s\n", f, saved.SnapshotID[:8])
			}
			before, err := p.store.Base(f.Root, f.Path)
			if err != nil {
				return err
			}
			if before == target.ID {
				fmt.Fprintf(p.out, "%s: already at %s\n", f, describe(target))
				return nil
			}
			if err := p.apply(&f, target, target.ID == masterID, before); err != nil {
				return err
			}
			fmt.Fprintf(p.out, "restored %s to %s\n", f, describe(target))
			if before != "" {
				fmt.Fprintf(p.out, "to undo: wipsync restore %s\n", before[:8])
			}
			return nil
		},
	}
}

// findSnapshot picks one of a folder's snapshots: master, or the one whose
// ID starts with name.
func findSnapshot(list []restic.Snapshot, f config.Folder, name, masterID string) (restic.Snapshot, error) {
	if name == "master" {
		if masterID == "" {
			return restic.Snapshot{}, fmt.Errorf("%s has no master yet", f)
		}
		name = masterID
	}
	var found []restic.Snapshot
	for _, s := range list {
		if strings.HasPrefix(s.ID, name) {
			found = append(found, s)
		}
	}
	switch {
	case len(found) == 1:
		return found[0], nil
	case len(found) > 1:
		return restic.Snapshot{}, fmt.Errorf("%d snapshots of %s start with %q; give more of the ID", len(found), f, name)
	case name == masterID:
		return restic.Snapshot{}, fmt.Errorf("master of %s is %s, which is not among the folder's snapshots", f, masterID[:8])
	}
	return restic.Snapshot{}, fmt.Errorf("no snapshot of %s starts with %q; see wipsync log", f, name)
}
