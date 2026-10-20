package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
)

// hint is a key in the help, and whether it does anything where the
// selection is now.
type hint struct {
	key, does string
	usable    bool
}

type hintGroup struct {
	title string
	hints []hint
}

// hints are all the keys, in groups, each told as it applies here.
func (m Model) hints() (left, right []hintGroup) {
	acts := m.actions()
	act := func(key, does string) hint {
		for _, a := range acts {
			if a.key == key {
				return hint{key, a.does, m.busy == ""}
			}
		}
		return hint{key, does, false}
	}
	e, _ := m.entry()
	browsing := m.view == files
	inFolders := m.focus == folders

	l := hint{"l", "log", inFolders && !browsing && len(m.selected().snapshots()) > 0}
	h := hint{"h", "folders", m.focus == log}
	tab := hint{"tab", "browse", true}
	if browsing {
		l = hint{"l", "open", e.dir}
		h = hint{"h", "up", true}
		tab.does = "folders"
	}
	left = []hintGroup{
		{"Actions", []hint{
			act("s", "snapshot"),
			act("P", "push"),
			act("p", "pull"),
			act("r", "restore"),
			act("d", "delete"),
			act("a", "add"),
			act("o", "open here"),
			act("O", "open in window"),
		}},
		{"General", []hint{
			{"R", "refresh", !m.loading && m.busy == ""},
			{"?", "help", true},
			{"q", "quit", true},
		}},
	}
	right = []hintGroup{
		{"Move", []hint{
			{"j/k", "down / up", true},
			{"g/G", "top / bottom", true},
			l, h, tab,
		}},
		{"Marks and search", []hint{
			{"space", "mark", inFolders && (!browsing || e.dir)},
			{"esc", "unmark all", m.marked().any()},
			{"/", "search", inFolders},
			{"n/N", "next / previous match", inFolders && m.lastQuery != ""},
			{".", "hidden files", browsing},
		}},
	}
	return left, right
}

// where names what the usable keys apply to, for the top of the help.
func (m Model) where() string {
	if k := m.marked(); k.any() && m.focus == folders {
		return fmt.Sprintf("%d marked", len(k.folders)+len(k.dirs))
	}
	if m.view == files {
		if p := m.path(); p != "" {
			return "Browser · " + short(p)
		}
		return "Browser · " + short(m.br.dir)
	}
	r := m.trackedRow()
	switch {
	case m.focus == log && r.folder != nil && m.logSel < len(r.folder.Snapshots):
		return "Log · " + r.folder.Folder.String() + " · " + r.folder.Snapshots[m.logSel].ID[:8]
	case r.folder != nil:
		return "Folder · " + r.folder.Folder.String()
	case r.root != "":
		return "Root · " + r.root
	}
	return "Folders"
}

// help is the box that lists every key, with those that do nothing here
// dimmed.
func (m Model) help(width int) *dialog {
	inner := max(10, min(64, width-6))
	keyStyle, dim := fg(m.theme.Master).Bold(true), fg(m.theme.Dim)
	column := func(groups []hintGroup, w int) []string {
		var lines []string
		for i, g := range groups {
			if i > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, fg(m.theme.Title).Bold(true).Render(g.title))
			for _, h := range g.hints {
				key := runewidth.FillRight(h.key, 6)
				does := runewidth.Truncate(h.does, max(0, w-6), "…")
				if h.usable {
					lines = append(lines, keyStyle.Render(key)+does)
				} else {
					lines = append(lines, dim.Render(key+does))
				}
			}
		}
		return lines
	}
	left, right := m.hints()
	lines := []string{fg(m.theme.Tracked).Render(ansi.Truncate(m.where(), inner, "…")), ""}
	if inner < 50 {
		// Too narrow for two columns: one under the other.
		lines = append(lines, column(left, inner)...)
		lines = append(lines, "")
		lines = append(lines, column(right, inner)...)
	} else {
		w := (inner - 2) / 2
		l, r := column(left, w), column(right, w)
		for i := range max(len(l), len(r)) {
			var a, b string
			if i < len(l) {
				a = l[i]
			}
			if i < len(r) {
				b = r[i]
			}
			lines = append(lines, a+strings.Repeat(" ", max(0, w+2-ansi.StringWidth(a)))+b)
		}
	}
	return &dialog{title: "Keys", lines: lines}
}
