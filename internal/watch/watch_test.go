package watch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/ahaltindis/wipsync/internal/config"
	"github.com/ahaltindis/wipsync/internal/restic"
	"github.com/ahaltindis/wipsync/internal/state"
)

// fake is a client whose folders change when the test says so.
type fake struct {
	folders []config.Folder
	print   map[string]string // each folder's fingerprint now
	saved   map[string]string // and at its last snapshot
	down    bool              // the server does not answer
	busy    bool
	fail    error
	calls   []string
}

func (f *fake) Here() []config.Folder { return f.folders }

func (f *fake) Look(_ context.Context, fo config.Folder) (string, bool, error) {
	p := f.print[fo.String()]
	return p, p != f.saved[fo.String()], nil
}

func (f *fake) Snapshot(_ context.Context, fo config.Folder) (restic.BackupResult, error) {
	f.calls = append(f.calls, "snapshot "+fo.String())
	switch {
	case f.busy:
		return restic.BackupResult{}, fmt.Errorf("%s is busy: %w", fo, state.ErrBusy)
	case f.down:
		return restic.BackupResult{}, errors.New("restic backup: connection refused")
	case f.fail != nil:
		return restic.BackupResult{}, f.fail
	}
	f.saved[fo.String()] = f.print[fo.String()]
	return restic.BackupResult{SnapshotID: strings.Repeat("5", 64), FilesChanged: 1}, nil
}

func (f *fake) Reach(context.Context) error {
	f.calls = append(f.calls, "ping")
	if f.down {
		return errors.New("ssh: connect timed out")
	}
	return nil
}

// clock runs rounds at times the test moves on.
type clock struct {
	t    *testing.T
	w    *Watcher
	back *fake
	now  time.Time
	logs *bytes.Buffer
}

func newClock(t *testing.T) *clock {
	back := &fake{
		folders: []config.Folder{{Root: "Projects", Path: "app"}, {Root: "Projects", Path: "web"}},
		print:   map[string]string{"Projects/app": "a1", "Projects/web": "w1"},
		saved:   map[string]string{"Projects/app": "a1", "Projects/web": "w1"},
	}
	var logs bytes.Buffer
	w := &Watcher{Open: func() (Backend, error) { return back, nil }, Log: log.New(&logs, "", 0)}
	c := &clock{t: t, w: w, back: back, now: time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC), logs: &logs}
	c.at(0)
	return c
}

// at moves the clock on by d, a tick at a time, and returns what was asked
// of the client meanwhile.
func (c *clock) at(d time.Duration) []string {
	c.back.calls = nil
	end := c.now.Add(d)
	for {
		c.w.Round(context.Background(), c.now)
		if !c.now.Before(end) {
			break
		}
		c.now = c.now.Add(tick)
	}
	return c.back.calls
}

func (c *clock) log() string {
	s := c.logs.String()
	c.logs.Reset()
	return s
}

func TestQuietThenSnapshot(t *testing.T) {
	c := newClock(t)
	if calls := c.at(10 * time.Minute); len(calls) != 0 {
		t.Errorf("unchanged folders: %v", calls)
	}
	if got := c.log(); got != "watching 2 folders\n" {
		t.Errorf("log = %q", got)
	}

	// A change is seen at the next look, and saved once it rested Quiet.
	c.back.print["Projects/app"] = "a2"
	calls := c.at(Poll - tick)
	if len(calls) != 0 {
		t.Errorf("before it rested: %v", calls)
	}
	calls = c.at(Quiet + tick)
	if want := "ping snapshot Projects/app"; strings.Join(calls, " ") != want {
		t.Errorf("calls = %v, want %s", calls, want)
	}
	if got := c.log(); got != "Projects/app: snapshot 55555555, 0 new, 1 changed, 0 B stored\n" {
		t.Errorf("log = %q", got)
	}
	// Saved: nothing more to do.
	if calls := c.at(10 * time.Minute); len(calls) != 0 {
		t.Errorf("after the snapshot: %v", calls)
	}
}

func TestKeepsChanging(t *testing.T) {
	c := newClock(t)
	start := c.now
	var at time.Time
	for i := 0; c.now.Sub(start) < 2*MaxWait && at.IsZero(); i++ {
		c.back.print["Projects/app"] = fmt.Sprintf("a%d", i)
		for _, call := range c.at(tick) {
			if call == "snapshot Projects/app" {
				at = c.now
			}
		}
	}
	// It waits for MaxWait from when the change was first seen, which is
	// up to a Poll after the change.
	if at.IsZero() || at.Sub(start) < MaxWait || at.Sub(start) > MaxWait+Poll+Quiet {
		t.Errorf("a folder that keeps changing was saved %s after it started", at.Sub(start))
	}
}

func TestBusyFolderWaits(t *testing.T) {
	c := newClock(t)
	c.back.print["Projects/app"] = "a2"
	c.back.busy = true
	calls := c.at(Poll + Quiet + tick)
	if strings.Join(calls, " ") != "ping snapshot Projects/app" {
		t.Errorf("calls = %v", calls)
	}
	if got := c.log(); strings.Contains(got, "busy") {
		t.Errorf("a busy folder was logged: %q", got)
	}
	// Free again: saved at the next look, a Quiet later.
	c.back.busy = false
	if calls := c.at(Quiet); strings.Join(calls, " ") != "snapshot Projects/app" {
		t.Errorf("after it was free: %v", calls)
	}
}

func TestUnreachableServer(t *testing.T) {
	c := newClock(t)
	c.back.down = true
	c.back.print["Projects/app"] = "a2"
	c.back.print["Projects/web"] = "w2"
	calls := c.at(Poll + Quiet + tick)
	if strings.Join(calls, " ") != "ping" {
		t.Errorf("with the server away: %v, want one ping and no snapshot", calls)
	}
	if got := c.log(); !strings.Contains(got, "the server is unreachable, trying again in 5 min: ssh: connect timed out") {
		t.Errorf("log = %q", got)
	}
	// Tried again after 5, then 10 more minutes; nothing in between.
	if calls := c.at(FirstRetry - 2*tick); len(calls) != 0 {
		t.Errorf("before the retry: %v", calls)
	}
	if calls := c.at(2 * tick); strings.Join(calls, " ") != "ping" {
		t.Errorf("at the retry: %v", calls)
	}
	if got := c.log(); !strings.Contains(got, "trying again in 10 min") {
		t.Errorf("log = %q", got)
	}
	for range 5 {
		c.at(MaxRetry)
	}
	if got := c.log(); !strings.HasSuffix(got, "trying again in 30 min: ssh: connect timed out\n") || strings.Contains(got, "60 min") {
		t.Errorf("the waits do not stop at 30 min: %q", got)
	}

	// Back: both folders are saved at the next try.
	c.back.down = false
	calls = c.at(MaxRetry)
	if strings.Join(calls, " ") != "ping snapshot Projects/app snapshot Projects/web" {
		t.Errorf("once it is back: %v", calls)
	}
	if got := c.log(); !strings.HasPrefix(got, "the server is reachable again\n") {
		t.Errorf("log = %q", got)
	}
}

func TestFailureLoggedOnce(t *testing.T) {
	c := newClock(t)
	c.back.print["Projects/app"] = "a2"
	c.back.fail = errors.New("restic backup: permission denied")
	c.at(10 * time.Minute)
	if got := c.log(); strings.Count(got, "permission denied") != 1 {
		t.Errorf("a failure that repeats is logged more than once:\n%s", got)
	}
	// Tried every Poll meanwhile; after each failure the server is asked
	// whether it is still there.
	if calls := c.at(Poll); strings.Join(calls, " ") != "ping snapshot Projects/app ping" {
		t.Errorf("calls = %v", calls)
	}
	c.back.fail = nil
	if calls := c.at(Poll); strings.Join(calls, " ") != "ping snapshot Projects/app" {
		t.Errorf("calls = %v", calls)
	}
}

func TestNewFolderPickedUp(t *testing.T) {
	c := newClock(t)
	c.back.folders = append(c.back.folders, config.Folder{Root: "notes", Path: "journal"})
	c.back.print["notes/journal"] = "n1" // never saved
	calls := c.at(Poll + Quiet + tick)
	if strings.Join(calls, " ") != "ping snapshot notes/journal" {
		t.Errorf("a folder added while running: %v", calls)
	}
}
