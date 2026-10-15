package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// dialog is a box over the panes that takes every key until it is closed.
// It is one of four kinds: a menu of keys, a list to choose from, a yes or
// no question, or a line to type; a message is a question with one answer.
type dialog struct {
	title string
	text  string
	lines []string // drawn as they are, under the text

	menu    []option // each chosen by its own key
	choices []string // chosen with j, k and enter
	labels  []string // how the choices are shown, if not as they are
	sel     int
	typing  bool   // a line is being typed
	typed   string // what was typed so far
	preset  string // what an empty line stands for
	problem string // why the last answer would not do
	confirm bool   // a yes or no question

	// done is given the answer: the menu key, the choice, the typed line,
	// or "yes". A closed dialog gives none.
	done func(m Model, answer string) (tea.Model, tea.Cmd)
}

type option struct {
	key, label string
}

// message is a dialog that only tells something.
func message(title, text string) *dialog {
	return &dialog{title: title, text: text}
}

// key handles a key while the dialog is open.
func (d *dialog) key(m Model, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	answer := func(a string) (tea.Model, tea.Cmd) {
		m.dialog = nil
		if d.done == nil {
			return m, nil
		}
		return d.done(m, a)
	}
	k := msg.String()
	if k == "esc" {
		m.dialog = nil
		return m, nil
	}
	switch {
	case d.typing:
		switch msg.Type {
		case tea.KeyEnter:
			line := strings.TrimSpace(d.typed)
			if line == "" {
				line = d.preset
			}
			return answer(line)
		case tea.KeyBackspace:
			if r := []rune(d.typed); len(r) > 0 {
				d.typed = string(r[:len(r)-1])
			}
		case tea.KeyRunes, tea.KeySpace:
			d.typed += string(msg.Runes)
		}

	case d.menu != nil:
		for _, o := range d.menu {
			if o.key == k {
				return answer(k)
			}
		}
		if k == "q" {
			m.dialog = nil
		}

	case d.choices != nil:
		switch k {
		case "j", "down":
			d.sel = min(d.sel+1, len(d.choices)-1)
		case "k", "up":
			d.sel = max(d.sel-1, 0)
		case "enter", "l":
			return answer(d.choices[d.sel])
		case "q", "h":
			m.dialog = nil
		}

	case d.confirm:
		switch k {
		case "y", "enter":
			return answer("yes")
		case "n", "q":
			m.dialog = nil
		}

	default: // a message: any key closes it
		m.dialog = nil
	}
	return m, nil
}

// box draws the dialog, at most width cells wide.
func (d *dialog) box(m Model, width int) []string {
	inner := max(10, min(64, width-6))
	wrap := lipgloss.NewStyle().Width(inner)
	dim := fg(m.theme.Dim)

	var body []string
	body = append(body, fg(m.theme.Title).Bold(true).Render(ansi.Truncate(d.title, inner, "…")))
	if d.text != "" {
		body = append(body, "", wrap.Render(d.text))
	}
	if d.lines != nil {
		body = append(body, "")
		for _, l := range d.lines {
			body = append(body, ansi.Truncate(l, inner, "…"))
		}
	}
	hint := "any key to close"
	switch {
	case d.typing:
		shown := d.typed
		if shown == "" {
			shown = dim.Render(d.preset)
		}
		body = append(body, "", "> "+shown+"█")
		if d.problem != "" {
			body = append(body, fg(m.theme.Error).Render(wrap.Render(d.problem)))
		}
		hint = "enter accept   esc cancel"
	case d.menu != nil:
		body = append(body, "")
		for _, o := range d.menu {
			body = append(body, fg(m.theme.Master).Bold(true).Render(o.key)+"  "+o.label)
		}
		hint = "esc cancel"
	case d.choices != nil:
		body = append(body, "")
		for i, c := range d.choices {
			if d.labels != nil {
				c = d.labels[i]
			}
			line := "  " + ansi.Truncate(c, inner-2, "…")
			if i == d.sel {
				line = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(m.theme.SelectedText)).
					Background(lipgloss.Color(m.theme.SelectedBack)).Width(inner).Render(line)
			}
			body = append(body, line)
		}
		hint = "j/k move   enter choose   esc cancel"
	case d.confirm:
		hint = "y yes   n no"
	}
	body = append(body, "", dim.Render(hint))

	framed := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(m.theme.SelectedBorder)).Padding(0, 1).Width(inner + 2).
		Render(strings.Join(body, "\n"))
	return strings.Split(framed, "\n")
}

// overlay draws the box in the middle of the lines, over what is there.
func overlay(lines, box []string, width int) []string {
	if len(box) == 0 || len(lines) == 0 {
		return lines
	}
	boxWidth := lipgloss.Width(box[0])
	x := max(0, (width-boxWidth)/2)
	y := max(0, (len(lines)-len(box))/2)
	out := append([]string(nil), lines...)
	for i, b := range box {
		if y+i >= len(out) {
			break
		}
		under := out[y+i]
		left := ansi.Truncate(under, x, "")
		if pad := x - lipgloss.Width(left); pad > 0 {
			left += strings.Repeat(" ", pad)
		}
		out[y+i] = left + b + ansi.TruncateLeft(under, x+boxWidth, "")
	}
	return out
}
