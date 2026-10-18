// Package tray shows the watcher's status as an icon in the desktop's tray
// (StatusNotifierItem over D-Bus). A left click runs a command, by default
// one that opens the TUI in a terminal; a right click opens a menu of what
// the watcher can be asked to do.
package tray

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"

	"fyne.io/systray"

	"github.com/ahaltindis/wipsync/internal/watch"
)

// Tray is the icon. Without a session bus or a tray to show it in, it does
// nothing; a tray that appears later picks it up.
type Tray struct {
	open string
	ask  func(watch.Action)
	log  *log.Logger

	mu     sync.Mutex
	icons  map[watch.State][]byte
	items  []*systray.MenuItem // the menu's actions, once it is made
	acting bool                // as last shown: an action runs
}

// actions are the menu's items, in order.
var actions = []struct {
	action  watch.Action
	title   string
	tooltip string
}{
	{watch.PullAll, "Pull all", "Bring master into every folder behind it, and into those not here yet"},
	{watch.PushAll, "Push all", "Make the latest snapshot of every folder that has one master"},
}

// Start shows the icon until ctx ends. Open is run by sh on a left click;
// ask is given what is picked from the menu.
func Start(ctx context.Context, open string, ask func(watch.Action), l *log.Logger) *Tray {
	t := &Tray{open: open, ask: ask, log: l, icons: map[watch.State][]byte{}}
	t.Show(watch.Status{State: watch.UpToDate, Detail: "starting"})
	start, end := systray.RunWithExternalLoop(func() {
		systray.SetTitle("wipsync")
		systray.SetOnTapped(t.click)
		t.menu()
	}, nil)
	start()
	go func() {
		<-ctx.Done()
		end()
	}()
	return t
}

// Show sets the icon and tooltip from the watcher's status.
func (t *Tray) Show(st watch.Status) {
	t.mu.Lock()
	png, ok := t.icons[st.State]
	if !ok {
		png = icon(colours[st.State])
		t.icons[st.State] = png
	}
	t.acting = st.Acting
	t.mu.Unlock()
	systray.SetIcon(png)
	systray.SetTooltip(Tooltip(st))
	t.enable()
}

// menu makes the menu's items and passes on what is clicked.
func (t *Tray) menu() {
	t.mu.Lock()
	for _, a := range actions {
		item := systray.AddMenuItem(a.title, a.tooltip)
		t.items = append(t.items, item)
		go func() {
			for range item.ClickedCh {
				t.ask(a.action)
			}
		}()
	}
	t.mu.Unlock()
	t.enable()
}

// enable greys the menu out while an action runs.
func (t *Tray) enable() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, item := range t.items {
		if t.acting {
			item.Disable()
		} else {
			item.Enable()
		}
	}
}

// Tooltip is the status as text: the state, then the recent events.
func Tooltip(st watch.Status) string {
	lines := []string{"wipsync: " + st.Detail}
	if len(st.Events) > 0 {
		lines = append(lines, "")
	}
	for _, e := range st.Events {
		lines = append(lines, e.At.Local().Format("15:04")+"  "+e.Line)
	}
	return strings.Join(lines, "\n")
}

// click runs the open command. It must start what it opens outside this
// service (systemd-run, uwsm app): what stays in it is stopped with it.
func (t *Tray) click() {
	out, err := exec.Command("sh", "-c", t.open).CombinedOutput()
	if err != nil {
		t.log.Printf("cannot open from the tray: %v %s", err, strings.TrimSpace(string(out)))
	}
}

// DefaultOpen opens the TUI, with the given wipsync binary and config, in a
// terminal of its own.
func DefaultOpen(binary, config string) string {
	return fmt.Sprintf("systemd-run --user --collect --quiet xdg-terminal-exec %s --config %s tui",
		quote(binary), quote(config))
}

// quote makes s one word for sh.
func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
