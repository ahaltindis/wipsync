package watch

import (
	"strings"
	"testing"
	"time"

	"github.com/ahaltindis/wipsync/internal/app"
	"github.com/ahaltindis/wipsync/internal/config"
	"github.com/ahaltindis/wipsync/internal/restic"
)

// shows records every status handed to Show.
func shows(c *clock) *[]Status {
	var got []Status
	c.w.Show = func(st Status) { got = append(got, st) }
	return &got
}

func states(list []Status) string {
	var s []string
	for _, st := range list {
		s = append(s, st.State.String())
	}
	return strings.Join(s, ", ")
}

func TestStatusSnapshotting(t *testing.T) {
	c := newClock(t)
	got := shows(c)
	c.at(0)
	if states(*got) != "up to date" || len((*got)[0].Events) != 1 || (*got)[0].Events[0].Line != "watching 2 folders" {
		t.Errorf("at the start: %+v", *got)
	}
	// Nothing changed: shown again only when something does.
	*got = nil
	c.at(10 * time.Minute)
	if len(*got) != 0 {
		t.Errorf("shown while nothing changed: %v", states(*got))
	}

	c.back.print["Projects/app"] = "a2"
	c.at(Poll + Quiet + tick)
	if want := "snapshotting, up to date"; states(*got) != want {
		t.Errorf("states = %s, want %s", states(*got), want)
	}
	if d := (*got)[0].Detail; d != "snapshotting Projects/app" {
		t.Errorf("detail = %q", d)
	}
	last := (*got)[1].Events[0]
	if last.Line != "Projects/app: snapshot 55555555, 0 new, 1 changed, 0 B stored" {
		t.Errorf("newest event = %+v", last)
	}
}

func TestStatusOffline(t *testing.T) {
	c := newClock(t)
	got := shows(c)
	c.at(0)
	*got = nil
	c.back.down = true
	c.back.print["Projects/app"] = "a2"
	c.at(Poll + Quiet + tick)
	if states(*got) != "server unreachable" {
		t.Fatalf("states = %s", states(*got))
	}
	if d := (*got)[0].Detail; !strings.HasPrefix(d, "server unreachable, trying again at ") {
		t.Errorf("detail = %q", d)
	}
	c.back.down = false
	c.at(FirstRetry)
	// Back: the waiting folder is sent.
	if want := "server unreachable, snapshotting, up to date"; states(*got) != want {
		t.Fatalf("states = %s, want %s", states(*got), want)
	}
	if e := (*got)[2].Events; e[0].Line != "Projects/app: snapshot 55555555, 0 new, 1 changed, 0 B stored" ||
		e[1].Line != "the server is reachable again" {
		t.Errorf("events = %+v", e)
	}
}

func TestStatusBehind(t *testing.T) {
	c, _ := newsClock(t)
	got := shows(c)
	m1, mine, other := snap("a1", "desktop", 30), snap("b1", "laptop", 40), snap("c1", "desktop", 3)
	here := func(path string, st app.Status, base, master string, snaps ...restic.Snapshot) app.FolderState {
		return app.FolderState{Folder: config.Folder{Root: "Projects", Path: path}, Dir: "/p/" + path, Status: st,
			Base: base, Master: master, Snapshots: snaps}
	}
	c.back.world = app.World{Folders: []app.FolderState{
		here("app", app.Behind, mine.ID, m1.ID, m1, mine),
		here("api", app.InStep, m1.ID, m1.ID, other, m1),
		here("web", app.Ahead, mine.ID, m1.ID, mine, m1),
	}}
	c.at(0)
	st := (*got)[len(*got)-1]
	if st.State != Behind || st.Detail != "behind: Projects/app, Projects/api" {
		t.Errorf("status = %s %q", st.State, st.Detail)
	}
	// Taken: up to date at the next check.
	c.back.world.Folders = c.back.world.Folders[2:]
	c.at(Check)
	if st := (*got)[len(*got)-1]; st.State != UpToDate {
		t.Errorf("after the pull: %s", st.State)
	}
}

func TestRecentKept(t *testing.T) {
	c := newClock(t)
	for i := range Recent + 5 {
		c.w.say(c.now.Add(time.Duration(i)*time.Second), "line %d", i)
	}
	e := c.w.status().Events
	if len(e) != Recent || e[0].Line != "line 14" || e[Recent-1].Line != "line 5" {
		t.Errorf("events = %+v", e)
	}
}
