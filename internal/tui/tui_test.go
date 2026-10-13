package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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

// fake stands in for the client: it serves a world and records what it
// is asked to do.
type fake struct {
	world     app.World
	surveyErr error
	fail      map[string]error  // by call, as recorded in calls
	roots     map[string]string // the roots this client knows
	calls     []string
}

func (f *fake) did(call string) error {
	f.calls = append(f.calls, call)
	return f.fail[call]
}

func (f *fake) Survey(context.Context) (app.World, error) { return f.world, f.surveyErr }

func (f *fake) Snapshot(_ context.Context, fo config.Folder) (restic.BackupResult, error) {
	err := f.did("snapshot " + fo.String())
	return restic.BackupResult{SnapshotID: strings.Repeat("5", 64), FilesChanged: 2, BytesStored: 2048}, err
}

func (f *fake) Push(_ context.Context, fo config.Folder) (app.PushResult, error) {
	return app.PushResult{Snapshot: snap("b2", "", "laptop", 30)}, f.did("push " + fo.String())
}

func (f *fake) Pull(_ context.Context, fo config.Folder, from string) (app.PullResult, error) {
	err := f.did("pull " + fo.String() + " from " + from)
	return app.PullResult{Outcome: app.Pulled, Target: snap("b2", "", "desktop", 30), Saved: strings.Repeat("7", 64)}, err
}

func (f *fake) FindTarget(_ context.Context, fo config.Folder, from string) (app.Target, error) {
	return app.Target{Snapshot: snap("b1", "", "desktop", 90)}, f.did("target " + fo.String() + " from " + from)
}

func (f *fake) Fetch(_ context.Context, fo config.Folder, rootDir string, t app.Target) (app.PullResult, error) {
	err := f.did("fetch " + fo.String() + " into " + rootDir)
	return app.PullResult{Outcome: app.Pulled, Target: t.Snapshot, Dir: rootDir + "/" + fo.Path}, err
}

func (f *fake) Restore(_ context.Context, fo config.Folder, name string) (app.RestoreResult, error) {
	err := f.did("restore " + fo.String() + " to " + name[:8])
	return app.RestoreResult{Target: snap("b1", "", "desktop", 90), Undo: strings.Repeat("7", 64)}, err
}

func (f *fake) RootDir(root string) (string, bool) {
	dir, ok := f.roots[root]
	return dir, ok
}

func (f *fake) CheckNewRoot(root, dir string) error {
	return f.fail["root "+root+" at "+dir]
}

func (f *fake) Roots() map[string]string { return f.roots }

func (f *fake) CheckAddable(dir string) error { return f.fail["addable "+dir] }

func (f *fake) RootAbove(dir string) (string, string, bool) {
	for name, rootDir := range f.roots {
		if strings.HasPrefix(dir, rootDir+"/") {
			return name, rootDir, true
		}
	}
	return "", "", false
}

func (f *fake) RootAt(dir string) (string, bool) {
	for name, rootDir := range f.roots {
		if rootDir == dir {
			return name, true
		}
	}
	return "", false
}

func (f *fake) CheckRootName(name string) string {
	if _, taken := f.roots[name]; taken {
		return fmt.Sprintf("root name %q is already used for %s", name, f.roots[name])
	}
	return ""
}

func (f *fake) Add(dir, rootName, rootDir string) (config.Folder, error) {
	err := f.did("add " + dir + " as " + rootName + " at " + rootDir)
	rel, _ := filepath.Rel(rootDir, dir)
	return config.Folder{Root: rootName, Path: rel}, err
}

// started returns a model that has its size and has read the sample world.
func started(t *testing.T, w, h int) (Model, *fake) {
	t.Helper()
	back := &fake{world: sampleWorld(), roots: map[string]string{"Projects": "/home/u/Projects", "notes": "/home/u/notes"}}
	m := Model{client: "laptop", repository: "sftp:u@host:/srv/repo", theme: DefaultTheme(), loading: true, back: back}
	return send(t, m, tea.WindowSizeMsg{Width: w, Height: h}, m.Init()()), back
}

// run presses a key and, if that starts something in the background, lets
// it finish and the screen refresh.
func run(t *testing.T, m Model, key string) Model {
	t.Helper()
	next, cmd := m.Update(keyMsg(key))
	m = next.(Model)
	for cmd != nil {
		msg := cmd()
		if msg == nil {
			break
		}
		next, cmd = m.Update(msg)
		m = next.(Model)
	}
	return m
}

func keyMsg(k string) tea.KeyMsg {
	switch k {
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

// asks is what the open box says, without its line breaks; "" if none is open.
func asks(m Model) string {
	if m.dialog == nil {
		return ""
	}
	return m.dialog.title + " | " + m.dialog.text + " | " + m.dialog.problem
}

func bottom(m Model) string {
	lines := strings.Split(m.View(), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
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
		m = send(t, m, keyMsg(k))
	}
	return m
}

func TestScreen(t *testing.T) {
	m, _ := started(t, 100, 16)
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
		"l log    s snapshot  P push  p pull  r restore    space mark  / search  tab browse",
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
	m, _ := started(t, 100, 16)
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
	m, _ := started(t, 100, 16)
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
	m, _ := started(t, 120, 16)
	cases := []struct {
		name  string
		typed []string
		want  string
	}{
		{"a folder that is here", nil, "l log    s snapshot  P push  p pull  r restore    space mark  / search  tab browse    j/k move  R refresh  q quit"},
		{"a folder that is not here", []string{"j"}, "l log    p pull    space mark  / search  tab browse    j/k move  R refresh  q quit"},
		{"a root", []string{"g"}, "p pull all    space mark  / search  tab browse    j/k move  R refresh  q quit"},
		{"a snapshot in the log", []string{"j", "l"}, "h folders    r restore this snapshot    tab browse    j/k move  R refresh  q quit"},
	}
	for _, tc := range cases {
		m = keys(t, m, tc.typed...)
		if got := bottom(m); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
	// A key that is not offered does nothing: s in the log, a anywhere.
	_, back := started(t, 120, 16)
	m = keys(t, m, "s", "a", "h", "a")
	if m.dialog != nil || m.busy != "" || len(back.calls) != 0 {
		t.Errorf("an unoffered key did something: dialog %v, busy %q", m.dialog != nil, m.busy)
	}
}

func TestScrolling(t *testing.T) {
	m, _ := started(t, 60, 8) // room for four rows of eight
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
	m, _ := started(t, 100, 16)
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

func TestErrors(t *testing.T) {
	back := &fake{surveyErr: errors.New("repository unreachable")}
	m := Model{theme: DefaultTheme(), loading: true, back: back}
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 10}, m.Init()())
	if screen := m.View(); !strings.Contains(screen, "repository unreachable") {
		t.Errorf("the error is not shown:\n%s", screen)
	}
	if _, cmd := m.Update(keyMsg("q")); cmd == nil {
		t.Error("q does not quit")
	}

	m, back = started(t, 100, 16)
	back.world.MasterErr = errors.New("ssh: connection refused")
	m = send(t, m, m.load()())
	if screen := m.View(); !strings.Contains(screen, "master is unknown: ssh: connection refused") {
		t.Errorf("the master error is not shown:\n%s", screen)
	}
}

// Snapshot and push run at once, and say how they went.
func TestSnapshotAndPush(t *testing.T) {
	m, back := started(t, 120, 16)
	m = keys(t, m, "j", "j") // aha/wipsync, which is here

	next, cmd := m.Update(keyMsg("s"))
	m = next.(Model)
	if m.busy != "snapshotting Projects/aha/wipsync" || !strings.Contains(m.View(), "snapshotting Projects/aha/wipsync… ") {
		t.Errorf("the screen does not say what is running: busy = %q", m.busy)
	}
	// While it runs, another action is not started.
	if again, more := m.Update(keyMsg("P")); more != nil || again.(Model).busy != m.busy {
		t.Error("a second action started while the first was running")
	}
	next, refresh := m.Update(cmd())
	m = next.(Model)
	if m.busy != "" || refresh == nil || !m.loading {
		t.Errorf("after the snapshot: busy = %q, loading = %v", m.busy, m.loading)
	}
	if got := bottom(m); got != "Projects/aha/wipsync: snapshot 55555555, 0 new, 2 changed, 2.0 KiB stored" {
		t.Errorf("bottom line = %q", got)
	}
	m = send(t, m, refresh())

	m = run(t, m, "P")
	if got := bottom(m); !strings.HasPrefix(got, "Projects/aha/wipsync: master is now b2b2b2b2 (laptop, ") {
		t.Errorf("bottom line = %q", got)
	}
	if want := []string{"snapshot Projects/aha/wipsync", "push Projects/aha/wipsync"}; !slices.Equal(back.calls, want) {
		t.Errorf("calls = %v, want %v", back.calls, want)
	}
	// The note goes with the next key.
	if got := bottom(keys(t, m, "k")); strings.Contains(got, "master is now") {
		t.Errorf("the note stayed: %q", got)
	}
}

// A failure is shown in a box with the whole message, until a key is pressed.
func TestFailureIsShownInFull(t *testing.T) {
	m, back := started(t, 100, 20)
	long := "Projects/aha/archype: push rejected: another client moved master to 9b6e802d; pull first"
	back.fail = map[string]error{"push Projects/aha/archype": errors.New(long)}
	m = run(t, m, "P")
	screen := m.View()
	t.Logf("a failure:\n%s", screen)
	if !strings.Contains(screen, "Could not push Projects/aha/archype") || !strings.Contains(screen, "any key to close") ||
		!strings.Contains(asks(m), long) || !strings.Contains(screen, "to 9b6e802d; pull first") {
		t.Errorf("the failure is not shown in a box, in full:\n%s", screen)
	}
	// The box sits over the panes: every line is still as wide as the window.
	for i, line := range strings.Split(screen, "\n") {
		if w := runewidth.StringWidth(line); w != 100 {
			t.Errorf("line %d is %d cells wide with the box open", i, w)
		}
	}
	m = keys(t, m, "j")
	if m.dialog != nil || m.selected().name() != "Projects/aha/archype" {
		t.Error("the key that closed the box also moved the selection, or did not close it")
	}
}

func TestPullMaster(t *testing.T) {
	m, back := started(t, 100, 20)
	m = keys(t, m, "j", "j", "j") // Freelance/kadir/boxing-machine, which is behind
	const name = "Projects/Freelance/kadir/boxing-machine"

	m = keys(t, m, "p")
	screen := m.View()
	t.Logf("the pull menu:\n%s", screen)
	for _, want := range []string{"Pull " + name, "m  master", "c  a client's latest snapshot", "esc cancel"} {
		if !strings.Contains(screen, want) {
			t.Errorf("the menu lacks %q", want)
		}
	}
	// Keys go to the box, not to the panes behind it.
	if moved := keys(t, m, "j"); moved.selected().name() != name || moved.dialog == nil {
		t.Error("a key went past the open menu")
	}

	m = keys(t, m, "m")
	screen = m.View()
	if !strings.Contains(screen, "Pull "+name+"?") || !strings.Contains(screen, "y yes   n no") ||
		!strings.Contains(asks(m), "Takes master into /home/u/Projects/Freelance/kadir/boxing-machine, if it is ahead") ||
		!strings.Contains(asks(m), "saved as a snapshot first") {
		t.Errorf("no confirmation:\n%s", screen)
	}
	// No means no.
	if no := keys(t, m, "n"); no.dialog != nil || no.busy != "" || len(back.calls) != 0 {
		t.Error("n did not just close the question")
	}
	m = run(t, m, "y")
	if want := []string{"pull " + name + " from "}; !slices.Equal(back.calls, want) {
		t.Errorf("calls = %v, want %v", back.calls, want)
	}
	if !strings.HasPrefix(m.note, "pulled "+name+": b2b2b2b2 (desktop, ") || !strings.HasSuffix(m.note, "; state before saved as 77777777") {
		t.Errorf("note = %q", m.note)
	}

	// Esc at any step cancels.
	m = keys(t, m, "p", "esc")
	if m.dialog != nil {
		t.Error("esc did not close the menu")
	}
	m = keys(t, m, "p", "m", "esc")
	if m.dialog != nil || len(back.calls) != 1 {
		t.Error("esc did not cancel the confirmation")
	}
}

func TestPullFromClient(t *testing.T) {
	m, back := started(t, 100, 20)
	m = keys(t, m, "G") // notes/recipes, with snapshots from desktop and laptop
	m = keys(t, m, "p", "c")
	screen := m.View()
	t.Logf("choosing a client:\n%s", screen)
	// This client is not offered: its own latest is what the folder has.
	if !strings.Contains(screen, "from which client?") || !slices.Equal(m.dialog.choices, []string{"desktop"}) {
		t.Errorf("unexpected client list %v:\n%s", m.dialog.choices, screen)
	}
	m = keys(t, m, "enter")
	if !strings.Contains(asks(m), "Takes the latest snapshot of desktop into /home/u/notes/recipes") {
		t.Errorf("the confirmation does not name the client: %s", asks(m))
	}
	m = run(t, m, "y")
	if want := []string{"pull notes/recipes from desktop"}; !slices.Equal(back.calls, want) {
		t.Errorf("calls = %v, want %v", back.calls, want)
	}

	// A folder only this client has snapshots of.
	back.world.Folders[0].Snapshots = []restic.Snapshot{snap("b2", "", "laptop", 30)}
	m = send(t, m, m.load()())
	m = keys(t, m, "g", "j", "p", "c")
	if !strings.Contains(asks(m), "No other client has snapshots of this folder") {
		t.Errorf("unexpected box: %s", asks(m))
	}
}

// A folder that is not here goes under its root; a root this client does
// not know is asked for once.
func TestPullFolderThatIsNotHere(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	m, back := started(t, 100, 20)
	m = keys(t, m, "j") // aha/website, not here
	m = keys(t, m, "p", "m")
	if !strings.Contains(asks(m), "Takes master into /home/u/Projects/aha/website, which is created") {
		t.Errorf("unexpected confirmation: %s", asks(m))
	}
	m = run(t, m, "y")
	want := []string{"target Projects/aha/website from ", "fetch Projects/aha/website into /home/u/Projects"}
	if !slices.Equal(back.calls, want) {
		t.Errorf("calls = %v, want %v", back.calls, want)
	}
	if !strings.HasPrefix(m.note, "pulled Projects/aha/website into /home/u/Projects/aha/website: b1b1b1b1 (desktop, ") {
		t.Errorf("note = %q", m.note)
	}

	// The same with a root that has no location yet.
	back.calls, back.roots = nil, map[string]string{}
	back.fail = map[string]error{"root Projects at /taken": errors.New("/taken is already the root other on this client")}
	m = keys(t, m, "p", "m")
	screen := m.View()
	t.Logf("asking for a root:\n%s", screen)
	if !strings.Contains(screen, "Where is Projects on this machine?") || !strings.Contains(screen, "> /home/u/Projects") {
		t.Errorf("the root is not asked for, or its suggestion is missing:\n%s", screen)
	}
	// An answer that will not do is said so, and can be corrected.
	m = keys(t, m, "/", "t", "a", "k", "e", "n", "enter")
	if !strings.Contains(asks(m), "/taken is already the root other on this client") || !strings.Contains(m.View(), "> /taken") {
		t.Errorf("the problem with the answer is not shown:\n%s", m.View())
	}
	for range "taken" {
		m = keys(t, m, "backspace")
	}
	m = keys(t, m, "d", "a", "t", "a", "/", "P", "enter")
	if !strings.Contains(asks(m), "Takes master into /data/P/aha/website") {
		t.Errorf("unexpected confirmation: %s", asks(m))
	}
	m = run(t, m, "y")
	if got := back.calls[len(back.calls)-1]; got != "fetch Projects/aha/website into /data/P" {
		t.Errorf("last call = %q", got)
	}
	// Enter alone takes the suggestion.
	back.calls = nil
	m = keys(t, m, "p", "m", "enter")
	if !strings.Contains(asks(m), "Takes master into /home/u/Projects/aha/website") {
		t.Errorf("the suggestion was not taken: %s", asks(m))
	}
}

func TestPullAll(t *testing.T) {
	m, back := started(t, 100, 20)
	m = keys(t, m, "g") // the root Projects: one behind, one not here, one changed, one in step
	m = keys(t, m, "p")
	screen := m.View()
	t.Logf("pulling a root:\n%s", screen)
	if !strings.Contains(screen, "Pull 2 folders under Projects?") || !strings.Contains(asks(m), "1 behind master, 1 not on this client yet") {
		t.Errorf("unexpected confirmation:\n%s", screen)
	}
	back.fail = map[string]error{"pull Projects/Freelance/kadir/boxing-machine from ": errors.New("both this folder and master changed")}
	m = run(t, m, "y")
	want := []string{
		"pull Projects/Freelance/kadir/boxing-machine from ",
		"target Projects/aha/website from ",
		"fetch Projects/aha/website into /home/u/Projects",
	}
	if !slices.Equal(back.calls, want) {
		t.Errorf("calls = %v, want %v", back.calls, want)
	}
	// One failed and one went through: both are reported.
	screen = m.View()
	if !strings.Contains(asks(m), "1 of 2 folders failed") || !strings.Contains(asks(m), "Projects/Freelance/kadir/boxing-machine: both this folder and master changed") {
		t.Errorf("the failure is not reported:\n%s", screen)
	}
	m = keys(t, m, "enter")
	if got := bottom(m); got != "pulled 1 of 2 folders under Projects" {
		t.Errorf("bottom line = %q", got)
	}

	// A root with nothing to pull.
	back.calls = nil
	m = keys(t, m, "G", "k", "k", "p") // the root notes: one ahead, one both changed
	if !strings.Contains(asks(m), "Nothing to pull") || len(back.calls) != 0 {
		t.Errorf("unexpected box: %s", asks(m))
	}
}

func TestRestore(t *testing.T) {
	m, back := started(t, 100, 20)
	m = keys(t, m, "j", "j", "j") // Freelance/kadir/boxing-machine, two snapshots
	const name = "Projects/Freelance/kadir/boxing-machine"

	// r on the folder goes to its log to choose.
	m = keys(t, m, "r")
	if m.focus != log || !strings.Contains(bottom(m), "choose a snapshot") {
		t.Errorf("r did not move into the log: focus = %d, bottom = %q", m.focus, bottom(m))
	}
	m = keys(t, m, "j", "r") // the older one
	screen := m.View()
	t.Logf("confirming a restore:\n%s", screen)
	if !strings.Contains(screen, "Restore "+name+"?") || !strings.Contains(asks(m), "match b1b1b1b1, the snapshot from") ||
		!strings.Contains(asks(m), "saved as a snapshot first") {
		t.Errorf("unexpected confirmation:\n%s", screen)
	}
	m = run(t, m, "y")
	if want := []string{"restore " + name + " to b1b1b1b1"}; !slices.Equal(back.calls, want) {
		t.Errorf("calls = %v, want %v", back.calls, want)
	}
	if got := bottom(m); got != "restored "+name+" to b1b1b1b1; its state before is snapshot 77777777" {
		t.Errorf("bottom line = %q", got)
	}

	// In the log of a folder that is not here there is nothing to restore into.
	back.calls = nil
	m = keys(t, m, "h", "g", "j", "j", "l", "r") // aha/website
	if m.dialog != nil || len(back.calls) != 0 || !strings.Contains(bottom(m), "not on this client") {
		t.Errorf("restore was offered for a folder that is not here: %q", bottom(m))
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

// browsing returns a model over a small home directory, some of it tracked.
//
//	~/Projects            root Projects
//	  aha/archype         tracked, in step
//	  aha/wipsync         tracked, behind
//	  new-thing/README.md
//	  .cache/
//	~/notes/journal       root notes; tracked, ahead
//	~/other/Projects/x
//	~/todo.txt
func browsing(t *testing.T) (Model, *fake, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, dir := range []string{"Projects/aha/archype/src", "Projects/aha/wipsync", "Projects/new-thing", "Projects/.cache", "notes/journal", "other/Projects/x"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{"Projects/new-thing/README.md", "todo.txt"} {
		if err := os.WriteFile(filepath.Join(home, file), []byte("hello\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b1 := snap("b1", "", "desktop", 90)
	folder := func(root, path string, s app.Status) app.FolderState {
		return app.FolderState{Folder: config.Folder{Root: root, Path: path}, Dir: filepath.Join(home, root, path),
			Status: s, Base: b1.ID, Master: b1.ID, Snapshots: []restic.Snapshot{b1}}
	}
	back := &fake{
		world: app.World{Folders: []app.FolderState{
			folder("Projects", "aha/archype", app.InStep),
			folder("Projects", "aha/wipsync", app.Behind),
			folder("notes", "journal", app.Ahead),
		}},
		roots: map[string]string{"Projects": filepath.Join(home, "Projects"), "notes": filepath.Join(home, "notes")},
	}
	m := Model{client: "laptop", repository: "sftp:u@host:/srv/repo", theme: DefaultTheme(), loading: true, back: back}
	return send(t, m, tea.WindowSizeMsg{Width: 110, Height: 18}, m.Init()()), back, home
}

func TestBrowser(t *testing.T) {
	m, _, home := browsing(t)
	m = keys(t, m, "tab")
	screen := m.View()
	t.Logf("the browser, opened beside the selected folder:\n%s", screen)
	// It opens where the selected folder is, on that folder.
	if m.view != files || m.br.dir != filepath.Join(home, "Projects/aha") || m.path() != filepath.Join(home, "Projects/aha/archype") {
		t.Fatalf("opened at %q on %q", m.br.dir, m.path())
	}
	for _, want := range []string{
		"wipsync  ~/Projects/aha",
		" archype/", "tracked", " wipsync/", "behind",
		// A tracked directory shows what the folder list would: its log.
		"Projects/aha/archype", "in step with master", "b1b1b1b1",
		"h up  l open    s snapshot  P push  p pull    space mark  / search  . hidden  tab folders",
	} {
		if !strings.Contains(screen, want) {
			t.Errorf("the screen lacks %q", want)
		}
	}

	// Up a level: the directory we came from is selected, and previewed.
	m = keys(t, m, "h")
	screen = m.View()
	t.Logf("one level up:\n%s", screen)
	if m.br.dir != filepath.Join(home, "Projects") || m.path() != filepath.Join(home, "Projects/aha") {
		t.Fatalf("after h: at %q on %q", m.br.dir, m.path())
	}
	for _, want := range []string{" aha/", " new-thing/", "not tracked: a to add it", "h up  l open    a add    space mark  / search  . hidden"} {
		if !strings.Contains(screen, want) {
			t.Errorf("the screen lacks %q", want)
		}
	}
	if strings.Contains(screen, ".cache") {
		t.Error("a hidden directory is shown")
	}
	if m = keys(t, m, "."); !strings.Contains(m.View(), " .cache/") || m.path() != filepath.Join(home, "Projects/aha") {
		t.Errorf("the dot did not show hidden names, or moved the selection:\n%s", m.View())
	}
	m = keys(t, m, ".")

	// Up again: the root is marked, and acts as one.
	m = keys(t, m, "h")
	screen = m.View()
	if m.path() != filepath.Join(home, "Projects") || !strings.Contains(screen, " Projects/") || !strings.Contains(screen, "root") ||
		!strings.Contains(screen, "2 folders") || !strings.Contains(screen, "p pull all") {
		t.Errorf("the root is not shown as one:\n%s", screen)
	}
	// Directories come before files, each in order.
	order := []string{" notes/", " other/", " Projects/", " todo.txt"}
	at := 0
	for _, name := range order {
		i := strings.Index(screen[at:], name)
		if i < 0 {
			t.Fatalf("%q is missing or out of order:\n%s", name, screen)
		}
		at += i
	}
	// A file can be neither entered nor added.
	m = keys(t, m, "G")
	if m.path() != filepath.Join(home, "todo.txt") {
		t.Fatalf("G went to %q", m.path())
	}
	if got := bottom(m); got != "h up    / search  . hidden  tab folders    j/k move  R refresh  q quit" {
		t.Errorf("bottom line on a file = %q", got)
	}
	if !strings.Contains(m.View(), "6 B, changed ") {
		t.Errorf("a file's details are missing:\n%s", m.View())
	}
	if in := keys(t, m, "l", "a"); in.br.dir != home || in.dialog != nil {
		t.Error("a file was entered or added")
	}

	// Into a directory and back; at the top, h stays.
	m = keys(t, m, "g", "j", "l", "l")
	if m.br.dir != filepath.Join(home, "other/Projects") || m.path() != filepath.Join(home, "other/Projects/x") {
		t.Errorf("after l l: at %q on %q", m.br.dir, m.path())
	}
	m = keys(t, m, "l", "l") // x is empty
	if m.br.dir != filepath.Join(home, "other/Projects/x") || !strings.Contains(m.View(), " empty") {
		t.Errorf("an empty directory: at %q\n%s", m.br.dir, m.View())
	}
	for range 40 {
		m = keys(t, m, "h")
	}
	if m.br.dir != "/" {
		t.Errorf("h did not stop at the top: %q", m.br.dir)
	}

	// Back to the folders from a place that belongs to none of them: the
	// selection is as it was left.
	m = keys(t, m, "tab")
	if m.view != tracked || m.selected().name() != "Projects/aha/archype" {
		t.Errorf("back in the folder list on %q", m.selected().name())
	}
}

// goTo moves the folder list to the row called name.
func goTo(t *testing.T, m Model, name string) Model {
	t.Helper()
	m = keys(t, m, "g")
	for range m.rows {
		if m.selected().name() == name {
			return m
		}
		m = keys(t, m, "j")
	}
	t.Fatalf("there is no row %q", name)
	return m
}

// Switching between the two views stays on the same thing where it can.
func TestSwitchingKeepsPlace(t *testing.T) {
	m, back, home := browsing(t)
	at := func(m Model) string { return strings.TrimPrefix(m.path(), home+"/") }
	// show puts the browser in a directory, on one of its entries.
	show := func(m Model, dir, name string) Model {
		m.browse(filepath.Join(home, dir), name)
		return m
	}

	// From a folder to its directory, selected in its parent.
	m = keys(t, goTo(t, m, "Projects/aha/wipsync"), "tab")
	if m.br.dir != filepath.Join(home, "Projects/aha") || at(m) != "Projects/aha/wipsync" {
		t.Errorf("from a folder: in %q on %q", m.br.dir, at(m))
	}
	// And back to a folder from its directory, wherever the list was.
	if m = keys(t, m, "k", "tab"); m.selected().name() != "Projects/aha/archype" {
		t.Errorf("from a tracked directory: on %q", m.selected().name())
	}
	m = keys(t, goTo(t, m, "notes/journal"), "tab")
	if m.br.dir != filepath.Join(home, "notes") || at(m) != "notes/journal" {
		t.Errorf("from another folder: in %q on %q", m.br.dir, at(m))
	}
	// From inside a tracked folder, on an entry or in an empty directory.
	if m = keys(t, show(m, "Projects/aha/archype", "src"), "tab"); m.selected().name() != "Projects/aha/archype" {
		t.Errorf("from an entry inside a tracked folder: on %q", m.selected().name())
	}
	m = show(keys(t, goTo(t, m, "notes/journal"), "tab"), "Projects/aha/archype/src", "")
	if len(m.br.entries) != 0 {
		t.Fatalf("src has %d entries", len(m.br.entries))
	}
	if m = keys(t, m, "tab"); m.selected().name() != "Projects/aha/archype" {
		t.Errorf("from an empty directory inside a tracked folder: on %q", m.selected().name())
	}

	// A root goes to its directory, and comes back as the root.
	m = keys(t, goTo(t, m, "Projects"), "tab")
	if m.br.dir != home || at(m) != "Projects" {
		t.Errorf("from a root: in %q on %q", m.br.dir, at(m))
	}
	if m = keys(t, show(m, "", "notes"), "tab"); m.selected().name() != "notes" {
		t.Errorf("from a root's directory: on %q", m.selected().name())
	}
	// Under a root but in no folder, it is the root.
	m = show(keys(t, m, "tab"), "Projects", "new-thing")
	if m = keys(t, m, "tab"); m.selected().name() != "Projects" {
		t.Errorf("from a plain directory under a root: on %q", m.selected().name())
	}
	// Under nothing wipsync knows, the folder list is as it was left.
	m = show(keys(t, goTo(t, m, "Projects/aha/archype"), "tab"), "other", "Projects")
	if m = keys(t, m, "tab"); m.selected().name() != "Projects/aha/archype" {
		t.Errorf("from an unrelated directory: on %q", m.selected().name())
	}

	// A folder that is not here has no directory. With its root known, the
	// browser goes to the root; otherwise it stays where it was.
	back.world.Folders = append(back.world.Folders,
		app.FolderState{Folder: config.Folder{Root: "Projects", Path: "aha/website"}, Status: app.NotHere},
		app.FolderState{Folder: config.Folder{Root: "elsewhere", Path: "thing"}, Status: app.NotHere})
	m = send(t, m, m.load()())
	m = show(keys(t, m, "tab"), "other", "Projects")
	m = keys(t, goTo(t, keys(t, m, "tab"), "elsewhere/thing"), "tab")
	if m.br.dir != filepath.Join(home, "other") || at(m) != "other/Projects" {
		t.Errorf("for a folder with no place here: in %q on %q", m.br.dir, at(m))
	}
	m = keys(t, goTo(t, keys(t, m, "tab"), "Projects/aha/website"), "tab")
	if m.br.dir != home || at(m) != "Projects" {
		t.Errorf("for a folder that is not here: in %q on %q", m.br.dir, at(m))
	}
}

// A tracked directory whose name starts with a dot is still shown when the
// browser is opened on it.
func TestSwitchingToHiddenFolder(t *testing.T) {
	m, back, home := browsing(t)
	dir := filepath.Join(home, "Projects/.cache")
	back.world.Folders = append(back.world.Folders, app.FolderState{
		Folder: config.Folder{Root: "Projects", Path: ".cache"}, Dir: dir, Status: app.Changed})
	m = send(t, m, m.load()())
	m = keys(t, goTo(t, m, "Projects/.cache"), "tab")
	if m.path() != dir {
		t.Errorf("on %q, want %q", m.path(), dir)
	}
}

// Space marks rows; the action keys then apply to everything marked.
func TestMarkingFolders(t *testing.T) {
	m, back := started(t, 130, 20)
	// On aha/archype: space marks it and moves on, twice.
	m = keys(t, m, " ", " ")
	if m.selected().name() != "Projects/aha/wipsync" || len(m.marked().folders) != 2 {
		t.Fatalf("after two spaces: on %q with %d marked", m.selected().name(), len(m.marked().folders))
	}
	screen := m.View()
	t.Logf("two folders marked:\n%s", screen)
	if !strings.Contains(screen, "┃  aha/archype") || !strings.Contains(screen, "┃  aha/website") || strings.Contains(screen, "┃  aha/wipsync") {
		t.Errorf("the marks are not drawn where they belong:\n%s", screen)
	}
	// archype is here and website is not: one to snapshot or push, two to pull.
	if got, want := bottom(m), "l log    2 marked:  s snapshot 1  P push 1  p pull 2    space mark  esc unmark all  / search  tab browse"; !strings.HasPrefix(got, want) {
		t.Errorf("bottom line:\n got %q\nwant %q", got, want)
	}
	// Space again unmarks.
	if un := keys(t, m, "k", " "); len(un.marked().folders) != 1 {
		t.Errorf("%d marked after unmarking one", len(un.marked().folders))
	}
	// The log keeps its own keys while it has the focus.
	if in := keys(t, m, "l"); !strings.Contains(bottom(in), "r restore this snapshot") || strings.Contains(bottom(in), "marked") {
		t.Errorf("in the log with marks: %q", bottom(in))
	}

	m = keys(t, m, " ") // wipsync too
	m = run(t, m, "s")
	want := []string{"snapshot Projects/aha/archype", "snapshot Projects/aha/wipsync"}
	if !slices.Equal(back.calls, want) {
		t.Errorf("calls = %v, want %v", back.calls, want)
	}
	if m.note != "2 folders: 2 snapshots saved, 0 unchanged" || m.marked().any() {
		t.Errorf("note = %q, still marked: %v", m.note, m.marked().any())
	}

	// A root's line marks everything under it, and unmarks it again.
	back.calls = nil
	m = keys(t, goTo(t, m, "notes"), " ")
	if n := len(m.marked().folders); n != 2 {
		t.Fatalf("%d marked under the root, want 2", n)
	}
	if un := keys(t, goTo(t, m, "notes"), " "); un.marked().any() {
		t.Error("space on a root with all marked did not unmark them")
	}
	back.fail = map[string]error{"push notes/recipes": errors.New("notes/recipes: push rejected")}
	m = run(t, m, "P")
	if want := []string{"push notes/journal", "push notes/recipes"}; !slices.Equal(back.calls, want) {
		t.Errorf("calls = %v, want %v", back.calls, want)
	}
	if !strings.Contains(asks(m), "1 of 2 failed") || !strings.Contains(asks(m), "notes/recipes: push rejected") || m.note != "pushed 1 of 2 folders" {
		t.Errorf("box: %s, note: %q", asks(m), m.note)
	}
	m = keys(t, m, "enter")

	// Esc drops all marks.
	m = keys(t, goTo(t, m, "Projects"), " ")
	if m = keys(t, m, "esc"); m.marked().any() {
		t.Error("esc did not unmark")
	}
}

func TestPullingMarked(t *testing.T) {
	m, back := started(t, 130, 20)
	m = keys(t, goTo(t, m, "Projects"), " ") // all four: three here, one not
	m = keys(t, m, "p")
	if !strings.Contains(asks(m), "Pull 4 marked folders?") || !strings.Contains(asks(m), "3 are here, 1 are not on this client yet") {
		t.Fatalf("unexpected question: %s", asks(m))
	}
	if no := keys(t, m, "n"); !no.marked().any() || len(back.calls) != 0 {
		t.Error("n pulled, or dropped the marks")
	}
	m = run(t, m, "y")
	want := []string{
		"pull Projects/aha/archype from ",
		"target Projects/aha/website from ", "fetch Projects/aha/website into /home/u/Projects",
		"pull Projects/aha/wipsync from ",
		"pull Projects/Freelance/kadir/boxing-machine from ",
	}
	if !slices.Equal(back.calls, want) {
		t.Errorf("calls = %v\nwant    %v", back.calls, want)
	}
	if m.note != "pulled 4 of 4 folders" || m.marked().any() {
		t.Errorf("note = %q, still marked: %v", m.note, m.marked().any())
	}

	// A root with no place here is asked for once; two such roots are too many.
	back.calls, back.roots = nil, map[string]string{"notes": "/home/u/notes"}
	back.world.Folders = append(back.world.Folders,
		app.FolderState{Folder: config.Folder{Root: "Work", Path: "a"}, Status: app.NotHere},
		app.FolderState{Folder: config.Folder{Root: "Work", Path: "b"}, Status: app.NotHere},
		app.FolderState{Folder: config.Folder{Root: "Zeta", Path: "c"}, Status: app.NotHere})
	m = send(t, m, m.load()())
	t.Setenv("HOME", "/home/u")
	m = keys(t, goTo(t, m, "Work"), " ", "p")
	if !strings.Contains(asks(m), "Where is Work on this machine?") {
		t.Fatalf("the root was not asked for: %s", asks(m))
	}
	m = run(t, keys(t, m, "enter"), "y")
	if got := back.calls; len(got) != 4 || got[1] != "fetch Work/a into /home/u/Work" || got[3] != "fetch Work/b into /home/u/Work" {
		t.Errorf("calls = %v", got)
	}
	m = keys(t, goTo(t, m, "Work"), " ")
	m = keys(t, goTo(t, m, "Zeta"), " ", "p")
	if !strings.Contains(asks(m), "2 roots this client has no location for: Work, Zeta") {
		t.Errorf("unexpected box: %s", asks(m))
	}
}

func TestMarkingInBrowser(t *testing.T) {
	m, back, home := browsing(t)
	projects := filepath.Join(home, "Projects")
	for _, dir := range []string{"one", "two", "three"} {
		if err := os.Mkdir(filepath.Join(projects, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	m = keys(t, m, "tab", "h") // ~/Projects: aha, new-thing, one, three, two
	m.browse(projects, "new-thing")
	m = keys(t, m, " ", " ", "j", " ") // new-thing, one, and two; not three
	screen := m.View()
	t.Logf("three directories marked:\n%s", screen)
	if !strings.Contains(screen, "┃new-thing/") || !strings.Contains(screen, "┃one/") || !strings.Contains(screen, "┃two/") || strings.Contains(screen, "┃three/") {
		t.Errorf("the marks are not drawn where they belong:\n%s", screen)
	}
	if !strings.Contains(bottom(m), "3 marked:  a add 3    space mark  esc unmark all  / search  . hidden  tab folders") {
		t.Errorf("bottom line = %q", bottom(m))
	}
	// Marks stay while moving around, and other views have their own.
	m = keys(t, m, "h", "l")
	if n := len(m.marked().dirs); n != 3 {
		t.Errorf("%d marked after moving away and back", n)
	}
	if away := keys(t, m, "tab"); away.marked().any() {
		t.Error("the browser's marks show in the folder list")
	}

	m = keys(t, m, "a")
	if !strings.Contains(asks(m), "Add 3 directories?") ||
		!strings.Contains(asks(m), "They become Projects/new-thing, Projects/one, Projects/two.") {
		t.Fatalf("unexpected question: %s", asks(m))
	}
	m = run(t, m, "y")
	want := []string{
		"add " + projects + "/new-thing as Projects at " + projects,
		"add " + projects + "/one as Projects at " + projects,
		"add " + projects + "/two as Projects at " + projects,
	}
	if !slices.Equal(back.calls, want) {
		t.Errorf("calls = %v\nwant    %v", back.calls, want)
	}
	if !strings.HasPrefix(m.note, "added 3 of 3 folders") || m.marked().any() {
		t.Errorf("note = %q, still marked: %v", m.note, m.marked().any())
	}

	// Tracked and untracked together: each key takes what it applies to.
	back.calls = nil
	m.browse(filepath.Join(projects, "aha"), "archype")
	m = keys(t, m, " ")
	m.browse(projects, "three")
	m = keys(t, m, " ")
	if !strings.Contains(bottom(m), "2 marked:  s snapshot 1  P push 1  p pull 1  a add 1") {
		t.Errorf("bottom line = %q", bottom(m))
	}
	// A file is not marked, and space still moves on.
	m.browse(home, "todo.txt")
	before := len(m.marked().dirs) + len(m.marked().folders)
	if m = keys(t, m, " "); len(m.marked().dirs)+len(m.marked().folders) != before {
		t.Error("a file was marked")
	}
	m = keys(t, m, "esc")

	// Directories under no known root share one, asked for once.
	other := filepath.Join(home, "other")
	if err := os.Mkdir(filepath.Join(other, "Projects/y"), 0o755); err != nil {
		t.Fatal(err)
	}
	m.browse(filepath.Join(other, "Projects"), "x")
	m = keys(t, m, " ", " ", "a")
	if !strings.Contains(asks(m), "Which parent is the root of the 2 directories") || !slices.Equal(m.dialog.choices, []string{other, other + "/Projects"}) {
		t.Fatalf("unexpected question: %s, choices %v", asks(m), m.dialog.choices)
	}
	m = keys(t, m, "enter")
	if !strings.Contains(asks(m), "They become other/Projects/x, other/Projects/y.") {
		t.Fatalf("unexpected question: %s", asks(m))
	}
	m = run(t, m, "y")
	if len(back.calls) != 2 || back.calls[1] != "add "+other+"/Projects/y as other at "+other {
		t.Errorf("calls = %v", back.calls)
	}

	// One that cannot be added stops them all.
	back.calls = nil
	back.fail = map[string]error{"addable " + other + "/Projects/y": errors.New(other + "/Projects/y is already added")}
	m.browse(filepath.Join(other, "Projects"), "x")
	m = keys(t, m, " ", " ", "a")
	if !strings.Contains(asks(m), "Cannot add 1 of the 2 marked directories") || !strings.Contains(asks(m), "is already added") || len(back.calls) != 0 {
		t.Errorf("unexpected box: %s", asks(m))
	}
}

// "/" searches the left pane: the selection follows what is typed.
func TestSearch(t *testing.T) {
	m, _ := started(t, 130, 20)
	typed := func(m Model, text string) Model {
		for _, r := range text {
			m = send(t, m, keyMsg(string(r)))
		}
		return m
	}
	m = keys(t, m, "/")
	if got := bottom(m); got != "/█    enter keep  esc back" {
		t.Errorf("bottom line = %q", got)
	}
	// The selection moves as the query grows, without regard to case.
	m = typed(m, "a")
	if m.selected().name() != "Projects/aha/archype" {
		t.Errorf("after a: on %q", m.selected().name())
	}
	m = typed(m, "ha/W")
	if m.selected().name() != "Projects/aha/website" || bottom(m) != "/aha/W█    enter keep  esc back" {
		t.Errorf("after aha/W: on %q, bottom %q", m.selected().name(), bottom(m))
	}
	m = typed(m, "i")
	if m.selected().name() != "Projects/aha/wipsync" {
		t.Errorf("after aha/Wi: on %q", m.selected().name())
	}
	// Nothing matches: it says so, and waits where the search started.
	m = typed(m, "zz")
	if m.selected().name() != "Projects/aha/archype" || !strings.Contains(bottom(m), "no match") {
		t.Errorf("with no match: on %q, bottom %q", m.selected().name(), bottom(m))
	}
	// Taking letters back brings the match back.
	m = keys(t, m, "backspace", "backspace")
	if m.selected().name() != "Projects/aha/wipsync" {
		t.Errorf("after backspace: on %q", m.selected().name())
	}
	// While typing, letters are letters: q does not quit, s does not snapshot.
	m = keys(t, m, "backspace", "backspace", "backspace", "backspace", "backspace", "backspace")
	next, cmd := m.Update(keyMsg("q"))
	if m = next.(Model); cmd != nil || m.search == nil || m.search.query != "q" {
		t.Fatalf("q while searching: quit %v, query %+v", cmd != nil, m.search)
	}
	// Esc gives up and goes back to where it started.
	if m = keys(t, m, "esc"); m.search != nil || m.selected().name() != "Projects/aha/archype" {
		t.Errorf("after esc: searching %v, on %q", m.search != nil, m.selected().name())
	}

	// Enter keeps the place; n and N then go through the matches, around the end.
	m = typed(keys(t, m, "/"), "es")
	if m.selected().name() != "notes" { // the root, the first line with "es"
		t.Fatalf("after es: on %q", m.selected().name())
	}
	m = keys(t, m, "enter")
	if m.search != nil || m.selected().name() != "notes" || !strings.Contains(bottom(m), "/ search  n next") {
		t.Errorf("after enter: on %q, bottom %q", m.selected().name(), bottom(m))
	}
	var seen []string
	for range 4 {
		m = keys(t, m, "n")
		seen = append(seen, m.selected().name())
	}
	if want := []string{"notes/journal", "notes/recipes", "notes", "notes/journal"}; !slices.Equal(seen, want) {
		t.Errorf("n went through %v, want %v", seen, want)
	}
	if m = keys(t, m, "N", "N"); m.selected().name() != "notes/recipes" {
		t.Errorf("after N N: on %q", m.selected().name())
	}
	// A root is found by its name, a folder also by its root's.
	m = typed(keys(t, m, "/"), "proj")
	if m.selected().name() != "Projects" {
		t.Errorf("after proj: on %q", m.selected().name())
	}
	m = keys(t, m, "enter")

	// The log has no search; the key does nothing there.
	if in := keys(t, m, "j", "l", "/"); in.search != nil {
		t.Error("/ started a search in the log")
	}
}

func TestSearchInBrowser(t *testing.T) {
	m, _, home := browsing(t)
	m = keys(t, m, "tab")
	m.browse(home, "") // notes, other, Projects, todo.txt
	m = keys(t, m, "/", "o")
	if m.path() != filepath.Join(home, "notes") {
		t.Errorf("after o: on %q", m.path())
	}
	m = keys(t, m, "t", "h")
	if m.path() != filepath.Join(home, "other") {
		t.Errorf("after oth: on %q", m.path())
	}
	m = keys(t, m, "esc", "/", "T", "O", "D")
	if m.path() != filepath.Join(home, "todo.txt") {
		t.Errorf("after TOD: on %q", m.path())
	}
	// The right pane follows, as when moving by hand.
	if !strings.Contains(m.View(), "a file cannot be added") {
		t.Errorf("the preview did not follow the search:\n%s", m.View())
	}
	m = keys(t, m, "enter", "h", "l")
	// The kept query still works after moving to another directory.
	m.browse(filepath.Join(home, "Projects"), "")
	if m = keys(t, m, "n"); m.note != "nothing matches TOD" {
		t.Errorf("note = %q", m.note)
	}
	// An empty directory has nothing to find.
	m.browse(filepath.Join(home, "other/Projects/x"), "")
	if m = keys(t, m, "/", "a"); !strings.Contains(bottom(m), "no match") {
		t.Errorf("bottom line = %q", bottom(m))
	}
}
