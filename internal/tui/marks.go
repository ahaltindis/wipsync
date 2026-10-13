package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ahaltindis/wipsync/internal/app"
)

// marked is what the marks of the current view stand for.
type marked struct {
	folders []*app.FolderState // tracked folders, in the order shown
	dirs    []string           // directories that are not tracked, in the browser
}

func (k marked) any() bool { return len(k.folders)+len(k.dirs) > 0 }

// here are the marked folders that are on this client.
func (k marked) here() []*app.FolderState {
	var out []*app.FolderState
	for _, f := range k.folders {
		if f.Dir != "" {
			out = append(out, f)
		}
	}
	return out
}

// marked returns what is marked in the view the left pane shows. Each view
// keeps its own marks.
func (m Model) marked() marked {
	var k marked
	if m.view == tracked {
		for _, r := range m.rows {
			if r.folder != nil && m.marks[r.name()] {
				k.folders = append(k.folders, r.folder)
			}
		}
		return k
	}
	paths := make([]string, 0, len(m.br.marks))
	for p := range m.br.marks {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	for _, p := range paths {
		if f := m.byDir[p]; f != nil {
			k.folders = append(k.folders, f)
		} else {
			k.dirs = append(k.dirs, p)
		}
	}
	return k
}

// mark toggles the mark on what is selected and moves on to the next line.
// On a root it marks all of its folders, or unmarks them if all were.
func (m *Model) mark() {
	if m.view == files {
		e, ok := m.entry()
		if !ok {
			return
		}
		if e.dir {
			m.br.marks = cloned(m.br.marks)
			if p := m.path(); m.br.marks[p] {
				delete(m.br.marks, p)
			} else {
				m.br.marks[p] = true
			}
		}
		m.br.sel = min(m.br.sel+1, len(m.br.entries)-1)
		m.look()
		return
	}
	m.marks = cloned(m.marks)
	switch r := m.trackedRow(); {
	case r.folder != nil:
		if m.marks[r.name()] {
			delete(m.marks, r.name())
		} else {
			m.marks[r.name()] = true
		}
	case r.root != "":
		all := true
		for _, f := range m.under(r.root) {
			all = all && m.marks[f.Folder.String()]
		}
		for _, f := range m.under(r.root) {
			if all {
				delete(m.marks, f.Folder.String())
			} else {
				m.marks[f.Folder.String()] = true
			}
		}
	}
	m.move(1)
}

// cloned copies the marks before they are changed: an earlier copy of the
// model must keep the ones it had.
func cloned(marks map[string]bool) map[string]bool {
	out := make(map[string]bool, len(marks)+1)
	for k := range marks {
		out[k] = true
	}
	return out
}

// unmark removes every mark of the current view.
func (m *Model) unmark() {
	if m.view == files {
		m.br.marks = nil
	} else {
		m.marks = nil
	}
}

// markedActions are the keys that act on everything marked.
func (m Model) markedActions(k marked) []action {
	var out []action
	if n := len(k.here()); n > 0 {
		out = append(out,
			action{"s", fmt.Sprintf("snapshot %d", n), Model.snapshotMarked},
			action{"P", fmt.Sprintf("push %d", n), Model.pushMarked})
	}
	if n := len(k.folders); n > 0 {
		out = append(out, action{"p", fmt.Sprintf("pull %d", n), Model.askPullMarked})
	}
	if n := len(k.dirs); n > 0 {
		out = append(out, action{"a", fmt.Sprintf("add %d", n), Model.addMarked})
	}
	return out
}

// failures turns what went wrong for some of n into one error, or nil.
func failures(failed []string, n int) error {
	if len(failed) == 0 {
		return nil
	}
	return fmt.Errorf("%d of %d failed.\n\n%s", len(failed), n, strings.Join(failed, "\n\n"))
}

func (m Model) snapshotMarked() (tea.Model, tea.Cmd) {
	list := m.marked().here()
	m.unmark()
	n := len(list)
	return m.start(fmt.Sprintf("snapshotting %d folders", n), "snapshot every marked folder", func(ctx context.Context) (string, error) {
		saved := 0
		var failed []string
		for _, f := range list {
			res, err := m.back.Snapshot(ctx, f.Folder)
			if err != nil {
				failed = append(failed, f.Folder.String()+": "+err.Error())
			} else if res.SnapshotID != "" {
				saved++
			}
		}
		note := fmt.Sprintf("%d folders: %d snapshots saved, %d unchanged", n, saved, n-saved-len(failed))
		return note, failures(failed, n)
	})
}

func (m Model) pushMarked() (tea.Model, tea.Cmd) {
	list := m.marked().here()
	m.unmark()
	n := len(list)
	return m.start(fmt.Sprintf("pushing %d folders", n), "push every marked folder", func(ctx context.Context) (string, error) {
		var failed []string
		for _, f := range list {
			if _, err := m.back.Push(ctx, f.Folder); err != nil {
				failed = append(failed, err.Error())
			}
		}
		return fmt.Sprintf("pushed %d of %d folders", n-len(failed), n), failures(failed, n)
	})
}

// askPullMarked confirms pulling master into every marked folder. A root
// this client does not know is asked for first; with more than one such
// root there would be too much to ask.
func (m Model) askPullMarked() (tea.Model, tea.Cmd) {
	k := m.marked()
	var unknown []string
	missing := 0
	for _, f := range k.folders {
		if f.Dir != "" {
			continue
		}
		missing++
		if _, ok := m.back.RootDir(f.Folder.Root); !ok && !slices.Contains(unknown, f.Folder.Root) {
			unknown = append(unknown, f.Folder.Root)
		}
	}
	if len(unknown) > 1 {
		m.dialog = message("Pull the marked folders",
			"They belong to "+fmt.Sprint(len(unknown))+" roots this client has no location for: "+strings.Join(unknown, ", ")+
				". Pull one folder of each first, so that their locations are known.")
		return m, nil
	}
	n := len(k.folders)
	confirm := func(m Model, asked string) (tea.Model, tea.Cmd) {
		text := fmt.Sprintf("Takes master into each, if it is ahead of the folder. %d are here", n-missing)
		if missing > 0 {
			text += fmt.Sprintf(", %d are not on this client yet and are created", missing)
		}
		text += ". Folders that changed here are refused and reported. Each folder's current state is saved as a snapshot first."
		m.dialog = &dialog{
			title:   fmt.Sprintf("Pull %d marked folders?", n),
			text:    text,
			confirm: true,
			done: func(m Model, _ string) (tea.Model, tea.Cmd) {
				m.unmark()
				return m.start(fmt.Sprintf("pulling %d folders", n), "pull every marked folder", func(ctx context.Context) (string, error) {
					var failed []string
					for _, f := range k.folders {
						var err error
						if f.Dir != "" {
							_, err = pull(ctx, m.back, f.Folder, "")
						} else {
							rootDir, known := m.back.RootDir(f.Folder.Root)
							if !known {
								rootDir = asked
							}
							_, err = fetch(ctx, m.back, f.Folder, rootDir, "")
						}
						if err != nil {
							failed = append(failed, f.Folder.String()+": "+err.Error())
						}
					}
					return fmt.Sprintf("pulled %d of %d folders", n-len(failed), n), failures(failed, n)
				})
			},
		}
		return m, nil
	}
	if len(unknown) == 1 {
		return m.withRoot(unknown[0], confirm)
	}
	return confirm(m, "")
}

// addition is a directory to add, and the root it goes under.
type addition struct {
	dir, rootName, rootDir string
}

func (a addition) name() string {
	rel, _ := filepath.Rel(a.rootDir, a.dir)
	return a.rootName + "/" + filepath.ToSlash(rel)
}

// addMarked starts syncing every marked directory. Those under a known
// root go there; the rest share one root, which is asked for once.
func (m Model) addMarked() (tea.Model, tea.Cmd) {
	dirs := m.marked().dirs
	var problems []string
	for _, dir := range dirs {
		if err := m.back.CheckAddable(dir); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) > 0 {
		m.dialog = message(fmt.Sprintf("Cannot add %d of the %d marked directories", len(problems), len(dirs)),
			strings.Join(problems, "\n\n")+"\n\nNothing was added. Unmark them with space and try again.")
		return m, nil
	}
	var list []addition
	var rootless []string
	for _, dir := range dirs {
		if name, rootDir, ok := m.back.RootAbove(dir); ok {
			list = append(list, addition{dir, name, rootDir})
		} else {
			rootless = append(rootless, dir)
		}
	}
	if len(rootless) == 0 {
		return m.askAddMarked(list)
	}
	// The parents all of them have in common, highest first.
	candidates := app.RootCandidates(rootless[0])
	for _, dir := range rootless[1:] {
		theirs := app.RootCandidates(dir)
		candidates = slices.DeleteFunc(candidates, func(c string) bool { return !slices.Contains(theirs, c) })
	}
	if len(candidates) == 0 {
		m.dialog = message("Add the marked directories",
			"Those that are under no known root have no parent in common that could be their root. Add them in separate steps.")
		return m, nil
	}
	labels := make([]string, len(candidates))
	for i, c := range candidates {
		labels[i] = short(c)
	}
	m.dialog = &dialog{
		title:   fmt.Sprintf("Which parent is the root of the %d directories under no known root?", len(rootless)),
		text:    "A folder is known to all clients by its root and its path under it. Each client maps the root to its own location.",
		choices: candidates,
		labels:  labels,
		done: func(m Model, rootDir string) (tea.Model, tea.Cmd) {
			return m.nameRoot(rootDir, filepath.Base(rootDir), func(m Model, name string) (tea.Model, tea.Cmd) {
				for _, dir := range rootless {
					list = append(list, addition{dir, name, rootDir})
				}
				slices.SortFunc(list, func(x, y addition) int { return strings.Compare(x.dir, y.dir) })
				return m.askAddMarked(list)
			})
		},
	}
	return m, nil
}

// askAddMarked confirms adding the directories, each under its root.
func (m Model) askAddMarked(list []addition) (tea.Model, tea.Cmd) {
	names := make([]string, len(list))
	for i, a := range list {
		names[i] = a.name()
	}
	n := len(list)
	m.dialog = &dialog{
		title:   fmt.Sprintf("Add %d directories?", n),
		text:    "They become " + strings.Join(names, ", ") + ". Nothing is uploaded until their first snapshots.",
		confirm: true,
		done: func(m Model, _ string) (tea.Model, tea.Cmd) {
			m.unmark()
			return m.start(fmt.Sprintf("adding %d folders", n), "add every marked directory", func(context.Context) (string, error) {
				var failed []string
				for _, a := range list {
					if _, err := m.back.Add(a.dir, a.rootName, a.rootDir); err != nil {
						failed = append(failed, err.Error())
					}
				}
				return fmt.Sprintf("added %d of %d folders: mark them and s for their first snapshots", n-len(failed), n), failures(failed, n)
			})
		},
	}
	return m, nil
}
