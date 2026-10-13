package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/muesli/termenv"

	"github.com/ahaltindis/wipsync/internal/app"
	"github.com/ahaltindis/wipsync/internal/config"
	"github.com/ahaltindis/wipsync/internal/restic"
)

func init() {
	// No colour codes, so the tests read the screen as plain text.
	lipgloss.SetColorProfile(termenv.Ascii)
}

func snap(id, parent, host string, minutesAgo int) restic.Snapshot {
	return restic.Snapshot{
		ID: strings.Repeat(id, 64/len(id)), Parent: parent, Host: host,
		Time: time.Date(2026, 10, 13, 12, 0, 0, 0, time.Local).Add(-time.Duration(minutesAgo) * time.Minute),
	}
}

func sampleWorld() app.World {
	b1, b2 := snap("b1", "", "desktop", 90), snap("b2", strings.Repeat("b1", 32), "laptop", 30)
	folder := func(root, path, dir string, s app.Status, base, master string, snaps ...restic.Snapshot) app.FolderState {
		return app.FolderState{Folder: config.Folder{Root: root, Path: path}, Dir: dir, Status: s,
			Base: base, Master: master, Snapshots: snaps}
	}
	return app.World{Folders: []app.FolderState{
		folder("Projects", "aha/archype", "/home/u/Projects/aha/archype", app.InStep, b1.ID, b1.ID, b1),
		folder("Projects", "aha/website", "", app.NotHere, "", b1.ID, b1),
		folder("Projects", "aha/wipsync", "/home/u/Projects/aha/wipsync", app.Changed, b1.ID, b1.ID, b1),
		folder("Projects", "Freelance/kadir/boxing-machine", "/home/u/Projects/Freelance/kadir/boxing-machine", app.Behind, b1.ID, b2.ID, b2, b1),
		folder("notes", "journal", "/home/u/notes/journal", app.Ahead, b2.ID, b1.ID, b2, b1),
		folder("notes", "recipes", "/home/u/notes/recipes", app.Diverged, b1.ID, b2.ID, b2, b1),
	}}
}

// started returns a model that has its size and has read the sample world.
func started(t *testing.T, w, h int) Model {
	t.Helper()
	m := Model{client: "laptop", repository: "sftp:u@host:/srv/repo", theme: DefaultTheme(), loading: true,
		survey: func(context.Context) (app.World, error) { return sampleWorld(), nil }}
	return send(t, m, tea.WindowSizeMsg{Width: w, Height: h}, m.Init()())
}

func send(t *testing.T, m Model, msgs ...tea.Msg) Model {
	t.Helper()
	for _, msg := range msgs {
		next, _ := m.Update(msg)
		m = next.(Model)
	}
	return m
}

func keys(t *testing.T, m Model, typed ...string) Model {
	t.Helper()
	for _, k := range typed {
		msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		switch k {
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		}
		m = send(t, m, msg)
	}
	return m
}

func TestScreen(t *testing.T) {
	m := started(t, 100, 16)
	screen := m.View()
	t.Logf("the screen at 100x16:\n%s", screen)

	lines := strings.Split(screen, "\n")
	if len(lines) != 16 {
		t.Errorf("%d lines, want 16", len(lines))
	}
	// The top and bottom lines stand apart from the panes.
	for _, i := range []int{1, 14} {
		if strings.TrimSpace(lines[i]) != "" {
			t.Errorf("line %d should be empty: %q", i, lines[i])
		}
	}
	for i, line := range lines {
		if w := runewidth.StringWidth(line); w != 100 {
			t.Errorf("line %d is %d cells wide, want 100: %q", i, w, line)
		}
	}
	for _, want := range []string{
		"wipsync  laptop  sftp:u@host:/srv/repo",
		" Projects", " notes",
		"Freelance/kadir/boxing-machi… behind", "aha/wipsync", "changed",
		"aha/website", "not here", "journal", "ahead", "both changed",
		// The first folder is selected, and its log is on the right.
		"Projects/aha/archype",
		"/home/u/Projects/aha/archype",
		"in step with master",
		"b1b1b1b1  2026-10-13 10:30  desktop    master  base",
		"j/k move  l log    s snapshot  P push  p pull  r restore    R refresh  q quit",
	} {
		if !strings.Contains(screen, want) {
			t.Errorf("the screen lacks %q", want)
		}
	}
	// Folders are sorted by path under their root, roots by name.
	order := []string{" Projects", "aha/archype ", "aha/website", "aha/wipsync", "Freelance/kadir", " notes", "journal", "recipes"}
	at := 0
	for _, name := range order {
		i := strings.Index(screen[at:], name)
		if i < 0 {
			t.Fatalf("%q is missing or out of order", name)
		}
		at += i
	}
}

func TestMoving(t *testing.T) {
	m := started(t, 100, 16)
	if got := m.selected().name(); got != "Projects/aha/archype" {
		t.Fatalf("starts on %q", got)
	}
	m = keys(t, m, "j", "j", "j")
	if got := m.selected().name(); got != "Projects/Freelance/kadir/boxing-machine" {
		t.Errorf("after j j j: %q", got)
	}
	if screen := m.View(); !strings.Contains(screen, "behind master: p to pull") ||
		!strings.Contains(screen, "b2b2b2b2  2026-10-13 11:30  [laptop]   master") ||
		!strings.Contains(screen, "b1b1b1b1  2026-10-13 10:30  desktop    base") {
		t.Errorf("the right pane did not follow the selection:\n%s", screen)
	}
	m = keys(t, m, "k", "k", "k", "k", "k", "k")
	if got := m.selected().name(); got != "Projects" {
		t.Errorf("at the top: %q", got)
	}
	if screen := m.View(); !strings.Contains(screen, "4 folders") || !strings.Contains(screen, "1 behind") || !strings.Contains(screen, "/home/u/Projects") {
		t.Errorf("a root's summary is missing:\n%s", screen)
	}
	m = keys(t, m, "G")
	if got := m.selected().name(); got != "notes/recipes" {
		t.Errorf("after G: %q", got)
	}
	m = keys(t, m, "j", "down")
	if got := m.selected().name(); got != "notes/recipes" {
		t.Errorf("past the end: %q", got)
	}
	m = keys(t, m, "g")
	if got := m.selected().name(); got != "Projects" {
		t.Errorf("after g: %q", got)
	}
}

func TestPanes(t *testing.T) {
	m := started(t, 100, 16)
	m = keys(t, m, "j", "j", "j", "l") // the folder with two snapshots
	if m.focus != log {
		t.Fatal("l did not move to the log")
	}
	if m = keys(t, m, "l"); m.focus != log {
		t.Error("a second l left the log")
	}
	m = keys(t, m, "j", "j", "j")
	if m.logSel != 1 {
		t.Errorf("log selection = %d, want 1 (the last of two)", m.logSel)
	}
	if got := m.selected().name(); got != "Projects/Freelance/kadir/boxing-machine" {
		t.Errorf("moving in the log changed the folder to %q", got)
	}
	m = keys(t, m, "h", "k")
	if m.focus != folders || m.logSel != 0 {
		t.Errorf("focus = %d, log selection = %d after going back and moving", m.focus, m.logSel)
	}
	if m = keys(t, m, "h"); m.focus != folders {
		t.Error("a second h left the folder list")
	}
	// A root has no log to go into.
	m = keys(t, m, "g", "l")
	if m.focus != folders {
		t.Error("l went into the log of a root")
	}
}

// The bottom line offers only what applies to the selection, and only
// those keys do anything.
func TestHints(t *testing.T) {
	m := started(t, 120, 16)
	bottom := func(m Model) string {
		lines := strings.Split(m.View(), "\n")
		return strings.TrimSpace(lines[len(lines)-1])
	}
	cases := []struct {
		name  string
		typed []string
		want  string
	}{
		{"a folder that is here", nil, "j/k move  l log    s snapshot  P push  p pull  r restore    R refresh  q quit"},
		{"a folder that is not here", []string{"j"}, "j/k move  l log    p pull    R refresh  q quit"},
		{"a root", []string{"g"}, "j/k move    p pull all    R refresh  q quit"},
		{"a snapshot in the log", []string{"j", "l"}, "j/k move  h folders    r restore this snapshot    R refresh  q quit"},
	}
	for _, tc := range cases {
		m = keys(t, m, tc.typed...)
		if got := bottom(m); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
	// In the log, s is not offered and does nothing; r is, and says so.
	if got := bottom(keys(t, m, "s")); strings.Contains(got, "not built") {
		t.Errorf("s did something in the log: %q", got)
	}
	if got := bottom(keys(t, m, "r")); !strings.Contains(got, `"r" (restore this snapshot) is not built yet`) {
		t.Errorf("r in the log: %q", got)
	}
	// Adding belongs to the directory browser, which is not here yet.
	if got := bottom(keys(t, m, "h", "a")); strings.Contains(got, "not built") || strings.Contains(got, " a ") {
		t.Errorf("a is offered or does something: %q", got)
	}
}

func TestScrolling(t *testing.T) {
	m := started(t, 60, 8) // room for four rows of eight
	m = keys(t, m, "G")
	screen := m.View()
	if !strings.Contains(screen, "recipes") || strings.Contains(screen, " Projects") {
		t.Errorf("the list did not scroll to the selection:\n%s", screen)
	}
	m = keys(t, m, "g")
	if screen := m.View(); !strings.Contains(screen, " Projects") {
		t.Errorf("the list did not scroll back:\n%s", screen)
	}
	// Very small and very wide windows do not break it.
	for _, size := range [][2]int{{20, 3}, {10, 2}, {1, 1}, {300, 60}} {
		m = send(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for i, line := range strings.Split(m.View(), "\n") {
			if w := runewidth.StringWidth(line); w > size[0] && size[0] >= 20 {
				t.Errorf("at %dx%d line %d is %d cells wide", size[0], size[1], i, w)
			}
		}
	}
}

func TestRefreshKeepsSelection(t *testing.T) {
	m := started(t, 100, 16)
	m = keys(t, m, "j") // aha/website
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")})
	m = next.(Model)
	if !m.loading || cmd == nil {
		t.Fatal("R did not start a refresh")
	}
	if !strings.Contains(m.View(), "loading…") {
		t.Error("the screen does not say it is loading")
	}
	m = send(t, m, cmd())
	if m.loading || m.selected().name() != "Projects/aha/website" {
		t.Errorf("after the refresh: loading = %v, selected = %q", m.loading, m.selected().name())
	}
}

func TestErrorsAndNotes(t *testing.T) {
	m := Model{theme: DefaultTheme(), loading: true,
		survey: func(context.Context) (app.World, error) { return app.World{}, errors.New("repository unreachable") }}
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 10}, m.Init()())
	if screen := m.View(); !strings.Contains(screen, "repository unreachable") {
		t.Errorf("the error is not shown:\n%s", screen)
	}

	m = started(t, 100, 16)
	m = keys(t, m, "s")
	if screen := m.View(); !strings.Contains(screen, `"s" (snapshot) is not built yet`) {
		t.Errorf("no word on an unbuilt key:\n%s", screen)
	}
	m = keys(t, m, "j")
	if strings.Contains(m.View(), "not built yet") {
		t.Error("the remark stayed after the next key")
	}
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}); cmd == nil {
		t.Error("q does not quit")
	}

	w := sampleWorld()
	w.MasterErr = errors.New("ssh: connection refused")
	m.survey = func(context.Context) (app.World, error) { return w, nil }
	m = send(t, m, m.load()())
	if screen := m.View(); !strings.Contains(screen, "master is unknown: ssh: connection refused") {
		t.Errorf("the master error is not shown:\n%s", screen)
	}
}

func TestTheme(t *testing.T) {
	dir := t.TempDir()
	missing, err := LoadTheme(filepath.Join(dir, "none.toml"))
	if err != nil || missing != DefaultTheme() {
		t.Errorf("a missing theme file: %+v, %v", missing, err)
	}
	// A file sets some colours and leaves the rest.
	path := filepath.Join(dir, "theme.toml")
	if err := os.WriteFile(path, []byte("behind = \"#ff0000\"\nroot = \"11\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadTheme(path)
	want := DefaultTheme()
	want.Behind, want.Root = "#ff0000", "11"
	if err != nil || got != want {
		t.Errorf("got %+v, %v", got, err)
	}
	// The printed default file reads back as the defaults.
	if err := os.WriteFile(path, []byte(DefaultThemeFile()), 0o644); err != nil {
		t.Fatal(err)
	}
	if back, err := LoadTheme(path); err != nil || back != DefaultTheme() {
		t.Errorf("the default file read back as %+v, %v", back, err)
	}
	if err := os.WriteFile(path, []byte("behind = [1,"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTheme(path); err == nil {
		t.Error("a broken theme file was accepted")
	}
}
