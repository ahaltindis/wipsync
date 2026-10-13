package app

import (
	"context"
	"slices"
	"strings"

	"github.com/ahaltindis/wipsync/internal/config"
	"github.com/ahaltindis/wipsync/internal/master"
	"github.com/ahaltindis/wipsync/internal/restic"
)

// Status is where a folder on this client stands, and so what is due.
type Status int

const (
	InStep   Status = iota // nothing to do
	Changed                // edited since its last snapshot: a snapshot is due
	Ahead                  // has snapshots master does not: a push is due
	Behind                 // master moved on: a pull is due
	Diverged               // both moved since they were the same: a pull would be refused
	NotHere                // in the repository, but not on this client
)

// FolderState is everything known about one folder.
type FolderState struct {
	Folder config.Folder
	Dir    string // where it is on this client; "" if it is not

	Snapshots []restic.Snapshot // from all clients, newest first
	Base      string            // the snapshot this client's folder is based on
	Master    string            // the server's master; "" if not set
	Changed   bool              // the folder differs from its last snapshot or pull
	Status    Status
}

// World is every folder in the repository or on this client.
type World struct {
	Folders []FolderState // sorted by root, then by path

	// MasterErr is why the masters could not be read. The statuses then
	// only tell changed folders from unchanged ones.
	MasterErr error
}

// Survey looks at everything at once: one call to restic, one for the
// masters, and a fingerprint of every folder on this client.
func (a *App) Survey(ctx context.Context) (World, error) {
	var w World
	client, err := a.Client()
	if err != nil {
		return w, err
	}
	if err := restic.CheckVersion(ctx); err != nil {
		return w, err
	}
	all, err := client.Snapshots(ctx, nil)
	if err != nil {
		return w, err
	}
	var masters map[string]string
	pointers, masterErr := master.Open(a.Config.Repository)
	if masterErr == nil {
		masters, masterErr = pointers.All(ctx)
	}
	w.MasterErr = masterErr

	states := map[string]*FolderState{}
	state := func(f config.Folder) *FolderState {
		name := f.String()
		if states[name] == nil {
			states[name] = &FolderState{Folder: config.Folder{Root: f.Root, Path: f.Path}, Status: NotHere}
		}
		return states[name]
	}
	for _, s := range all {
		if f, ok := FolderOf(s.Tags); ok {
			st := state(f)
			st.Snapshots = append(st.Snapshots, s)
		}
	}
	for _, f := range a.Config.Folders {
		st := state(f)
		st.Folder, st.Dir = f, a.Config.Dir(f)
		if st.Base, err = a.State.Base(f.Root, f.Path); err != nil {
			return w, err
		}
		// A folder that cannot be looked at counts as changed.
		changed, err := a.Changed(ctx, f)
		st.Changed = changed || err != nil
	}
	for name, st := range states {
		st.Master = masters[name]
		if st.Dir != "" {
			st.Status = status(*st)
		}
		slices.Reverse(st.Snapshots) // restic lists oldest first
		w.Folders = append(w.Folders, *st)
	}
	slices.SortFunc(w.Folders, func(x, y FolderState) int {
		if x.Folder.Root != y.Folder.Root {
			return compare(x.Folder.Root, y.Folder.Root)
		}
		return compare(x.Folder.Path, y.Folder.Path)
	})
	return w, nil
}

// compare orders names the way a reader expects: without regard to case,
// and by case only between names that are otherwise the same.
func compare(x, y string) int {
	if c := strings.Compare(strings.ToLower(x), strings.ToLower(y)); c != 0 {
		return c
	}
	return strings.Compare(x, y)
}

// status works out what is due for a folder on this client, from its
// snapshots' parent links.
func status(st FolderState) Status {
	parents := map[string]string{}
	for _, s := range st.Snapshots {
		parents[s.ID] = s.Parent
	}
	_, masterKnown := parents[st.Master]
	switch {
	case st.Base == "":
		return Changed // never snapshotted here
	case st.Master == "" || !masterKnown:
		// Nothing to compare with: only local changes can be told.
	case st.Master == st.Base:
	case descends(parents, st.Master, st.Base):
		if st.Changed {
			return Diverged // the snapshot a pull takes first would put it there
		}
		return Behind
	case !descends(parents, st.Base, st.Master):
		return Diverged
	}
	if st.Changed {
		return Changed
	}
	if st.Master != st.Base {
		return Ahead // including when there is no master yet
	}
	return InStep
}
