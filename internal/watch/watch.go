// Package watch keeps a client's folders snapshotted. It looks at each
// folder every so often, and snapshots one that changed once it has been
// quiet for a while. It pulls or pushes only when asked to, such as from
// the tray's menu.
package watch

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"

	"github.com/ahaltindis/wipsync/internal/app"
	"github.com/ahaltindis/wipsync/internal/db"
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
	Here() []db.Folder
	Look(ctx context.Context, f db.Folder) (print string, changed bool, err error)
	Snapshot(ctx context.Context, f db.Folder) (restic.BackupResult, error)
	Reach(ctx context.Context) error
	Survey(ctx context.Context) (app.World, error)
	ClientName() string
	PullAll(ctx context.Context, scope app.Scope) (app.AllResult, error)
	PushAll(ctx context.Context, scope app.Scope) (app.AllResult, error)
}

// Action is what the watcher can be asked to do between its rounds.
type Action int

const (
	PullAll Action = iota // app.PullAll, for every folder
	PushAll               // app.PushAll, for every folder
)

var actionNames = [...]string{"pull all", "push all"}

func (a Action) String() string { return actionNames[a] }

// ParseAction is the action String names.
func ParseAction(s string) (Action, bool) {
	for i, name := range actionNames {
		if name == s {
			return Action(i), true
		}
	}
	return 0, false
}

// Request asks the watcher for an action. Reply, if not nil, is given how
// it went.
type Request struct {
	Action Action
	Reply  chan<- Reply
}

// Reply is how an action went.
type Reply struct {
	Result app.AllResult
	Err    error
}

// Watcher runs the loop. Open is called again every Poll, so that folders
// added by add or pull are picked up without a restart.
type Watcher struct {
	Open func() (Backend, error)
	Log  *log.Logger
	// Notify shows news on the desktop; when it is nil or fails, the news
	// is only logged.
	Notify func(ctx context.Context, title, body string, urgent bool) error
	// Show is given the status whenever it changes; it may be nil.
	Show func(Status)
	// Asks brings actions to run between rounds; it may be nil.
	Asks <-chan Request

	back     Backend
	opened   time.Time
	openErr  string
	folders  map[string]*folder
	offline  bool
	retry    time.Duration // the wait before the next try, while offline
	nextPing time.Time
	lastPing time.Time

	nextCheck time.Time
	checkErr  string
	told      map[string]time.Time // news told, by key, and when
	known     map[string]bool      // folders not on this client, as of the last check
	notifyErr string

	recent   []Event // newest first, at most Recent
	snapping string  // the folder being snapshotted, if any
	acting   Action  // the action running, while isActing
	isActing bool
	behind   []string // the folders with something to take, as of the last check
	ahead    []string // the folders with something to push, as of the last check or since
	shown    *Status
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
		case r := <-w.Asks:
			res, err := w.Do(ctx, time.Now(), r.Action)
			if r.Reply != nil {
				r.Reply <- Reply{res, err}
			}
		}
	}
}

// Round does what is due at now.
func (w *Watcher) Round(ctx context.Context, now time.Time) {
	defer w.show()
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
	if !now.Before(w.nextCheck) && ctx.Err() == nil {
		w.check(ctx, now)
	}
}

// check reads the repository and tells what is new.
func (w *Watcher) check(ctx context.Context, now time.Time) {
	if !w.online(ctx, now) {
		w.nextCheck = w.nextPing
		return
	}
	w.nextCheck = now.Add(Check)
	world, err := w.back.Survey(ctx)
	if err != nil {
		if ctx.Err() == nil && err.Error() != w.checkErr {
			w.say(now, "cannot read the repository: %v", err)
			w.checkErr = err.Error()
		}
		return
	}
	w.checkErr = ""
	w.behind = behind(world, w.back.ClientName())
	w.ahead = ahead(world)
	if w.told == nil {
		w.told = map[string]time.Time{}
	}
	var lines []string
	urgent := false
	for _, it := range news(world, w.back.ClientName(), w.known) {
		at, done := w.told[it.key]
		if done && (!strings.HasPrefix(it.key, "wip ") || now.Sub(at) < WIPAgain) {
			continue
		}
		w.told[it.key] = now
		lines = append(lines, it.line)
		urgent = urgent || it.urgent
	}
	// The folders not here now are what a new one is told against next time.
	w.known = map[string]bool{}
	for _, f := range world.Folders {
		if f.Dir == "" {
			w.known[f.Folder.String()] = true
		}
	}
	if len(lines) == 0 {
		return
	}
	for _, l := range lines {
		w.say(now, "%s", l)
	}
	title := "wipsync: 1 update"
	if len(lines) > 1 {
		title = fmt.Sprintf("wipsync: %d updates", len(lines))
	}
	w.notify(ctx, now, title, strings.Join(lines, "\n"), urgent)
}

// notify shows news on the desktop, and logs once why it cannot.
func (w *Watcher) notify(ctx context.Context, now time.Time, title, body string, urgent bool) {
	if w.Notify == nil {
		return
	}
	if err := w.Notify(ctx, title, body, urgent); err != nil {
		if err.Error() != w.notifyErr {
			w.say(now, "cannot notify: %v", err)
			w.notifyErr = err.Error()
		}
	} else {
		w.notifyErr = ""
	}
}

// Do runs an action, logs how it went for each folder, and tells the
// outcome on the desktop. The repository is read again right after.
func (w *Watcher) Do(ctx context.Context, now time.Time, a Action) (app.AllResult, error) {
	defer w.show()
	w.reopen(now)
	if w.back == nil {
		err := errors.New("the service cannot open the client; see journalctl --user -u wipsync")
		w.say(now, "cannot %s: %v", a, err)
		w.notify(ctx, now, "wipsync: "+a.String(), err.Error(), true)
		return app.AllResult{}, err
	}
	w.acting, w.isActing = a, true
	w.show()
	var res app.AllResult
	var err error
	switch a {
	case PullAll:
		res, err = w.back.PullAll(ctx, app.Scope{})
	case PushAll:
		res, err = w.back.PushAll(ctx, app.Scope{})
	}
	w.isActing = false
	w.nextCheck = time.Time{}
	if err != nil {
		if ctx.Err() == nil {
			w.say(now, "cannot %s: %v", a, err)
			w.notify(ctx, now, "wipsync: "+a.String(), err.Error(), true)
		}
		return res, err
	}
	for _, l := range res.Lines {
		w.say(now, "%s", l)
	}
	w.say(now, "%s: %s", a, res.Summary())
	body := res.Summary()
	if len(res.Failed) > 0 {
		body += "\n\n" + strings.Join(res.Failed, "\n")
	}
	w.notify(ctx, now, "wipsync: "+a.String(), body, len(res.Failed) > 0)
	return res, nil
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
			w.say(now, "cannot open the client: %v", err)
			w.openErr = err.Error()
		}
		return
	}
	w.openErr = ""
	if w.back == nil {
		if n := len(back.Here()); n == 1 {
			w.say(now, "watching 1 folder")
		} else {
			w.say(now, "watching %d folders", n)
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
func (w *Watcher) look(ctx context.Context, now time.Time, f db.Folder, st *folder) {
	print, changed, err := w.back.Look(ctx, f)
	switch {
	case err != nil:
		w.fail(now, f, st, err)
		st.due = now.Add(Poll)
		return
	case !changed:
		st.since, st.seen, st.due = time.Time{}, "", now.Add(Poll)
		return
	}
	// Changed: until it is pushed, there is something to push.
	w.toPush(f)
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

	w.snapping = f.String()
	w.show()
	res, err := w.back.Snapshot(ctx, f)
	w.snapping = ""
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
		w.fail(now, f, st, err)
		st.due = now.Add(Poll)
	default:
		if res.SnapshotID != "" {
			w.say(now, "%s: snapshot %s, %d new, %d changed, %s stored",
				f, res.SnapshotID[:8], res.FilesNew, res.FilesChanged, app.FormatBytes(res.BytesStored))
		}
		st.since, st.seen, st.failed, st.due = time.Time{}, "", "", now.Add(Poll)
	}
}

// fail logs a folder's failure, once until it changes or the folder recovers.
func (w *Watcher) fail(now time.Time, f db.Folder, st *folder, err error) {
	if err.Error() != st.failed {
		w.say(now, "%s: %v", f, err)
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
			w.say(now, "the server is reachable again")
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
	w.say(now, "the server is unreachable, trying again in %s: %v", durationText(w.retry), err)
	return false
}

func durationText(d time.Duration) string {
	if d%time.Minute == 0 {
		return fmt.Sprintf("%d min", int(d/time.Minute))
	}
	return d.String()
}
