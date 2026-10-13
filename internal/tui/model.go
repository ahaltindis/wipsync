// Package tui is the terminal interface: the folders on the left, the
// selected one's log on the right.
package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ahaltindis/wipsync/internal/app"
	"github.com/ahaltindis/wipsync/internal/restic"
)

type pane int

const (
	folders pane = iota
	log
)

// row is one line of the folder list: a root, or a folder under one.
type row struct {
	root   string
	folder *app.FolderState // nil for the root's own line
}

// Model is the whole state of the interface.
type Model struct {
	client     string
	repository string
	survey     func(context.Context) (app.World, error)
	theme      Theme

	world   app.World
	loaded  bool
	loading bool
	err     error
	note    string // a passing remark, shown in the bottom line

	rows   []row
	sel    int // selected row of the folder list
	top    int // its first visible row
	logSel int // selected line of the log
	logTop int
	focus  pane

	width, height int
}

// New returns the interface for a client.
func New(a *app.App, theme Theme) Model {
	return Model{
		client:     a.Config.Client,
		repository: a.Config.Repository,
		survey:     a.Survey,
		theme:      theme,
		loading:    true,
	}
}

type surveyMsg struct {
	world app.World
	err   error
}

func (m Model) load() tea.Cmd {
	return func() tea.Msg {
		world, err := m.survey(context.Background())
		return surveyMsg{world, err}
	}
}

func (m Model) Init() tea.Cmd { return m.load() }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.scroll()

	case surveyMsg:
		m.loading = false
		m.err = msg.err
		if msg.err == nil {
			m.setWorld(msg.world)
		}

	case tea.KeyMsg:
		m.note = ""
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "j", "down":
			m.move(1)
		case "k", "up":
			m.move(-1)
		case "g", "home":
			m.move(-len(m.rows) - len(m.selected().snapshots()))
		case "G", "end":
			m.move(len(m.rows) + len(m.selected().snapshots()))
		case "l", "right":
			if len(m.selected().snapshots()) > 0 {
				m.focus = log
			}
		case "h", "left":
			m.focus = folders
		case "R":
			if !m.loading {
				m.loading = true
				return m, m.load()
			}
		default:
			for _, a := range m.actions() {
				if a.key == msg.String() {
					m.note = "\"" + a.key + "\" (" + a.does + ") is not built yet; this version only shows"
				}
			}
		}
	}
	return m, nil
}

// setWorld takes in a fresh survey, keeping the selection on the same
// folder if it is still there.
func (m *Model) setWorld(w app.World) {
	before := m.selected()
	m.world, m.loaded = w, true
	m.rows = m.rows[:0]
	root := ""
	for i := range w.Folders {
		f := &w.Folders[i]
		if f.Folder.Root != root || len(m.rows) == 0 {
			root = f.Folder.Root
			m.rows = append(m.rows, row{root: root})
		}
		m.rows = append(m.rows, row{root: root, folder: f})
	}
	m.sel = 0
	for i, r := range m.rows {
		if r.name() == before.name() && r.name() != "" {
			m.sel = i
		}
	}
	if m.sel == 0 && len(m.rows) > 1 && before.name() == "" {
		m.sel = 1 // the first folder, not its root
	}
	if n := len(m.selected().snapshots()); m.logSel >= n {
		m.logSel, m.logTop = 0, 0
		if n == 0 {
			m.focus = folders
		}
	}
	m.scroll()
}

func (r row) name() string {
	if r.folder != nil {
		return r.folder.Folder.String()
	}
	return r.root
}

// snapshots are the lines of the row's log; a root has none.
func (r row) snapshots() []restic.Snapshot {
	if r.folder == nil {
		return nil
	}
	return r.folder.Snapshots
}

// selected is the row the folder list is on; the zero row if it is empty.
func (m Model) selected() row {
	if m.sel < 0 || m.sel >= len(m.rows) {
		return row{}
	}
	return m.rows[m.sel]
}

// under returns the folders of a root.
func (m Model) under(root string) []*app.FolderState {
	var out []*app.FolderState
	for _, r := range m.rows {
		if r.folder != nil && r.root == root {
			out = append(out, r.folder)
		}
	}
	return out
}

// move shifts the selection in the focused pane, stopping at its ends.
func (m *Model) move(by int) {
	clamp := func(v, n int) int { return max(0, min(v, n-1)) }
	if m.focus == log {
		m.logSel = clamp(m.logSel+by, len(m.selected().snapshots()))
	} else if len(m.rows) > 0 {
		was := m.sel
		m.sel = clamp(m.sel+by, len(m.rows))
		if m.sel != was {
			m.logSel, m.logTop = 0, 0
		}
	}
	m.scroll()
}

// scroll keeps both selections inside what is visible.
func (m *Model) scroll() {
	keep := func(sel, top, visible int) int {
		if visible <= 0 {
			return 0
		}
		if sel < top {
			return sel
		}
		if sel >= top+visible {
			return sel - visible + 1
		}
		return top
	}
	m.top = keep(m.sel, m.top, m.bodyHeight())
	m.logTop = keep(m.logSel, m.logTop, m.bodyHeight()-logHeader)
}

// bodyHeight is the height of the two panes: all but the top and bottom
// line and the empty line that sets each apart.
func (m Model) bodyHeight() int { return max(0, m.height-4) }

// action is something a key does to what is selected.
type action struct {
	key, does string
}

// actions are the keys that apply where the selection is now. Only these
// are offered in the bottom line, and only these do anything.
func (m Model) actions() []action {
	r := m.selected()
	switch {
	case m.focus == log:
		return []action{{"r", "restore this snapshot"}}
	case r.folder == nil && r.root != "":
		return []action{{"p", "pull all"}}
	case r.folder == nil:
		return nil
	case r.folder.Dir == "":
		return []action{{"p", "pull"}}
	}
	return []action{{"s", "snapshot"}, {"P", "push"}, {"p", "pull"}, {"r", "restore"}}
}
