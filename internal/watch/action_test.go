package watch

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ahaltindis/wipsync/internal/app"
)

func TestDo(t *testing.T) {
	c, sent := newsClock(t)
	c.at(0) // the first check
	var shown []Status
	c.w.Show = func(st Status) { shown = append(shown, st) }
	var during Status
	c.back.during = func() { during = shown[len(shown)-1] }
	c.back.all = app.AllResult{Verb: "push", Done: 1,
		Lines:  []string{"Projects/app: master is now 55555555", "Projects/web: push rejected"},
		Failed: []string{"Projects/web: push rejected"}}
	c.back.calls, c.back.surveys = nil, 0

	c.w.Do(context.Background(), c.now, PushAll)
	if strings.Join(c.back.calls, " ") != "push all" {
		t.Errorf("calls = %v", c.back.calls)
	}
	if !during.Acting || during.State != Snapshotting || during.Detail != "running push all" {
		t.Errorf("while it ran: %+v", during)
	}
	if last := shown[len(shown)-1]; last.Acting {
		t.Errorf("still acting after: %+v", last)
	}
	logs := c.log()
	for _, want := range []string{"Projects/app: master is now 55555555\n", "Projects/web: push rejected\n",
		"push all: pushed 1 of 2 folders, 1 failed\n"} {
		if !strings.Contains(logs, want) {
			t.Errorf("logs lack %q:\n%s", want, logs)
		}
	}
	want := told{"wipsync: push all", "pushed 1 of 2 folders, 1 failed\n\nProjects/web: push rejected", true}
	if len(*sent) != 1 || (*sent)[0] != want {
		t.Errorf("told %+v, want %+v", *sent, want)
	}

	// The repository is read again at once, so the icon catches up.
	c.at(0)
	if c.back.surveys != 1 {
		t.Errorf("%d surveys after the action", c.back.surveys)
	}
}

func TestDoWithoutFailures(t *testing.T) {
	c, sent := newsClock(t)
	c.at(0)
	c.back.all = app.AllResult{Verb: "pull"}
	c.w.Do(context.Background(), c.now, PullAll)
	if want := (told{"wipsync: pull all", "nothing to pull", false}); len(*sent) != 1 || (*sent)[0] != want {
		t.Errorf("told %+v, want %+v", *sent, want)
	}
}

func TestDoFails(t *testing.T) {
	c, sent := newsClock(t)
	c.at(0)
	c.back.allErr = errors.New("cannot read the masters: connection refused")
	c.w.Do(context.Background(), c.now, PullAll)
	if !strings.Contains(c.log(), "cannot pull all: cannot read the masters: connection refused") {
		t.Error("the failure is not logged")
	}
	if len(*sent) != 1 || !(*sent)[0].urgent || (*sent)[0].body != "cannot read the masters: connection refused" {
		t.Errorf("told %+v", *sent)
	}
}

// Run takes what is asked between its rounds.
func TestRunTakesAsks(t *testing.T) {
	c := newClock(t)
	asks := make(chan Request, 1)
	c.w.Asks = asks
	ctx, cancel := context.WithCancel(context.Background())
	c.back.during = cancel
	c.back.all = app.AllResult{Verb: "pull", Done: 1}
	reply := make(chan Reply, 1)
	asks <- Request{PullAll, reply}
	if err := c.w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(c.back.calls, " "), "pull all") {
		t.Errorf("calls = %v", c.back.calls)
	}
	if r := <-reply; r.Err != nil || r.Result.Done != 1 {
		t.Errorf("reply = %+v", r)
	}
}
