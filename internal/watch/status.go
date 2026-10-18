package watch

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/ahaltindis/wipsync/internal/app"
)

// Recent is how many of the last things the watcher did are kept for Show.
const Recent = 10

// State is what the watcher is doing, or what is due, as one word. When
// several apply, the one listed last wins.
type State int

const (
	UpToDate     State = iota // everything here is on the server, nothing to take
	Behind                    // master moved, or another client has newer work
	Snapshotting              // a snapshot is being sent
	Offline                   // the server does not answer
)

func (s State) String() string {
	return [...]string{"up to date", "behind", "snapshotting", "server unreachable"}[s]
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
	st := Status{State: UpToDate, Detail: "up to date", Events: w.recent}
	switch {
	case w.offline:
		st.State = Offline
		st.Detail = "server unreachable, trying again at " + w.nextPing.Local().Format("15:04")
	case w.snapping != "":
		st.State = Snapshotting
		st.Detail = "snapshotting " + w.snapping
	case len(w.behind) > 0:
		st.State = Behind
		st.Detail = "behind: " + strings.Join(w.behind, ", ")
	}
	return st
}

// show hands the status to Show when it changed since it was last shown.
func (w *Watcher) show() {
	if w.Show == nil {
		return
	}
	st := w.status()
	if w.shown != nil && st.State == w.shown.State && st.Detail == w.shown.Detail &&
		slices.Equal(st.Events, w.shown.Events) {
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
