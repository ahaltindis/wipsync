// Package watch keeps a client's folders snapshotted. It looks at each
// folder every so often, and snapshots one that changed once it has been
// quiet for a while. It never pulls or pushes.
package watch

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"time"

	"github.com/ahaltindis/wipsync/internal/app"
	"github.com/ahaltindis/wipsync/internal/config"
	"github.com/ahaltindis/wipsync/internal/restic"
	"github.com/ahaltindis/wipsync/internal/state"
)

const (
	Poll    = time.Minute      // how often a folder is looked at
	Quiet   = 30 * time.Second // how long a change must rest before it is saved
	MaxWait = 5 * time.Minute  // how long a folder that keeps changing waits at most

	// While the server is unreachable, it is tried again after FirstRetry,
	// then twice as long each time, up to MaxRetry.
	FirstRetry = 5 * time.Minute
	MaxRetry   = 30 * time.Minute

	tick = 5 * time.Second // how often the loop wakes to see what is due
)

// Backend is what the watcher asks of the client. *app.App is the real one.
type Backend interface {
	Here() []config.Folder
	Look(ctx context.Context, f config.Folder) (print string, changed bool, err error)
	Snapshot(ctx context.Context, f config.Folder) (restic.BackupResult, error)
	Reach(ctx context.Context) error
}

// Watcher runs the loop. Open is called again every Poll, so that folders
// added to the config are picked up without a restart.
type Watcher struct {
	Open func() (Backend, error)
	Log  *log.Logger

	back     Backend
	opened   time.Time
	openErr  string
	folders  map[string]*folder
	offline  bool
	retry    time.Duration // the wait before the next try, while offline
	nextPing time.Time
	lastPing time.Time
}

// folder is what the watcher remembers of one folder.
type folder struct {
	due    time.Time // when to look at it next
	seen   string    // its fingerprint at the last look, while it is changed
	since  time.Time // when the change was first seen; zero while unchanged
	failed string    // the last failure logged, so it is not logged again
}

// Run watches until ctx ends.
func (w *Watcher) Run(ctx context.Context) error {
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		w.Round(ctx, time.Now())
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// Round does what is due at now.
func (w *Watcher) Round(ctx context.Context, now time.Time) {
	w.reopen(now)
	if w.back == nil {
		return
	}
	for _, f := range w.back.Here() {
		if ctx.Err() != nil {
			return
		}
		st := w.folders[f.String()]
		if st == nil {
			st = &folder{}
			w.folders[f.String()] = st
		}
		if now.Before(st.due) {
			continue
		}
		w.look(ctx, now, f, st)
	}
}

func (w *Watcher) reopen(now time.Time) {
	if w.back != nil && now.Sub(w.opened) < Poll {
		return
	}
	w.opened = now
	back, err := w.Open()
	if err != nil {
		// The last good config goes on being used.
		if err.Error() != w.openErr {
			w.Log.Printf("cannot read the config: %v", err)
			w.openErr = err.Error()
		}
		return
	}
	w.openErr = ""
	if w.back == nil {
		if n := len(back.Here()); n == 1 {
			w.Log.Printf("watching 1 folder")
		} else {
			w.Log.Printf("watching %d folders", n)
		}
	}
	w.back = back
	if w.folders == nil {
		w.folders = map[string]*folder{}
	}
	// Forget the folders that are gone.
	var names []string
	for _, f := range back.Here() {
		names = append(names, f.String())
	}
	for name := range w.folders {
		if !slices.Contains(names, name) {
			delete(w.folders, name)
		}
	}
}

// look decides what to do with a folder: nothing, wait, or snapshot.
func (w *Watcher) look(ctx context.Context, now time.Time, f config.Folder, st *folder) {
	print, changed, err := w.back.Look(ctx, f)
	switch {
	case err != nil:
		w.fail(f, st, err)
		st.due = now.Add(Poll)
		return
	case !changed:
		st.since, st.seen, st.due = time.Time{}, "", now.Add(Poll)
		return
	}
	first := st.since.IsZero()
	if first {
		st.since = now
	}
	ready := !first && (print == st.seen || now.Sub(st.since) >= MaxWait)
	st.seen = print
	if !ready {
		st.due = now.Add(Quiet)
		return
	}
	if !w.online(ctx, now) {
		st.due = w.nextPing
		return
	}

	res, err := w.back.Snapshot(ctx, f)
	switch {
	case errors.Is(err, state.ErrBusy):
		// Another wipsync has it: look again soon.
		st.due = now.Add(Quiet)
	case err != nil && ctx.Err() != nil:
		// Stopped while it ran.
	case err != nil:
		// The server may have gone while restic ran.
		w.lastPing = time.Time{}
		if !w.online(ctx, now) {
			st.due = w.nextPing
			return
		}
		w.fail(f, st, err)
		st.due = now.Add(Poll)
	default:
		if res.SnapshotID != "" {
			w.Log.Printf("%s: snapshot %s, %d new, %d changed, %s stored",
				f, res.SnapshotID[:8], res.FilesNew, res.FilesChanged, app.FormatBytes(res.BytesStored))
		}
		st.since, st.seen, st.failed, st.due = time.Time{}, "", "", now.Add(Poll)
	}
}

// fail logs a folder's failure, once until it changes or the folder recovers.
func (w *Watcher) fail(f config.Folder, st *folder, err error) {
	if err.Error() != st.failed {
		w.Log.Printf("%s: %v", f, err)
		st.failed = err.Error()
	}
}

// online reports whether the server can be reached, asking it at most once
// a Poll while it answers, and less and less often while it does not.
func (w *Watcher) online(ctx context.Context, now time.Time) bool {
	if w.offline && now.Before(w.nextPing) {
		return false
	}
	if !w.offline && !w.lastPing.IsZero() && now.Sub(w.lastPing) < Poll {
		return true
	}
	err := w.back.Reach(ctx)
	w.lastPing = now
	if err == nil {
		if w.offline {
			w.Log.Printf("the server is reachable again")
		}
		w.offline, w.retry = false, 0
		return true
	}
	if ctx.Err() != nil {
		return false
	}
	switch {
	case !w.offline:
		w.retry = FirstRetry
	case w.retry < MaxRetry:
		w.retry = min(2*w.retry, MaxRetry)
	}
	w.offline, w.nextPing = true, now.Add(w.retry)
	w.Log.Printf("the server is unreachable, trying again in %s: %v", durationText(w.retry), err)
	return false
}

func durationText(d time.Duration) string {
	if d%time.Minute == 0 {
		return fmt.Sprintf("%d min", int(d/time.Minute))
	}
	return d.String()
}
