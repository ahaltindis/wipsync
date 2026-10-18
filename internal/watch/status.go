package watch

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/ahaltindis/wipsync/internal/app"
	"github.com/ahaltindis/wipsync/internal/db"
)

// Recent is how many of the last things the watcher did are kept for Show.
const Recent = 10

// State is what the watcher is doing, or what is due, as one word. When
// several apply, the one listed last wins.
type State int

const (
	UpToDate     State = iota // nothing to do: everything here is master, nothing to take
	Ahead                     // a push is due: changes not snapshotted, or snapshots master lacks
	Behind                    // a pull is due: master moved, or another client has newer work
	Snapshotting              // a snapshot is being sent, or an action runs
	Offline                   // the server does not answer
)

func (s State) String() string {
	return [...]string{"up to date", "to push", "behind", "snapshotting", "server unreachable"}[s]
}

// Event is one thing the watcher did or found, as it was logged.
type Event struct {
	At   time.Time
	Line string
}

// Status is what Show is given: the state, a line on it, and the recent
// events, newest first.
type Status struct {
	State  State
	Detail string
	Events []Event
	Acting bool // an action runs; another is not taken until it is done
}

// say logs a line and keeps it as a recent event.
func (w *Watcher) say(now time.Time, format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	w.Log.Print(line)
	w.recent = append([]Event{{At: now, Line: line}}, w.recent...)
	if len(w.recent) > Recent {
		w.recent = w.recent[:Recent]
	}
}

// status is the watcher's Status now.
func (w *Watcher) status() Status {
	st := Status{State: UpToDate, Detail: "up to date, nothing to do", Events: w.recent, Acting: w.isActing}
	switch {
	case w.isActing:
		st.State = Snapshotting
		st.Detail = "running " + w.acting.String()
	case w.offline:
		st.State = Offline
		st.Detail = "server unreachable, trying again at " + w.nextPing.Local().Format("15:04")
	case w.snapping != "":
		st.State = Snapshotting
		st.Detail = "snapshotting " + w.snapping
	case len(w.behind) > 0:
		st.State = Behind
		st.Detail = "behind: " + strings.Join(w.behind, ", ")
	case len(w.ahead) > 0:
		st.State = Ahead
		st.Detail = "to push: " + strings.Join(w.ahead, ", ")
	}
	return st
}

// ahead lists the folders here that have something to push: changes not
// snapshotted yet, or snapshots master does not have.
func ahead(world app.World) []string {
	var out []string
	for _, f := range world.Folders {
		if f.Dir != "" && (f.Status == app.Ahead || f.Status == app.Changed) {
			out = append(out, f.Folder.String())
		}
	}
	return out
}

// toPush notes that a folder has something to push, as seen between checks.
func (w *Watcher) toPush(f db.Folder) {
	if !slices.Contains(w.ahead, f.String()) {
		w.ahead = append(w.ahead, f.String())
	}
}

// show hands the status to Show when it changed since it was last shown.
func (w *Watcher) show() {
	if w.Show == nil {
		return
	}
	st := w.status()
	if w.shown != nil && st.State == w.shown.State && st.Detail == w.shown.Detail &&
		st.Acting == w.shown.Acting && slices.Equal(st.Events, w.shown.Events) {
		return
	}
	w.shown = &st
	w.Show(st)
}

// behind lists the folders here that have something to take: master moved
// past them, or another client has newer work.
func behind(world app.World, client string) []string {
	var out []string
	for _, f := range world.Folders {
		if f.Dir == "" {
			continue
		}
		_, newer := newerWork(f, client)
		if f.Status == app.Behind || f.Status == app.Diverged || newer {
			out = append(out, f.Folder.String())
		}
	}
	return out
}
