package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/ahaltindis/wipsync/internal/config"
	"github.com/ahaltindis/wipsync/internal/master"
	"github.com/ahaltindis/wipsync/internal/restic"
)

type PushResult struct {
	Snapshot restic.Snapshot // what master points at now
	Already  bool            // it did so before
}

// Push marks the folder's latest snapshot as master. It takes no snapshot
// and uploads nothing. The server accepts it only if master is still where
// this client last saw it.
func (a *App) Push(ctx context.Context, f config.Folder) (PushResult, error) {
	unlock, err := a.lock(f)
	if err != nil {
		return PushResult{}, err
	}
	defer unlock()
	client, pointers, err := a.remote(ctx)
	if err != nil {
		return PushResult{}, err
	}
	base, err := a.State.Base(f.Root, f.Path)
	if err != nil {
		return PushResult{}, err
	}
	if base == "" {
		return PushResult{}, fmt.Errorf("%s has no snapshot from this client yet; run wipsync snapshot first", f)
	}
	expect, err := a.State.Master(f.Root, f.Path)
	if err != nil {
		return PushResult{}, err
	}
	// Looked up to say exactly what is being pushed, and because a
	// snapshot that is gone must not become master.
	snap, err := client.Find(ctx, base)
	if errors.Is(err, restic.ErrNoSnapshot) {
		return PushResult{}, fmt.Errorf("%s: its latest snapshot %s is not in the repository", f, base[:8])
	} else if err != nil {
		return PushResult{}, err
	}
	res := PushResult{Snapshot: snap}

	err = pointers.Move(ctx, f.Root, f.Path, expect, base)
	var moved *master.MovedError
	switch {
	case errors.As(err, &moved) && moved.Current == base:
		// Master is already there: an earlier push moved it but was cut
		// off before recording that here.
		res.Already = true
	case err == nil:
		res.Already = expect == base
	case errors.As(err, &moved) && moved.Current == "":
		return res, fmt.Errorf("%s: push rejected: this client was in step with master %s, but the server has no master for the folder", f, expect[:8])
	case errors.As(err, &moved):
		return res, fmt.Errorf("%s: push rejected: another client moved master to %s; pull first", f, moved.Current[:8])
	default:
		return res, err
	}
	return res, a.State.SetMaster(f.Root, f.Path, base)
}
