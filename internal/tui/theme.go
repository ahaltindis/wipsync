package tui

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"

	"github.com/BurntSushi/toml"
	"github.com/charmbracelet/lipgloss"
)

// Theme is every colour the interface uses. A colour is a number from 0 to
// 255, as terminals count them, or "#rrggbb"; "" is the terminal's own
// text colour.
type Theme struct {
	// Rows of the folder list, by what is due for the folder.
	Root     string `toml:"root"`
	InStep   string `toml:"in_step"`
	Changed  string `toml:"changed"`
	Ahead    string `toml:"ahead"`
	Behind   string `toml:"behind"`
	Diverged string `toml:"diverged"`
	NotHere  string `toml:"not_here"`

	// The sign on lines marked with space.
	Marked string `toml:"marked"`

	// Marks in a folder's log.
	Master string `toml:"master"`
	Base   string `toml:"base"`

	// The directory browser.
	Tracked string `toml:"tracked"`
	Dir     string `toml:"dir"`
	File    string `toml:"file"`

	// The frame around it all.
	Title          string `toml:"title"`
	Border         string `toml:"border"`
	Dim            string `toml:"dim"`
	Error          string `toml:"error"`
	SelectedText   string `toml:"selected_text"`
	SelectedBack   string `toml:"selected_back"`
	UnfocusedBack  string `toml:"unfocused_back"`
	SelectedBorder string `toml:"selected_border"`
}

// DefaultTheme is what is used where theme.toml says nothing.
func DefaultTheme() Theme {
	return Theme{
		Root:     "220", // gold
		InStep:   "",
		Changed:  "141", // purple
		Ahead:    "78",  // green
		Behind:   "203", // red
		Diverged: "199", // pink
		NotHere:  "244", // grey

		Marked: "214", // amber

		Master: "220",
		Base:   "75",

		Tracked: "208", // orange
		Dir:     "33",  // blue
		File:    "245", // grey

		Title:          "255",
		Border:         "240",
		Dim:            "244",
		Error:          "203",
		SelectedText:   "232",
		SelectedBack:   "75",
		UnfocusedBack:  "238",
		SelectedBorder: "75",
	}
}

// LoadTheme reads the theme file, which may set any of the colours. A
// missing file gives the defaults.
func LoadTheme(path string) (Theme, error) {
	t := DefaultTheme()
	if _, err := toml.DecodeFile(path, &t); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return DefaultTheme(), fmt.Errorf("read theme: %w", err)
	}
	return t, nil
}

// DefaultThemeFile is the default theme as a file to start from.
func DefaultThemeFile() string {
	var b bytes.Buffer
	b.WriteString("# wipsync theme. A colour is a number from 0 to 255, as terminals count\n" +
		"# them, or \"#rrggbb\". \"\" is the terminal's own text colour.\n" +
		"# Any line can be left out; the default then applies.\n\n")
	toml.NewEncoder(&b).Encode(DefaultTheme())
	return b.String()
}

// fg is a style that writes in the colour, or in the terminal's own if it
// is "".
func fg(colour string) lipgloss.Style {
	s := lipgloss.NewStyle()
	if colour != "" {
		s = s.Foreground(lipgloss.Color(colour))
	}
	return s
}
