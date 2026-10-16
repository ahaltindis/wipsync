package cli

import (
	"bytes"
	"context"
	"log"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ahaltindis/wipsync/internal/app"
	"github.com/ahaltindis/wipsync/internal/watch"
)

// The service on one client tells what another client did, read from a
// real repository.
func TestWatchTellsNews(t *testing.T) {
	a := newEnv(t)
	a.repo()
	a.write("Projects/app/main.py", "print(1)\n")
	dirA := filepath.Join(a.home, "Projects/app")
	a.must("\n", "add", dirA)
	a.must("", "snapshot", dirA)
	a.must("", "push", dirA)
	b := a.secondClient("client-b")
	b.must("\n", "pull", "Projects/app")

	var logs bytes.Buffer
	var sent []string
	w := &watch.Watcher{
		Open: func() (watch.Backend, error) { return app.Open(b.cfg) },
		Log:  log.New(&logs, "", 0),
		Notify: func(_ context.Context, title, body string, urgent bool) error {
			sent = append(sent, title+" | "+body)
			return nil
		},
	}
	now := time.Now()
	check := func() string {
		t.Helper()
		b.use()
		sent = nil
		now = now.Add(watch.Check)
		w.Round(context.Background(), now)
		return strings.Join(sent, "\n")
	}
	if got := check(); got != "" {
		t.Errorf("in step, yet told: %s", got)
	}

	// client-a pushes a change: master moved.
	a.write("Projects/app/main.py", "print(2)\n")
	a.must("", "snapshot", dirA)
	a.must("", "push", dirA)
	master := a.snapshots(a.config().Repository)[1].ID
	if got, want := check(), "wipsync: 1 update | Projects/app: master moved to "+master[:8]+" (client-a), pull to take it"; got != want {
		t.Errorf("after a push:\n got %s\nwant %s", got, want)
	}

	// client-a works on without pushing, and adds a folder.
	a.write("Projects/app/main.py", "print(3)\n")
	a.must("", "snapshot", dirA)
	wip := a.snapshots(a.config().Repository)[2].ID
	a.write("Projects/web/index.html", "hi\n")
	dirWeb := filepath.Join(a.home, "Projects/web")
	a.must("", "add", dirWeb)
	a.must("", "snapshot", dirWeb)
	got := check()
	for _, want := range []string{"wipsync: 2 updates | ", "Projects/app: newer work on client-a (" + wip[:8] + ", ", "Projects/web: new folder from client-a"} {
		if !strings.Contains(got, want) {
			t.Errorf("after more work on client-a, %q is not told:\n%s", want, got)
		}
	}
	if !strings.Contains(logs.String(), "Projects/web: new folder from client-a") {
		t.Errorf("the news is not logged:\n%s", logs.String())
	}
}
