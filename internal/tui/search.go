package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// search is a query being typed after "/". The selection follows it to the
// first line that matches.
type search struct {
	query string
	from  int // the line that was selected before, to go back to on esc
}

// names are the lines of the left pane as they are searched: a folder by
// its root and path, a root by its name, a directory entry by its name.
func (m Model) names() []string {
	if m.view == files {
		names := make([]string, len(m.br.entries))
		for i, e := range m.br.entries {
			names[i] = e.name
		}
		return names
	}
	names := make([]string, len(m.rows))
	for i, r := range m.rows {
		names[i] = r.name()
	}
	return names
}

// line is the selected line of the left pane.
func (m Model) line() int {
	if m.view == files {
		return m.br.sel
	}
	return m.sel
}

// goLine selects a line of the left pane.
func (m *Model) goLine(i int) {
	if m.view == files {
		m.br.sel = i
		m.look()
		return
	}
	if i != m.sel {
		m.sel, m.logSel, m.logTop = i, 0, 0
	}
	m.scroll()
}

// find returns the first line at or after start, going by step and around
// the end, whose name contains the query without regard to case; -1 if
// there is none.
func (m Model) find(query string, start, step int) int {
	names := m.names()
	if query == "" || len(names) == 0 {
		return -1
	}
	query = strings.ToLower(query)
	for n, i := 0, start; n < len(names); n, i = n+1, i+step {
		i = (i%len(names) + len(names)) % len(names)
		if strings.Contains(strings.ToLower(names[i]), query) {
			return i
		}
	}
	return -1
}

// searchKey handles a key while a query is being typed.
func (m Model) searchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := *m.search
	switch msg.Type {
	case tea.KeyEsc:
		m.goLine(s.from)
		m.search = nil
		return m, nil
	case tea.KeyEnter:
		m.lastQuery = s.query
		m.search = nil
		return m, nil
	case tea.KeyBackspace:
		if r := []rune(s.query); len(r) > 0 {
			s.query = string(r[:len(r)-1])
		}
	case tea.KeyRunes, tea.KeySpace:
		s.query += string(msg.Runes)
	default:
		return m, nil
	}
	m.search = &s
	// The first match from the top; with none, or nothing typed, the line
	// the search started on.
	if i := m.find(s.query, 0, 1); i >= 0 {
		m.goLine(i)
	} else {
		m.goLine(s.from)
	}
	return m, nil
}

// again moves to the next match of the last query, or the one before.
func (m *Model) again(step int) {
	if i := m.find(m.lastQuery, m.line()+step, step); i >= 0 {
		m.goLine(i)
	} else {
		m.note = "nothing matches " + m.lastQuery
	}
}
