package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"github.com/ahaltindis/wipsync/internal/app"
)

// logHeader is how many lines the log pane shows above the snapshots.
const logHeader = 4

// cut makes s exactly w cells wide: shortened with an ellipsis, or padded.
func cut(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return runewidth.FillRight(runewidth.Truncate(s, w, "…"), w)
}

func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	left := max(24, min(m.width*2/5, 60))
	if left > m.width-12 {
		left = m.width / 2
	}
	right := m.width - left - 1
	height := m.bodyHeight()

	leftLines, rightLines := m.folderLines(left, height), m.logLines(right, height)
	bar := fg(m.theme.Border).Render("│")
	body := make([]string, height)
	for i := range body {
		body[i] = leftLines[i] + bar + rightLines[i]
	}
	if m.dialog != nil {
		body = overlay(body, m.dialog.box(m, m.width), m.width)
	}
	// An empty line sets the top and bottom lines apart from the panes.
	blank := strings.Repeat(" ", m.width)
	lines := append([]string{m.header(), blank}, body...)
	return strings.Join(append(lines, blank, m.footer()), "\n")
}

func (m Model) header() string {
	title := fg(m.theme.Title).Bold(true).Render(" wipsync")
	rest := fmt.Sprintf("  %s  %s", m.client, m.repository)
	state := ""
	switch {
	case m.busy != "":
		state = m.busy + "… "
	case m.loading:
		state = "loading… "
	}
	room := m.width - lipgloss.Width(title) - runewidth.StringWidth(state)
	return title + fg(m.theme.Dim).Render(cut(rest, room)+state)
}

func (m Model) footer() string {
	// Only what can be done from here is offered.
	keys := " j/k move"
	switch {
	case m.focus == log:
		keys += "  h folders"
	case len(m.selected().snapshots()) > 0:
		keys += "  l log"
	}
	keys += "  "
	for _, a := range m.actions() {
		keys += "  " + a.key + " " + a.does
	}
	keys += "    R refresh  q quit"
	switch {
	case m.err != nil:
		return fg(m.theme.Error).Render(cut(" "+m.err.Error(), m.width))
	case m.note != "":
		return fg(m.theme.Ahead).Render(cut(" "+m.note, m.width))
	case m.world.MasterErr != nil:
		return fg(m.theme.Error).Render(cut(" master is unknown: "+m.world.MasterErr.Error(), m.width))
	}
	return fg(m.theme.Dim).Render(cut(keys, m.width))
}

// selection is how the selected line of a pane is drawn: bright in the
// pane that has the focus, muted in the other.
func (m Model) selection(p pane) lipgloss.Style {
	if m.focus != p {
		return lipgloss.NewStyle().Background(lipgloss.Color(m.theme.UnfocusedBack))
	}
	return lipgloss.NewStyle().Bold(true).
		Foreground(lipgloss.Color(m.theme.SelectedText)).Background(lipgloss.Color(m.theme.SelectedBack))
}

// due is what a folder's line says at its end, and in which colour.
func (m Model) due(s app.Status) (string, string) {
	switch s {
	case app.Changed:
		return "changed", m.theme.Changed
	case app.Ahead:
		return "ahead", m.theme.Ahead
	case app.Behind:
		return "behind", m.theme.Behind
	case app.Diverged:
		return "both changed", m.theme.Diverged
	case app.NotHere:
		return "not here", m.theme.NotHere
	}
	return "", m.theme.InStep
}

// folderLines draws the folder list: each root, and under it its folders.
func (m Model) folderLines(w, h int) []string {
	lines := make([]string, 0, h)
	switch {
	case !m.loaded && m.err == nil:
		lines = append(lines, fg(m.theme.Dim).Render(cut(" reading the repository…", w)))
	case len(m.rows) == 0:
		lines = append(lines, fg(m.theme.Dim).Render(cut(" no folders yet: wipsync add <dir>", w)))
	}
	for i := m.top; i < len(m.rows) && len(lines) < h; i++ {
		r := m.rows[i]
		var text string
		style := fg(m.theme.Root).Bold(true)
		if r.folder == nil {
			text = cut(" "+r.root, w)
		} else {
			word, colour := m.due(r.folder.Status)
			style = fg(colour)
			text = cut("   "+r.folder.Folder.Path, w-runewidth.StringWidth(word)-2) + " " + word + " "
		}
		if i == m.sel {
			style = m.selection(folders)
		}
		lines = append(lines, style.Render(text))
	}
	for len(lines) < h {
		lines = append(lines, strings.Repeat(" ", w))
	}
	return lines
}

// explain says in a sentence what is due for a folder.
func explain(f *app.FolderState) string {
	switch f.Status {
	case app.Changed:
		if f.Base == "" {
			return "never snapshotted on this client: s to snapshot"
		}
		return "changed since its last snapshot: s to snapshot"
	case app.Ahead:
		if f.Master == "" {
			return "no master yet: P to push"
		}
		return "ahead of master: P to push"
	case app.Behind:
		return "behind master: p to pull"
	case app.Diverged:
		return "both this folder and master changed: a pull would be refused"
	case app.NotHere:
		return "not on this client: p to pull"
	}
	return "in step with master"
}

// logLines draws the right pane: what the selected row is, and for a
// folder its snapshots.
func (m Model) logLines(w, h int) []string {
	lines := make([]string, 0, h)
	add := func(style lipgloss.Style, text string) {
		if len(lines) < h {
			lines = append(lines, style.Render(cut(" "+text, w)))
		}
	}
	r := m.selected()
	switch {
	case r.folder == nil && r.root != "":
		counts := map[app.Status]int{}
		inRoot := m.under(r.root)
		where := "not on this client"
		for _, f := range inRoot {
			counts[f.Status]++
			if f.Dir != "" {
				where = strings.TrimSuffix(strings.TrimSuffix(f.Dir, f.Folder.Path), "/")
			}
		}
		add(fg(m.theme.Root).Bold(true), r.root)
		add(fg(m.theme.Dim), where)
		add(lipgloss.NewStyle(), "")
		add(lipgloss.NewStyle(), fmt.Sprintf("%d folders", len(inRoot)))
		for _, s := range []app.Status{app.Behind, app.Diverged, app.Changed, app.Ahead, app.NotHere, app.InStep} {
			if counts[s] > 0 {
				word, colour := m.due(s)
				if word == "" {
					word = "in step"
				}
				add(fg(colour), fmt.Sprintf("  %d %s", counts[s], word))
			}
		}

	case r.folder != nil:
		f := r.folder
		_, colour := m.due(f.Status)
		where := f.Dir
		if where == "" {
			where = "not on this client"
		}
		add(fg(m.theme.Title).Bold(true), f.Folder.String())
		add(fg(m.theme.Dim), where)
		add(fg(colour), explain(f))
		add(lipgloss.NewStyle(), "")
		if len(f.Snapshots) == 0 {
			add(fg(m.theme.Dim), "no snapshots yet")
		}
		hostWidth := 0
		for _, s := range f.Snapshots {
			hostWidth = max(hostWidth, runewidth.StringWidth(s.Host))
		}
		for i := m.logTop; i < len(f.Snapshots) && len(lines) < h; i++ {
			s := f.Snapshots[i]
			host := s.Host
			if host == m.client {
				host = "[" + host + "]"
			}
			text := fmt.Sprintf(" %s  %s  %s", s.ID[:8], s.Time.Local().Format("2006-01-02 15:04"),
				runewidth.FillRight(host, hostWidth+2))
			var marks []string
			if s.ID == f.Master {
				marks = append(marks, fg(m.theme.Master).Bold(true).Render("master"))
			}
			if s.ID == f.Base {
				marks = append(marks, fg(m.theme.Base).Bold(true).Render("base"))
			}
			if i == m.logSel && (m.focus == log) {
				plain := text
				if s.ID == f.Master {
					plain += "  master"
				}
				if s.ID == f.Base {
					plain += "  base"
				}
				lines = append(lines, m.selection(log).Render(cut(plain, w)))
				continue
			}
			line := text
			if len(marks) > 0 {
				line += "  " + strings.Join(marks, "  ")
			}
			// Padded by what it measures on screen, since the marks carry colour codes.
			if pad := w - lipgloss.Width(line); pad > 0 {
				line += strings.Repeat(" ", pad)
			} else if pad < 0 {
				line = cut(text, w)
			}
			lines = append(lines, line)
		}
	}
	for len(lines) < h {
		lines = append(lines, strings.Repeat(" ", w))
	}
	return lines
}
