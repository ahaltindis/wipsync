package watch

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/ahaltindis/wipsync/internal/app"
	"github.com/ahaltindis/wipsync/internal/restic"
)

const (
	Check    = 5 * time.Minute // how often the repository is read for news
	WIPAgain = time.Hour       // how soon newer work on another client is told again
)

// item is one piece of news about a folder.
type item struct {
	key    string // the same news has the same key, and is told once
	line   string
	urgent bool
}

// news is what the world has to tell this client: master moves, newer
// work on other clients, and folders that were not there before.
func news(w app.World, client string, known map[string]bool) []item {
	var out []item
	for _, f := range w.Folders {
		name := f.Folder.String()
		if f.Dir == "" {
			if known != nil && !known[name] && len(f.Snapshots) > 0 {
				s := f.Snapshots[0]
				out = append(out, item{key: "new " + name,
					line: fmt.Sprintf("%s: new folder from %s", name, s.Host)})
			}
			continue
		}
		master := find(f.Snapshots, f.Master)
		switch f.Status {
		case app.Behind:
			out = append(out, item{key: "master " + name + " " + f.Master,
				line: fmt.Sprintf("%s: master moved to %s (%s), pull to take it", name, short(f.Master), master.Host)})
		case app.Diverged:
			out = append(out, item{key: "master " + name + " " + f.Master, urgent: true,
				line: fmt.Sprintf("%s: master moved to %s (%s), and this folder changed too", name, short(f.Master), master.Host)})
		}
		if s, ok := newerWork(f, client); ok {
			out = append(out, item{key: "wip " + name,
				line: fmt.Sprintf("%s: newer work on %s (%s, %s)", name, s.Host, short(s.ID),
					app.Age(time.Since(s.Time)))})
		}
	}
	return out
}

// newerWork returns another client's snapshot of a folder when it is the
// latest of all, and is neither master nor what this folder is based on.
func newerWork(f app.FolderState, client string) (restic.Snapshot, bool) {
	if len(f.Snapshots) == 0 {
		return restic.Snapshot{}, false
	}
	s := f.Snapshots[0]
	return s, s.Host != client && s.ID != f.Master && s.ID != f.Base
}

func find(list []restic.Snapshot, id string) restic.Snapshot {
	for _, s := range list {
		if s.ID == id {
			return s
		}
	}
	return restic.Snapshot{}
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// Desktop shows a notification through notify-send.
func Desktop(ctx context.Context, title, body string, urgent bool) error {
	urgency := "normal"
	if urgent {
		urgency = "critical"
	}
	out, err := exec.CommandContext(ctx, "notify-send", "--app-name=wipsync", "--urgency="+urgency, title, body).CombinedOutput()
	if err != nil {
		return fmt.Errorf("notify-send: %w %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
