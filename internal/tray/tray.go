// Package tray shows the watcher's status as an icon in the desktop's tray
// (StatusNotifierItem over D-Bus). A left click runs a command, by default
// one that opens the TUI in a terminal.
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
	log  *log.Logger

	mu    sync.Mutex
	icons map[watch.State][]byte
}

// Start shows the icon until ctx ends. Open is run by sh on a left click.
func Start(ctx context.Context, open string, l *log.Logger) *Tray {
	t := &Tray{open: open, log: l, icons: map[watch.State][]byte{}}
	t.Show(watch.Status{State: watch.UpToDate, Detail: "starting"})
	start, end := systray.RunWithExternalLoop(func() {
		systray.SetTitle("wipsync")
		systray.SetOnTapped(t.click)
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
	t.mu.Unlock()
	systray.SetIcon(png)
	systray.SetTooltip(Tooltip(st))
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
