package app

import (
	"context"
	"fmt"

	"github.com/ahaltindis/wipsync/internal/config"
	"github.com/ahaltindis/wipsync/internal/fingerprint"
	"github.com/ahaltindis/wipsync/internal/restic"
)

// Snapshot saves the folder's current state, unless nothing changed since
// its last snapshot; the result then has no snapshot ID. It keeps the
// folder's base and fingerprint up to date.
func (a *App) Snapshot(ctx context.Context, f config.Folder) (restic.BackupResult, error) {
	base, err := a.State.Base(f.Root, f.Path)
	if err != nil {
		return restic.BackupResult{}, err
	}

	// The fingerprint answers "did anything change?" without restic, which
	// would save a snapshot for as little as a directory's new timestamp.
	// If it cannot be taken, restic decides.
	print, printErr := fingerprint.Of(ctx, a.Config.Dir(f), f.Ignore)
	if printErr == nil && base != "" {
		last, err := a.State.Fingerprint(f.Root, f.Path)
		if err != nil {
			return restic.BackupResult{}, err
		}
		if print == last {
			return restic.BackupResult{}, nil
		}
	}

	client, err := a.Client()
	if err != nil {
		return restic.BackupResult{}, err
	}
	if err := restic.CheckVersion(ctx); err != nil {
		return restic.BackupResult{}, err
	}
	afterPull, err := a.State.AfterPull(f.Root, f.Path)
	if err != nil {
		return restic.BackupResult{}, err
	}
	res, err := client.Backup(ctx, restic.BackupOptions{
		Dir:       a.Config.Dir(f),
		Host:      a.Config.Client,
		Tags:      Tags(f),
		Excludes:  f.Ignore,
		Parent:    base,
		AfterPull: afterPull,
	})
	if err != nil {
		return restic.BackupResult{}, err
	}
	if res.SnapshotID != "" {
		if err := a.State.SetBase(f.Root, f.Path, res.SnapshotID); err != nil {
			return res, fmt.Errorf("snapshot %s was saved, but not recorded as the base: %w", res.SnapshotID[:8], err)
		}
		if err := a.State.SetAfterPull(f.Root, f.Path, false); err != nil {
			return res, err
		}
	}
	// The fingerprint from before the backup: a file that changed while
	// restic ran makes the next look differ, and is picked up then.
	if printErr == nil {
		if err := a.State.SetFingerprint(f.Root, f.Path, print); err != nil {
			return res, err
		}
	}
	return res, nil
}

// Changed reports whether the folder differs from its last snapshot or pull.
func (a *App) Changed(ctx context.Context, f config.Folder) (bool, error) {
	last, err := a.State.Fingerprint(f.Root, f.Path)
	if err != nil {
		return false, err
	}
	if last == "" {
		return true, nil
	}
	print, err := fingerprint.Of(ctx, a.Config.Dir(f), f.Ignore)
	return print != last, err
}
