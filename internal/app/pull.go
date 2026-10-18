package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ahaltindis/wipsync/internal/db"
	"github.com/ahaltindis/wipsync/internal/fingerprint"
	"github.com/ahaltindis/wipsync/internal/master"
	"github.com/ahaltindis/wipsync/internal/restic"
)

// Target is a snapshot chosen to bring into a folder.
type Target struct {
	Snapshot restic.Snapshot
	IsMaster bool
	All      []restic.Snapshot // the folder's snapshots, oldest first
}

// FindTarget picks what a pull of the folder would take: master, or with
// from the latest snapshot of that client.
func (a *App) FindTarget(ctx context.Context, f db.Folder, from string) (Target, error) {
	client, pointers, err := a.remote(ctx)
	if err != nil {
		return Target{}, err
	}
	list, err := client.Snapshots(ctx, Tags(f))
	if err != nil {
		return Target{}, err
	}
	if len(list) == 0 {
		return Target{}, fmt.Errorf("there are no snapshots of %s in the repository; see wipsync list", f)
	}
	snap, isMaster, err := pullTarget(ctx, pointers, list, f, from)
	return Target{Snapshot: snap, IsMaster: isMaster, All: list}, err
}

// pullTarget picks the snapshot to pull from a folder's snapshots, oldest
// first: master, or the latest one of the named client.
func pullTarget(ctx context.Context, pointers master.Pointers, list []restic.Snapshot, f db.Folder, from string) (restic.Snapshot, bool, error) {
	var clients []string
	for _, s := range list {
		if !slices.Contains(clients, s.Host) {
			clients = append(clients, s.Host)
		}
	}
	slices.Sort(clients)
	have := strings.Join(clients, ", ")

	if from != "" {
		for _, s := range slices.Backward(list) {
			if s.Host == from {
				return s, false, nil
			}
		}
		return restic.Snapshot{}, false, fmt.Errorf("%s has no snapshots from client %q; it has them from: %s", f, from, have)
	}
	id, err := pointers.Master(ctx, f.Root, f.Path)
	if err != nil {
		return restic.Snapshot{}, false, err
	}
	if id == "" {
		return restic.Snapshot{}, false, fmt.Errorf("%s has no master yet; take a client's latest snapshot with --from (clients: %s)", f, have)
	}
	for _, s := range list {
		if s.ID == id {
			return s, true, nil
		}
	}
	return restic.Snapshot{}, false, fmt.Errorf("master of %s is %s, which is not among the folder's snapshots", f, id[:8])
}

// PullOutcome says what a pull did.
type PullOutcome int

const (
	Pulled        PullOutcome = iota // the folder was made to match the target
	AlreadyThere                     // the folder is at the target
	AheadOfTarget                    // the folder contains the target and has moved on
)

type PullResult struct {
	Outcome PullOutcome
	Target  restic.Snapshot
	Dir     string // where the folder is on this client
	New     bool   // the folder was not on this client before

	// Saved is the snapshot taken of the folder's state first, if it had
	// changed. It is set even when the pull then fails.
	Saved string

	// Ignore holds the folder's ignore rules if the pull changed them.
	Ignore        []string
	IgnoreChanged bool
}

// Pull brings master, or with from a client's latest snapshot, into a
// folder that is on this client, if the target is ahead of it. The
// folder's state is saved as a snapshot first.
func (a *App) Pull(ctx context.Context, f db.Folder, from string) (PullResult, error) {
	res := PullResult{Dir: a.Data.Dir(f)}
	unlock, err := a.lock(f)
	if err != nil {
		return res, err
	}
	defer unlock()
	// Saved first, so that whatever the pull does can be undone.
	saved, err := a.snapshot(ctx, f)
	if err != nil {
		return res, err
	}
	res.Saved = saved.SnapshotID
	base, err := a.DB.Base(f.Root, f.Path)
	if err != nil {
		return res, err
	}
	if base == "" {
		return res, fmt.Errorf("%s has no snapshot from this client to compare with; run wipsync snapshot first", f)
	}
	t, err := a.FindTarget(ctx, f, from)
	if err != nil {
		return res, err
	}
	res.Target = t.Snapshot
	parents := map[string]string{}
	for _, s := range t.All {
		parents[s.ID] = s.Parent
	}
	inStep := func() error {
		if !t.IsMaster {
			return nil
		}
		return a.DB.SetMaster(f.Root, f.Path, t.Snapshot.ID)
	}

	switch {
	case t.Snapshot.ID == base:
		res.Outcome = AlreadyThere
		return res, inStep()

	case descends(parents, base, t.Snapshot.ID):
		res.Outcome = AheadOfTarget
		return res, inStep()

	case !descends(parents, t.Snapshot.ID, base):
		since := "they were last the same"
		if common := commonAncestor(parents, base, t.Snapshot.ID); common != "" {
			since = "snapshot " + common[:8]
		}
		return res, fmt.Errorf("%s: both this folder and %s changed since %s. "+
			"Pull cannot merge yet and changed nothing; the folder's state is saved as snapshot %s. "+
			"To take the other side as it is: wipsync restore %s",
			f, Describe(t.Snapshot), since, base[:8], t.Snapshot.ID[:8])
	}
	res.Outcome = Pulled
	res.Ignore, res.IgnoreChanged, err = a.apply(ctx, f, t.Snapshot, t.IsMaster, base)
	return res, err
}

// RootDir returns where a root is on this client.
func (a *App) RootDir(root string) (string, bool) {
	dir, ok := a.Data.Roots[root]
	return dir, ok
}

// CheckNewRoot reports why root cannot be placed at dir on this client.
func (a *App) CheckNewRoot(root, dir string) error {
	if problem := a.CheckRootName(root); problem != "" {
		return errors.New(problem)
	}
	if name, ok := a.Data.RootAt(dir); ok {
		return fmt.Errorf("%s is already the root %s on this client", dir, name)
	}
	return nil
}

// Fetch brings a folder that is not on this client yet into rootDir, the
// location of its root here.
func (a *App) Fetch(ctx context.Context, f db.Folder, rootDir string, t Target) (PullResult, error) {
	dir := filepath.Join(rootDir, filepath.FromSlash(f.Path))
	res := PullResult{Outcome: Pulled, Target: t.Snapshot, Dir: dir, New: true}
	unlock, err := a.lock(f)
	if err != nil {
		return res, err
	}
	defer unlock()
	client, err := a.Client()
	if err != nil {
		return res, err
	}
	if other, ok := a.Data.Overlap(dir); ok {
		return res, fmt.Errorf("%s overlaps %s, which is already on this client", dir, other)
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		return res, fmt.Errorf("%s exists and is not empty; pull would replace what is in it. Move it away first", dir)
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return res, err
	}
	// Created here, because a directory restic creates is closed to
	// everyone but its owner.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return res, err
	}
	if _, err := client.Restore(ctx, restic.RestoreOptions{ID: t.Snapshot.ID, Dir: dir, Excludes: t.Snapshot.Excludes}); err != nil {
		return res, fmt.Errorf("%w. %s may be partly filled; remove it before trying again", err, dir)
	}

	// The folder keeps the ignore rules its snapshot was taken with, so
	// that every client applies the same ones.
	f.Ignore = t.Snapshot.Excludes
	if f.Ignore == nil {
		f.Ignore = []string{}
	}
	// Added and recorded at once, so that it is not known here without
	// knowing what it was pulled from.
	print := a.print(ctx, f, dir)
	err = a.DB.Update(func(w db.Writer) error {
		if err := w.AddFolder(f, rootDir); err != nil {
			return err
		}
		return record(w, f, t.Snapshot, t.IsMaster, print)
	})
	if err != nil {
		return res, err
	}
	a.Data.Roots[f.Root] = rootDir
	a.Data.Folders = append(a.Data.Folders, f)
	return res, nil
}

// print takes the fingerprint of the folder at dir, as it is after a pull:
// what later looks are compared with. It is "" if it cannot be taken.
func (a *App) print(ctx context.Context, f db.Folder, dir string) string {
	print, err := fingerprint.Of(ctx, dir, f.Ignore)
	if err != nil {
		return ""
	}
	return print
}

// record notes that the folder now matches the snapshot, and has the
// fingerprint print.
func record(w db.Writer, f db.Folder, target restic.Snapshot, isMaster bool, print string) error {
	if err := w.SetBase(f.Root, f.Path, target.ID); err != nil {
		return err
	}
	if isMaster {
		if err := w.SetMaster(f.Root, f.Path, target.ID); err != nil {
			return err
		}
	}
	if err := w.SetAfterPull(f.Root, f.Path, true); err != nil {
		return err
	}
	if print == "" {
		return nil
	}
	return w.SetFingerprint(f.Root, f.Path, print)
}

// apply makes the folder match the target snapshot and records that. base
// is the snapshot holding the folder's state from before, for the message
// if it fails halfway. It returns the folder's ignore rules and whether
// they changed.
func (a *App) apply(ctx context.Context, f db.Folder, target restic.Snapshot, isMaster bool, base string) ([]string, bool, error) {
	client, err := a.Client()
	if err != nil {
		return nil, false, err
	}
	// Paths ignored on either side are left alone, so that nothing a
	// client keeps out of its snapshots is deleted here.
	dir := a.Data.Dir(f)
	excludes := slices.Clone(f.Ignore)
	for _, e := range target.Excludes {
		if !slices.Contains(excludes, e) {
			excludes = append(excludes, e)
		}
	}
	if _, err := client.Restore(ctx, restic.RestoreOptions{ID: target.ID, Dir: dir, Excludes: excludes}); err != nil {
		return nil, false, fmt.Errorf("%w. %s may be partly updated; its state from before is snapshot %s", err, dir, base[:8])
	}

	// From here on the folder follows the rules its snapshot was taken with.
	ignore := slices.Clone(target.Excludes)
	if ignore == nil {
		ignore = []string{}
	}
	changed := !slices.Equal(f.Ignore, ignore)
	f.Ignore = ignore
	print := a.print(ctx, f, dir)
	err = a.DB.Update(func(w db.Writer) error {
		if changed {
			if err := w.SetIgnore(f, ignore); err != nil {
				return err
			}
		}
		return record(w, f, target, isMaster, print)
	})
	if err != nil {
		return nil, false, err
	}
	for i := range a.Data.Folders {
		if a.Data.Folders[i].String() == f.String() {
			a.Data.Folders[i].Ignore = ignore
		}
	}
	return ignore, changed, nil
}

// descends reports whether snapshot id has ancestor somewhere up its chain
// of parents. parents maps each snapshot to its parent.
func descends(parents map[string]string, id, ancestor string) bool {
	for steps, cur := 0, parents[id]; cur != "" && steps <= len(parents); steps, cur = steps+1, parents[cur] {
		if cur == ancestor {
			return true
		}
	}
	return false
}

// commonAncestor returns the nearest snapshot both a and b descend from,
// or "" if their chains never meet.
func commonAncestor(parents map[string]string, a, b string) string {
	seen := map[string]bool{}
	for steps, cur := 0, a; cur != "" && steps <= len(parents); steps, cur = steps+1, parents[cur] {
		seen[cur] = true
	}
	for steps, cur := 0, b; cur != "" && steps <= len(parents); steps, cur = steps+1, parents[cur] {
		if seen[cur] {
			return cur
		}
	}
	return ""
}
