package tui

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
)

// openedMsg says how opening a directory went.
type openedMsg struct {
	here bool // it ran in this terminal, and is over
	err  error
}

// openDir is the directory o and O open: the one listed in the file system,
// or the selected folder's or root's. It is empty for a folder that is not
// on this client.
func (m Model) openDir() string {
	if m.view == files {
		return m.br.dir
	}
	r := m.trackedRow()
	switch {
	case r.folder != nil:
		return r.folder.Dir
	case r.root != "":
		dir, _ := m.back.RootDir(r.root)
		return dir
	}
	return ""
}

// openActions are o and O, where there is a directory to open.
func (m Model) openActions() []action {
	if m.openDir() == "" {
		return nil
	}
	return []action{{"o", "open here", Model.openHere}, {"O", "open in window", Model.openWindow}}
}

// opener runs a command from the config on a directory.
func opener(command, dir string) *exec.Cmd {
	cmd := exec.Command("sh", "-c", command+` "$@"`, "sh", dir)
	cmd.Dir = dir
	return cmd
}

// openWindow opens the directory with the window command, which goes on
// beside the interface: in a session of its own, away from the terminal.
func (m Model) openWindow() (tea.Model, tea.Cmd) {
	dir := m.openDir()
	cmd := opener(m.open.Window, dir)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		m.dialog = message("Could not open "+short(dir), err.Error())
		return m, nil
	}
	m.note = "opened " + short(dir)
	return m, func() tea.Msg {
		if err := cmd.Wait(); err != nil {
			if said := strings.TrimSpace(out.String()); said != "" {
				err = fmt.Errorf("%w\n%s", err, said)
			}
			return openedMsg{err: err}
		}
		return nil
	}
}

// openHere hands the terminal to the here command until it exits, then
// looks again, as it may have changed what is in the folders.
func (m Model) openHere() (tea.Model, tea.Cmd) {
	return m, tea.ExecProcess(opener(m.open.Here, m.openDir()), func(err error) tea.Msg {
		return openedMsg{here: true, err: err}
	})
}
