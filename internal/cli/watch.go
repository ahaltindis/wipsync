package cli

import (
	"fmt"
	"io"
	"log"
	"os"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/ahaltindis/wipsync/internal/app"
	"github.com/ahaltindis/wipsync/internal/tray"
	"github.com/ahaltindis/wipsync/internal/watch"
)

func watchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "watch",
		Short: "Snapshot the folders on this client whenever they change",
		Long: `Snapshot the folders on this client whenever they change, until stopped.

Each folder is looked at every minute; one that changed is snapshotted once
it has been quiet for 30 seconds, or after 5 minutes if it keeps changing.
The server is asked first, and while it does not answer, nothing is sent
and it is asked again after 5 minutes, then less often, up to every 30.
Every 5 minutes it reads the repository, and tells on the desktop (through
notify-send) and in its log what is new: master moved, newer work on
another client, a folder added on another client. Each is told once while
it runs; newer work at most once an hour. It never pulls or pushes.
Folders added to the config are picked up without a restart.

It shows an icon in the desktop's tray, coloured by what it is doing: blue
up to date, purple behind (master moved, or newer work on another client),
yellow snapshotting, red server unreachable. Its tooltip lists the last
things it did; a left click opens the TUI in a terminal. The config's
[tray] section hides it (hide = true) or sets what a click runs (open).

This is what the wipsync user service runs:

  systemctl --user enable --now wipsync`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := cmd.Flags().GetString("config")
			if err != nil {
				return err
			}
			// Read once now, so that a broken config stops it at the start.
			a, err := app.Open(path)
			if err != nil {
				return err
			}
			flags := log.LstdFlags
			if toJournal(cmd.ErrOrStderr()) {
				flags = 0 // the journal adds the time
			}
			w := &watch.Watcher{
				Open: func() (watch.Backend, error) {
					a, err := app.Open(path)
					if err != nil {
						return nil, err
					}
					// Nobody is at a terminal to answer ssh.
					a.NoTerminal = true
					return a, nil
				},
				Log:    log.New(cmd.ErrOrStderr(), "", flags),
				Notify: watch.Desktop,
			}
			if cfg := a.Config.Tray; !cfg.Hide {
				open := cfg.Open
				if open == "" {
					binary, err := os.Executable()
					if err != nil {
						return err
					}
					open = tray.DefaultOpen(binary, a.ConfigPath)
				}
				w.Show = tray.Start(cmd.Context(), open, w.Log).Show
			}
			return w.Run(cmd.Context())
		},
	}
}

// toJournal reports whether w is the stderr that systemd connected to the
// journal: it says so in JOURNAL_STREAM as the stream's device and inode.
func toJournal(w io.Writer) bool {
	f, ok := w.(*os.File)
	stream := os.Getenv("JOURNAL_STREAM")
	if !ok || stream == "" {
		return false
	}
	var st syscall.Stat_t
	if err := syscall.Fstat(int(f.Fd()), &st); err != nil {
		return false
	}
	return stream == fmt.Sprintf("%d:%d", st.Dev, st.Ino)
}
