package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/ahaltindis/wipsync/internal/config"
	"github.com/ahaltindis/wipsync/internal/restic"
)

type RestoreResult struct {
	Target  restic.Snapshot
	Already bool // the folder was at the target and had not changed

	// Saved is the snapshot taken of the folder's state first, if it had
	// changed. Undo is the snapshot that holds the state from before.
	Saved string
	Undo  string

	// Ignore holds the folder's ignore rules if the restore changed them.
	Ignore        []string
	IgnoreChanged bool
}

// Restore makes the folder match one of its snapshots, whatever state it
// is in, after saving that state as a snapshot. The snapshot is named by
// the start of its ID, or is "master".
func (a *App) Restore(ctx context.Context, f config.Folder, name string) (RestoreResult, error) {
	var res RestoreResult
	client, pointers, err := a.remote(ctx)
	if err != nil {
		return res, err
	}
	// Looked up before anything is saved, so a mistyped ID costs nothing.
	list, err := client.Snapshots(ctx, Tags(f))
	if err != nil {
		return res, err
	}
	masterID, err := pointers.Master(ctx, f.Root, f.Path)
	if err != nil {
		return res, err
	}
	if res.Target, err = findSnapshot(list, f, name, masterID); err != nil {
		return res, err
	}

	saved, err := a.Snapshot(ctx, f)
	if err != nil {
		return res, err
	}
	res.Saved = saved.SnapshotID
	if res.Undo, err = a.State.Base(f.Root, f.Path); err != nil {
		return res, err
	}
	if res.Undo == res.Target.ID {
		res.Already = true
		return res, nil
	}
	res.Ignore, res.IgnoreChanged, err = a.apply(ctx, f, res.Target, res.Target.ID == masterID, res.Undo)
	return res, err
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
