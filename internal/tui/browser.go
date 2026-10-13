package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"github.com/ahaltindis/wipsync/internal/app"
)

// browser is the left pane as a view of the file system, for finding
// directories to add.
type browser struct {
	dir     string
	entries []entry
	err     error // why dir could not be read
	sel     int
	top     int
	hidden  bool // whether names starting with a dot are shown

	// What the right pane shows for a plain directory: its contents.
	preview    []entry
	previewErr error
}

type entry struct {
	name string
	dir  bool
	size int64
	mod  time.Time
}

// readDir lists a directory: directories first, then files, each sorted
// without regard to case.
func readDir(dir string, hidden bool) ([]entry, error) {
	list, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	entries := make([]entry, 0, len(list))
	for _, d := range list {
		if !hidden && strings.HasPrefix(d.Name(), ".") {
			continue
		}
		e := entry{name: d.Name(), dir: d.IsDir()}
		// A link to a directory can be entered like one.
		if info, err := os.Stat(filepath.Join(dir, d.Name())); err == nil {
			e.dir, e.size, e.mod = info.IsDir(), info.Size(), info.ModTime()
		}
		entries = append(entries, e)
	}
	slices.SortFunc(entries, func(x, y entry) int {
		if x.dir != y.dir {
			if x.dir {
				return -1
			}
			return 1
		}
		if c := strings.Compare(strings.ToLower(x.name), strings.ToLower(y.name)); c != 0 {
			return c
		}
		return strings.Compare(x.name, y.name)
	})
	return entries, nil
}

// browse shows a directory, with the selection on the entry called name if
// there is one.
func (m *Model) browse(dir, name string) {
	b := &m.br
	b.dir = dir
	b.entries, b.err = readDir(dir, b.hidden)
	b.sel, b.top = 0, 0
	for i, e := range b.entries {
		if e.name == name {
			b.sel = i
		}
	}
	m.look()
}

// look refreshes what depends on the browser's selection.
func (m *Model) look() {
	b := &m.br
	b.preview, b.previewErr = nil, nil
	if e, ok := m.entry(); ok && e.dir {
		b.preview, b.previewErr = readDir(m.path(), b.hidden)
	}
	if visible := m.bodyHeight(); visible > 0 {
		if b.sel < b.top {
			b.top = b.sel
		} else if b.sel >= b.top+visible {
			b.top = b.sel - visible + 1
		}
	}
}

// entry is what the browser's selection is on.
func (m Model) entry() (entry, bool) {
	if m.br.sel < 0 || m.br.sel >= len(m.br.entries) {
		return entry{}, false
	}
	return m.br.entries[m.br.sel], true
}

// path is the full path of the browser's selection.
func (m Model) path() string {
	e, ok := m.entry()
	if !ok {
		return ""
	}
	return filepath.Join(m.br.dir, e.name)
}

// openBrowser switches the left pane to the file system, starting beside
// whatever was selected.
func (m *Model) openBrowser() {
	m.view, m.focus = files, folders
	if m.br.dir != "" {
		m.browse(m.br.dir, filepath.Base(m.path()))
		return
	}
	start, name := "", ""
	r := m.trackedRow()
	switch {
	case r.folder != nil && r.folder.Dir != "":
		start, name = filepath.Dir(r.folder.Dir), filepath.Base(r.folder.Dir)
	case r.root != "":
		if dir, ok := m.back.RootDir(r.root); ok {
			start, name = filepath.Dir(dir), filepath.Base(dir)
		}
	}
	if start == "" {
		start, _ = os.UserHomeDir()
	}
	if start == "" {
		start = "/"
	}
	m.browse(start, name)
}

// browserKey handles a key while the left pane shows the file system. It
// reports whether the key was one of its own.
func (m *Model) browserKey(key string) bool {
	b := &m.br
	switch key {
	case "j", "down":
		b.sel = min(b.sel+1, max(0, len(b.entries)-1))
	case "k", "up":
		b.sel = max(b.sel-1, 0)
	case "g", "home":
		b.sel = 0
	case "G", "end":
		b.sel = max(0, len(b.entries)-1)
	case "l", "right", "enter":
		if e, ok := m.entry(); ok && e.dir {
			m.browse(m.path(), "")
		}
		return true
	case "h", "left":
		if parent := filepath.Dir(b.dir); parent != b.dir {
			m.browse(parent, filepath.Base(b.dir))
		}
		return true
	case ".":
		b.hidden = !b.hidden
		e, _ := m.entry()
		m.browse(b.dir, e.name)
		return true
	default:
		return false
	}
	m.look()
	return true
}

// kind says what a path is to wipsync: a folder's directory, a root's, or
// neither.
func (m Model) kind(path string) (folder *app.FolderState, root string) {
	return m.byDir[path], m.rootAt[path]
}

// entryStyle is the colour of a name in the browser.
func (m Model) entryStyle(path string, e entry) lipgloss.Style {
	folder, root := m.kind(path)
	switch {
	case root != "":
		return fg(m.theme.Root).Bold(true)
	case folder != nil:
		return fg(m.theme.Tracked).Bold(true)
	case e.dir:
		return fg(m.theme.Dir)
	}
	return fg(m.theme.File)
}

// entryLine draws one name of a directory listing, w cells wide.
func (m Model) entryLine(dir string, e entry, w int, selected bool) string {
	path := filepath.Join(dir, e.name)
	name := " " + e.name
	if e.dir {
		name += "/"
	}
	// What the directory is to wipsync, and for a folder what is due.
	folder, root := m.kind(path)
	word, colour := "", ""
	switch {
	case root != "":
		word, colour = "root", m.theme.Root
	case folder != nil:
		word, colour = m.due(folder.Status)
		if word == "" {
			word = "tracked"
			colour = m.theme.Tracked
		}
	}
	room := w
	if word != "" {
		room = w - runewidth.StringWidth(word) - 2
	}
	if selected {
		text := cut(name, room)
		if word != "" {
			text += " " + word + " "
		}
		return m.selection(folders).Render(text)
	}
	line := m.entryStyle(path, e).Render(cut(name, room))
	if word != "" {
		line += " " + fg(colour).Render(word) + " "
	}
	return line
}

// browserLines draws the left pane as the file system.
func (m Model) browserLines(w, h int) []string {
	b := m.br
	lines := make([]string, 0, h)
	switch {
	case b.err != nil:
		lines = append(lines, fg(m.theme.Error).Render(cut(" "+b.err.Error(), w)))
	case len(b.entries) == 0:
		lines = append(lines, fg(m.theme.Dim).Render(cut(" empty", w)))
	}
	for i := b.top; i < len(b.entries) && len(lines) < h; i++ {
		lines = append(lines, m.entryLine(b.dir, b.entries[i], w, i == b.sel))
	}
	for len(lines) < h {
		lines = append(lines, strings.Repeat(" ", w))
	}
	return lines
}

// previewLines draws the right pane for a selection that is not a folder
// or a root: a directory's contents, or what a file is.
func (m Model) previewLines(w, h int) []string {
	lines := make([]string, 0, h)
	add := func(style lipgloss.Style, text string) {
		if len(lines) < h {
			lines = append(lines, style.Render(cut(" "+text, w)))
		}
	}
	e, ok := m.entry()
	switch {
	case !ok:
	case !e.dir:
		add(fg(m.theme.Title).Bold(true), e.name)
		add(fg(m.theme.Dim), short(m.path()))
		add(lipgloss.NewStyle(), "")
		add(fg(m.theme.File), fmt.Sprintf("%s, changed %s", app.FormatBytes(e.size), e.mod.Local().Format("2006-01-02 15:04")))
		add(fg(m.theme.Dim), "a file cannot be added; folders are directories")
	default:
		add(fg(m.theme.Dir).Bold(true), e.name+"/")
		add(fg(m.theme.Dim), short(m.path()))
		add(fg(m.theme.Dim), "not tracked: a to add it")
		add(lipgloss.NewStyle(), "")
		switch {
		case m.br.previewErr != nil:
			add(fg(m.theme.Error), m.br.previewErr.Error())
		case len(m.br.preview) == 0:
			add(fg(m.theme.Dim), "empty")
		}
		for _, child := range m.br.preview {
			if len(lines) >= h {
				break
			}
			lines = append(lines, m.entryLine(m.path(), child, w, false))
		}
	}
	for len(lines) < h {
		lines = append(lines, strings.Repeat(" ", w))
	}
	return lines
}

// short writes a path with the home directory as ~.
func short(path string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "/" {
		if path == home {
			return "~"
		}
		if rest, ok := strings.CutPrefix(path, home+"/"); ok {
			return "~/" + rest
		}
	}
	return path
}
