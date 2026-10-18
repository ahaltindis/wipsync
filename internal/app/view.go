package app

import (
	"context"
	"slices"

	"github.com/ahaltindis/wipsync/internal/db"
	"github.com/ahaltindis/wipsync/internal/restic"
)

// History is a folder's snapshots, with where this client and master are.
type History struct {
	Snapshots []restic.Snapshot // from all clients, newest first
	Base      string            // the snapshot this client's folder is based on
	Master    string            // the server's master; "" if not set

	// MasterErr is why master could not be read; the rest is still good.
	MasterErr error
}

// Log returns the folder's history.
func (a *App) Log(ctx context.Context, f db.Folder) (History, error) {
	var h History
	client, err := a.Client()
	if err != nil {
		return h, err
	}
	if err := restic.CheckVersion(ctx); err != nil {
		return h, err
	}
	if h.Base, err = a.DB.Base(f.Root, f.Path); err != nil {
		return h, err
	}
	if h.Snapshots, err = client.Snapshots(ctx, Tags(f)); err != nil {
		return h, err
	}
	slices.Reverse(h.Snapshots)
	if pointers, err := a.Pointers(); err != nil {
		h.MasterErr = err
	} else {
		h.Master, h.MasterErr = pointers.Master(ctx, f.Root, f.Path)
	}
	return h, nil
}

// Overview is every folder in the repository or on this client.
type Overview struct {
	Folders []FolderView // sorted by name

	// MasterErr is why the masters could not be read; the rest is still good.
	MasterErr error
}

// FolderView is one folder as seen from this client.
type FolderView struct {
	Folder db.Folder
	Dir    string                     // where it is on this client; "" if it is not
	Latest map[string]restic.Snapshot // each client's latest snapshot

	// Master is the server's master: its ID, and the snapshot if it is one
	// of the folder's.
	Master         string
	MasterSnapshot *restic.Snapshot
}

// List returns the overview.
func (a *App) List(ctx context.Context) (Overview, error) {
	var o Overview
	client, err := a.Client()
	if err != nil {
		return o, err
	}
	if err := restic.CheckVersion(ctx); err != nil {
		return o, err
	}
	all, err := client.Snapshots(ctx, nil)
	if err != nil {
		return o, err
	}
	var masters map[string]string
	pointers, masterErr := a.Pointers()
	if masterErr == nil {
		masters, masterErr = pointers.All(ctx)
	}
	o.MasterErr = masterErr

	views := map[string]*FolderView{}
	view := func(f db.Folder) *FolderView {
		name := f.String()
		if views[name] == nil {
			views[name] = &FolderView{Folder: db.Folder{Root: f.Root, Path: f.Path}, Latest: map[string]restic.Snapshot{}}
		}
		return views[name]
	}
	byID := map[string]restic.Snapshot{}
	// Snapshots come oldest first, so the last one seen per client is its
	// latest.
	for _, s := range all {
		if f, ok := FolderOf(s.Tags); ok {
			view(f).Latest[s.Host] = s
			byID[s.ID] = s
		}
	}
	for _, f := range a.Data.Folders {
		v := view(f)
		v.Folder, v.Dir = f, a.Data.Dir(f)
	}
	for name, v := range views {
		v.Master = masters[name]
		if s, ok := byID[v.Master]; ok {
			v.MasterSnapshot = &s
		}
		o.Folders = append(o.Folders, *v)
	}
	slices.SortFunc(o.Folders, func(x, y FolderView) int {
		switch xn, yn := x.Folder.String(), y.Folder.String(); {
		case xn < yn:
			return -1
		case xn > yn:
			return 1
		}
		return 0
	})
	return o, nil
}

// Shown returns the snapshots to show for the folder, newest first: every
// client's latest, and master if it is not one of those.
func (v FolderView) Shown() []restic.Snapshot {
	shown := make([]restic.Snapshot, 0, len(v.Latest)+1)
	for _, s := range v.Latest {
		shown = append(shown, s)
	}
	if m := v.MasterSnapshot; m != nil && v.Latest[m.Host].ID != m.ID {
		// Master is an older snapshot of its client, which appears twice.
		shown = append(shown, *m)
	}
	slices.SortFunc(shown, func(x, y restic.Snapshot) int { return y.Time.Compare(x.Time) })
	return shown
}
