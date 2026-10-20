package app

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/ahaltindis/wipsync/internal/db"
	"github.com/ahaltindis/wipsync/internal/restic"
)

// CheckDelete says why a snapshot of a folder cannot be deleted, or nil if
// it can. Master and what this client is based on are kept. So is a
// snapshot another one is based on: the parent links would break, and a
// client based on that one could take no more snapshots.
func CheckDelete(snapshots []restic.Snapshot, id, base, master string) error {
	switch {
	case id == master:
		return errors.New("it is master")
	case id == base:
		return errors.New("this client's folder is based on it")
	case slices.ContainsFunc(snapshots, func(s restic.Snapshot) bool { return s.Parent == id }):
		return errors.New("another snapshot is based on it")
	}
	return nil
}

// Delete removes one of the folder's snapshots, given by its full ID, from
// the repository, if CheckDelete allows it. Its data stays until a prune.
func (a *App) Delete(ctx context.Context, f db.Folder, id string) error {
	unlock, err := a.lock(f)
	if err != nil {
		return err
	}
	defer unlock()
	client, pointers, err := a.remote(ctx)
	if err != nil {
		return err
	}
	list, err := client.Snapshots(ctx, Tags(f))
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(list, func(s restic.Snapshot) bool { return s.ID == id }) {
		return fmt.Errorf("%s has no snapshot %s", f, id[:min(8, len(id))])
	}
	masterID, err := pointers.Master(ctx, f.Root, f.Path)
	if err != nil {
		return err
	}
	base, err := a.DB.Base(f.Root, f.Path)
	if err != nil {
		return err
	}
	if err := CheckDelete(list, id, base, masterID); err != nil {
		return fmt.Errorf("%s: snapshot %s is kept: %w", f, id[:min(8, len(id))], err)
	}
	return client.Forget(ctx, id)
}
