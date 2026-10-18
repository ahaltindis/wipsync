// Package tui is the terminal interface: the folders on the left, the
// selected one's log on the right, and keys to act on them.
package tui

import (
	"context"
	"slices"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ahaltindis/wipsync/internal/app"
	"github.com/ahaltindis/wipsync/internal/config"
	"github.com/ahaltindis/wipsync/internal/db"
	"github.com/ahaltindis/wipsync/internal/restic"
)

// backend is what the interface asks of the client. *app.App is the real
// one; the tests have their own.
type backend interface {
	Survey(ctx context.Context) (app.World, error)
	Snapshot(ctx context.Context, f db.Folder) (restic.BackupResult, error)
	Push(ctx context.Context, f db.Folder) (app.PushResult, error)
	Pull(ctx context.Context, f db.Folder, from string) (app.PullResult, error)
	FindTarget(ctx context.Context, f db.Folder, from string) (app.Target, error)
	Fetch(ctx context.Context, f db.Folder, rootDir string, t app.Target) (app.PullResult, error)
	Restore(ctx context.Context, f db.Folder, name string) (app.RestoreResult, error)
	PullAll(ctx context.Context, scope app.Scope) (app.AllResult, error)
	PushAll(ctx context.Context, scope app.Scope) (app.AllResult, error)
	RootDir(root string) (string, bool)
	CheckNewRoot(root, dir string) error

	// For adding a directory as a folder.
	Roots() map[string]string
	CheckAddable(dir string) error
	RootAbove(dir string) (name, rootDir string, ok bool)
	RootAt(dir string) (string, bool)
	CheckRootName(name string) string
	Add(dir, rootName, rootDir string) (db.Folder, error)
}

// leftView is what the left pane shows.
type leftView int

const (
	tracked leftView = iota // the folders, under their roots
	files                   // the file system
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
	back       backend
	theme      Theme
	open       config.Open // what o and O run

	world   app.World
	loaded  bool
	loading bool
	err     error  // why the repository could not be read
	note    string // how the last action went, shown in the bottom line
	busy    string // the action that is running, if one is
	dialog  *dialog

	view   leftView
	br     browser
	byDir  map[string]*app.FolderState // the folders on this client, by directory
	rootAt map[string]string           // the roots on this client, by directory

	marks map[string]bool // the marked folders of the folder list, by name

	search    *search // the query being typed, if one is
	lastQuery string  // the last query that was kept, for n and N

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
	open := a.Config.Open
	if open.Window == "" {
		open.Window = config.DefaultOpenWindow
	}
	if open.Here == "" {
		open.Here = config.DefaultOpenHere
	}
	return Model{
		client:     a.Data.Client,
		repository: a.Data.Repository,
		back:       a,
		theme:      theme,
		open:       open,
		loading:    true,
	}
}

type surveyMsg struct {
	world app.World
	err   error
}

func (m Model) load() tea.Cmd {
	return func() tea.Msg {
		world, err := m.back.Survey(context.Background())
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

	case doneMsg:
		// An action finished: say how it went, and look again.
		m.busy = ""
		m.note = msg.note
		if msg.err != nil {
			m.dialog = message("Could not "+msg.what, msg.err.Error())
		}
		m.loading = true
		return m, m.load()

	case openedMsg:
		if msg.err != nil {
			command := m.open.Window
			if msg.here {
				command = m.open.Here
			}
			m.dialog = message("Could not open", command+": "+msg.err.Error())
		}
		if msg.here && !m.loading && m.busy == "" {
			m.loading = true
			return m, m.load()
		}

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if m.dialog != nil {
			return m.dialog.key(m, msg)
		}
		if m.search != nil {
			return m.searchKey(msg)
		}
		m.note = ""
		key := msg.String()
		switch {
		case key == "/" && m.focus == folders:
			m.search = &search{from: m.line()}
			return m, nil
		case (key == "n" || key == "N") && m.focus == folders && m.lastQuery != "":
			if key == "n" {
				m.again(1)
			} else {
				m.again(-1)
			}
			return m, nil
		case key == "q":
			return m, tea.Quit
		case key == "?":
			m.dialog = m.help(m.width)
			return m, nil
		case key == "tab":
			if m.view == tracked {
				m.openBrowser()
			} else {
				m.closeBrowser()
			}
			return m, nil
		case key == "R":
			if !m.loading && m.busy == "" {
				m.loading = true
				return m, m.load()
			}
			return m, nil
		case key == "esc":
			m.unmark()
			return m, nil
		case key == " " && m.focus == folders:
			m.mark()
			return m, nil
		case m.view == files:
			if m.browserKey(key) {
				return m, nil
			}
			return m.act(key)
		}
		switch key {
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
		default:
			return m.act(key)
		}
	}
	return m, nil
}

// act runs the action a key stands for where the selection is, if any.
func (m Model) act(key string) (tea.Model, tea.Cmd) {
	for _, a := range m.actions() {
		if a.key == key && m.busy == "" {
			return a.run(m)
		}
	}
	return m, nil
}

// setWorld takes in a fresh survey, keeping the selection on the same
// folder if it is still there.
func (m *Model) setWorld(w app.World) {
	before := m.trackedRow()
	m.world, m.loaded = w, true
	m.rows = m.rows[:0]
	m.byDir, m.rootAt = map[string]*app.FolderState{}, map[string]string{}
	for name, dir := range m.back.Roots() {
		m.rootAt[dir] = name
	}
	root := ""
	for i := range w.Folders {
		f := &w.Folders[i]
		if f.Folder.Root != root || len(m.rows) == 0 {
			root = f.Folder.Root
			m.rows = append(m.rows, row{root: root})
		}
		m.rows = append(m.rows, row{root: root, folder: f})
		if f.Dir != "" {
			m.byDir[f.Dir] = f
		}
	}
	m.sel = 0
	for i, r := range m.rows {
		// A root and a folder cannot share a name: a folder's has a slash.
		if r.name() == before.name() && r.name() != "" {
			m.sel = i
		}
	}
	if m.sel == 0 && len(m.rows) > 1 && before.name() == "" {
		m.sel = 1 // the first folder, not its root
	}
	if len(m.marks) > 0 {
		m.marks = cloned(m.marks)
		for name := range m.marks {
			if !slices.ContainsFunc(m.rows, func(r row) bool { return r.folder != nil && r.name() == name }) {
				delete(m.marks, name) // the folder is gone
			}
		}
	}
	if m.view == files {
		// The directory may have changed too, and so may what is tracked.
		e, _ := m.entry()
		m.browse(m.br.dir, e.name)
	}
	if n := len(m.trackedRow().snapshots()); m.logSel >= n {
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

// trackedRow is the row the folder list is on; the zero row if it is empty.
func (m Model) trackedRow() row {
	if m.sel < 0 || m.sel >= len(m.rows) {
		return row{}
	}
	return m.rows[m.sel]
}

// selected is the folder or root that is selected, in whichever view the
// left pane shows. In the file system it is the zero row for anything that
// is neither.
func (m Model) selected() row {
	if m.view == tracked {
		return m.trackedRow()
	}
	folder, root := m.kind(m.path())
	switch {
	case folder != nil:
		return row{root: folder.Folder.Root, folder: folder}
	case root != "":
		return row{root: root}
	}
	return row{}
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
	m.br.top = keep(m.br.sel, m.br.top, m.bodyHeight())
}

// bodyHeight is the height of the two panes: all but the top and bottom
// line and the empty line that sets each apart.
func (m Model) bodyHeight() int { return max(0, m.height-4) }
