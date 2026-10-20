package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ahaltindis/wipsync/internal/app"
	"github.com/ahaltindis/wipsync/internal/db"
)

// action is something a key does to what is selected.
type action struct {
	key, does string
	run       func(Model) (tea.Model, tea.Cmd)
}

// actions are the keys that apply where the selection is now. Only these
// are offered in the bottom line, and only these do anything.
func (m Model) actions() []action {
	if k := m.marked(); k.any() && m.focus == folders {
		return m.markedActions(k)
	}
	return append(m.selectionActions(), m.openActions()...)
}

// selectionActions are what can be done to the selected folder or root.
func (m Model) selectionActions() []action {
	r := m.selected()
	if m.view == files {
		e, ok := m.entry()
		switch {
		case !ok:
			return nil
		case r.folder != nil:
			return []action{
				{"s", "snapshot", Model.snapshot},
				{"P", "push", Model.push},
				{"p", "pull", Model.pullMenu},
			}
		case r.root != "":
			return []action{{"P", "push all", Model.askPushAll}, {"p", "pull all", Model.askPullAll}}
		case e.dir:
			return []action{{"a", "add", Model.add}}
		}
		return nil
	}
	switch {
	case m.focus == log:
		return []action{{"r", "restore this snapshot", Model.askRestore}, {"d", "delete this snapshot", Model.askDelete}}
	case r.folder == nil && r.root != "":
		return []action{{"P", "push all", Model.askPushAll}, {"p", "pull all", Model.askPullAll}}
	case r.folder == nil:
		return nil
	case r.folder.Dir == "":
		return []action{{"p", "pull", Model.pullMenu}}
	}
	return []action{
		{"s", "snapshot", Model.snapshot},
		{"P", "push", Model.push},
		{"p", "pull", Model.pullMenu},
		{"r", "restore", Model.chooseRestore},
	}
}

// doneMsg is how a finished action reports back.
type doneMsg struct {
	what string // what was being done, for "Could not …"
	note string // how it went
	err  error
}

// start runs an action in the background; the screen says it is running.
func (m Model) start(busy, what string, run func(ctx context.Context) (string, error)) (tea.Model, tea.Cmd) {
	m.busy = busy
	return m, func() tea.Msg {
		note, err := run(context.Background())
		return doneMsg{what: what, note: note, err: err}
	}
}

func (m Model) snapshot() (tea.Model, tea.Cmd) {
	f := m.selected().folder.Folder
	return m.start("snapshotting "+f.String(), "snapshot "+f.String(), func(ctx context.Context) (string, error) {
		res, err := m.back.Snapshot(ctx, f)
		if err != nil || res.SnapshotID == "" {
			return f.String() + ": no changes", err
		}
		return fmt.Sprintf("%s: snapshot %s, %d new, %d changed, %s stored",
			f, res.SnapshotID[:8], res.FilesNew, res.FilesChanged, app.FormatBytes(res.BytesStored)), nil
	})
}

func (m Model) push() (tea.Model, tea.Cmd) {
	f := m.selected().folder.Folder
	return m.start("pushing "+f.String(), "push "+f.String(), func(ctx context.Context) (string, error) {
		res, err := m.back.Push(ctx, f)
		if err != nil {
			return "", err
		}
		return res.Line(f), nil
	})
}

// pullMenu asks what to pull into the selected folder: master, or a
// client's latest snapshot.
func (m Model) pullMenu() (tea.Model, tea.Cmd) {
	f := m.selected().folder
	m.dialog = &dialog{
		title: "Pull " + f.Folder.String(),
		menu:  []option{{"m", "master"}, {"c", "a client's latest snapshot"}},
		done: func(m Model, key string) (tea.Model, tea.Cmd) {
			if key == "m" {
				return m.askPull(f, "")
			}
			var clients []string
			for _, s := range f.Snapshots {
				if s.Host != m.client && !slices.Contains(clients, s.Host) {
					clients = append(clients, s.Host)
				}
			}
			if len(clients) == 0 {
				m.dialog = message("Pull "+f.Folder.String(), "No other client has snapshots of this folder.")
				return m, nil
			}
			slices.Sort(clients)
			m.dialog = &dialog{
				title:   "Pull " + f.Folder.String() + " from which client?",
				choices: clients,
				done: func(m Model, client string) (tea.Model, tea.Cmd) {
					return m.askPull(f, client)
				},
			}
			return m, nil
		},
	}
	return m, nil
}

// askPull confirms a pull, after finding out where the folder goes if it
// is not on this client yet.
func (m Model) askPull(f *app.FolderState, from string) (tea.Model, tea.Cmd) {
	what := "master"
	if from != "" {
		what = "the latest snapshot of " + from
	}
	name := f.Folder.String()
	if f.Dir != "" {
		m.dialog = &dialog{
			title:   "Pull " + name + "?",
			text:    "Takes " + what + " into " + f.Dir + ", if it is ahead of the folder. The folder's current state is saved as a snapshot first.",
			confirm: true,
			done: func(m Model, _ string) (tea.Model, tea.Cmd) {
				return m.start("pulling "+name, "pull "+name, func(ctx context.Context) (string, error) {
					return pull(ctx, m.back, f.Folder, from)
				})
			},
		}
		return m, nil
	}
	return m.withRoot(f.Folder.Root, func(m Model, rootDir string) (tea.Model, tea.Cmd) {
		dir := filepath.Join(rootDir, filepath.FromSlash(f.Folder.Path))
		m.dialog = &dialog{
			title:   "Pull " + name + "?",
			text:    "Takes " + what + " into " + dir + ", which is created.",
			confirm: true,
			done: func(m Model, _ string) (tea.Model, tea.Cmd) {
				return m.start("pulling "+name, "pull "+name, func(ctx context.Context) (string, error) {
					return fetch(ctx, m.back, f.Folder, rootDir, from)
				})
			},
		}
		return m, nil
	})
}

// withRoot goes on with where a root is on this client, asking if this
// client does not know it yet.
func (m Model) withRoot(root string, then func(Model, string) (tea.Model, tea.Cmd)) (tea.Model, tea.Cmd) {
	if dir, ok := m.back.RootDir(root); ok {
		return then(m, dir)
	}
	preset := root
	if home, err := os.UserHomeDir(); err == nil {
		preset = filepath.Join(home, root)
	}
	ask := &dialog{
		title:  "Where is " + root + " on this machine?",
		text:   "Its folders are created under this directory. The answer is remembered.",
		typing: true,
		preset: preset,
	}
	ask.done = func(m Model, line string) (tea.Model, tea.Cmd) {
		dir, err := absolute(line)
		if err == nil {
			err = m.back.CheckNewRoot(root, dir)
		}
		if err != nil {
			again := *ask
			again.typed, again.problem = line, err.Error()
			again.done = ask.done
			m.dialog = &again
			return m, nil
		}
		return then(m, dir)
	}
	m.dialog = ask
	return m, nil
}

// absolute turns what was typed into a full path; ~/ is the home directory.
func absolute(path string) (string, error) {
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, rest)
	}
	return filepath.Abs(path)
}

// pull brings the target into a folder that is here, and says how it went.
func pull(ctx context.Context, back Backend, f db.Folder, from string) (string, error) {
	res, err := back.Pull(ctx, f, from)
	if err != nil {
		return "", err
	}
	return res.Line(f), nil
}

// fetch brings a folder that is not here into its root's directory.
func fetch(ctx context.Context, back Backend, f db.Folder, rootDir, from string) (string, error) {
	target, err := back.FindTarget(ctx, f, from)
	if err != nil {
		return "", err
	}
	res, err := back.Fetch(ctx, f, rootDir, target)
	if err != nil {
		return "", err
	}
	return res.Line(f), nil
}

// all says how a pull or push of many folders went: the summary, and the
// failures as the error.
func all(res app.AllResult, err error) (string, error) {
	if err != nil {
		return "", err
	}
	return res.Summary(), failures(res.Failed, res.Done+len(res.Failed))
}

// askPullAll confirms pulling master into every folder of the selected
// root that is behind it or not on this client yet.
func (m Model) askPullAll() (tea.Model, tea.Cmd) {
	root := m.selected().root
	var behind, missing []*app.FolderState
	for _, f := range m.under(root) {
		switch {
		case f.Status == app.Behind:
			behind = append(behind, f)
		case f.Status == app.NotHere && f.Master != "":
			missing = append(missing, f)
		}
	}
	if len(behind)+len(missing) == 0 {
		m.dialog = message("Pull all of "+root, "Nothing to pull: no folder under it is behind master or missing here.")
		return m, nil
	}
	confirm := func(m Model, rootDir string) (tea.Model, tea.Cmd) {
		var parts []string
		if len(behind) > 0 {
			parts = append(parts, fmt.Sprintf("%d behind master", len(behind)))
		}
		if len(missing) > 0 {
			parts = append(parts, fmt.Sprintf("%d not on this client yet", len(missing)))
		}
		total := len(behind) + len(missing)
		m.dialog = &dialog{
			title:   fmt.Sprintf("Pull %d folders under %s?", total, root),
			text:    strings.Join(parts, ", ") + ". Folders that changed here are left alone. Each folder's current state is saved as a snapshot first.",
			confirm: true,
			done: func(m Model, _ string) (tea.Model, tea.Cmd) {
				busy := fmt.Sprintf("pulling %d folders under %s", total, root)
				return m.start(busy, "pull all of "+root, func(ctx context.Context) (string, error) {
					scope := app.Scope{Root: root}
					if _, known := m.back.RootDir(root); !known {
						scope.NewRoot, scope.NewRootDir = root, rootDir
					}
					return all(m.back.PullAll(ctx, scope))
				})
			},
		}
		return m, nil
	}
	if len(missing) == 0 {
		return confirm(m, "")
	}
	return m.withRoot(root, confirm)
}

// askPushAll confirms pushing every folder of the selected root that has
// a snapshot master does not.
func (m Model) askPushAll() (tea.Model, tea.Cmd) {
	root := m.selected().root
	var ahead, changed int
	for _, f := range m.under(root) {
		switch f.Status {
		case app.Ahead, app.Diverged:
			ahead++
		case app.Changed:
			changed++
		}
	}
	if ahead+changed == 0 {
		m.dialog = message("Push all of "+root, "Nothing to push: no folder under it has a snapshot master does not.")
		return m, nil
	}
	var parts []string
	if ahead > 0 {
		parts = append(parts, fmt.Sprintf("Makes the latest snapshot of %s master.", plural(ahead, "folder")))
	}
	if changed > 0 {
		parts = append(parts, fmt.Sprintf("%s changed since the latest snapshot, and fail until that is snapshotted.", plural(changed, "folder")))
	}
	text := strings.Join(parts, " ")
	m.dialog = &dialog{
		title:   "Push all of " + root + "?",
		text:    text,
		confirm: true,
		done: func(m Model, _ string) (tea.Model, tea.Cmd) {
			return m.start("pushing the folders under "+root, "push all of "+root, func(ctx context.Context) (string, error) {
				return all(m.back.PushAll(ctx, app.Scope{Root: root}))
			})
		},
	}
	return m, nil
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}

// chooseRestore moves into the log, where a snapshot is picked.
func (m Model) chooseRestore() (tea.Model, tea.Cmd) {
	if len(m.selected().snapshots()) == 0 {
		m.note = "there is no snapshot to restore yet"
		return m, nil
	}
	m.focus = log
	m.note = "choose a snapshot with j and k, then r to restore it; h goes back"
	return m, nil
}

// askRestore asks how to restore the folder to the snapshot selected in
// the log, saving its current state first or not, and confirms it.
func (m Model) askRestore() (tea.Model, tea.Cmd) {
	f := m.selected().folder
	if f == nil || m.logSel >= len(f.Snapshots) {
		return m, nil
	}
	if f.Dir == "" {
		m.note = "this folder is not on this client; pull it first"
		return m, nil
	}
	s := f.Snapshots[m.logSel]
	name := f.Folder.String()
	m.dialog = &dialog{
		title: "Restore " + name + " to " + s.ID[:8],
		menu:  []option{{"s", "save its current state first"}, {"H", "hard restore: lose its current state"}},
		done: func(m Model, key string) (tea.Model, tea.Cmd) {
			save := key == "s"
			text := "Makes " + f.Dir + " match " + app.Describe(s) + ", whatever state it is in. Its current state is saved as a snapshot first."
			if !save {
				text = "Makes " + f.Dir + " match " + app.Describe(s) + ", whatever state it is in. Its current state is not saved: what is not in a snapshot is lost."
			}
			m.dialog = &dialog{
				title:   "Restore " + name + "?",
				text:    text,
				confirm: true,
				done: func(m Model, _ string) (tea.Model, tea.Cmd) {
					return m.start("restoring "+name, "restore "+name, func(ctx context.Context) (string, error) {
						return restore(ctx, m.back, f.Folder, s.ID, save)
					})
				},
			}
			return m, nil
		},
	}
	return m, nil
}

// restore makes a folder match a snapshot, and says how it went.
func restore(ctx context.Context, back Backend, f db.Folder, id string, save bool) (string, error) {
	res, err := back.Restore(ctx, f, id, save)
	if err != nil {
		return "", err
	}
	if res.Already {
		return fmt.Sprintf("%s: already at %s", f, app.Brief(res.Target)), nil
	}
	note := fmt.Sprintf("restored %s to %s", f, res.Target.ID[:8])
	switch {
	case !save:
		note += "; its state before was not saved"
	case res.Undo != "":
		note += "; its state before is snapshot " + res.Undo[:8]
	}
	return note, nil
}

// askDelete confirms deleting the snapshot selected in the log, or says
// why it is kept.
func (m Model) askDelete() (tea.Model, tea.Cmd) {
	f := m.selected().folder
	if f == nil || m.logSel >= len(f.Snapshots) {
		return m, nil
	}
	s := f.Snapshots[m.logSel]
	name := f.Folder.String()
	if err := app.CheckDelete(f.Snapshots, s.ID, f.Base, f.Master); err != nil {
		m.dialog = message("Cannot delete "+s.ID[:8], "It is kept: "+err.Error()+".")
		return m, nil
	}
	m.dialog = &dialog{
		title:   "Delete a snapshot of " + name + "?",
		text:    "Removes " + app.Describe(s) + " from the repository, for every client. Its data stays until the repository is pruned.",
		confirm: true,
		done: func(m Model, _ string) (tea.Model, tea.Cmd) {
			return m.start("deleting "+s.ID[:8], "delete "+s.ID[:8], func(ctx context.Context) (string, error) {
				if err := m.back.Delete(ctx, f.Folder, s.ID); err != nil {
					return "", err
				}
				return fmt.Sprintf("deleted %s of %s", s.ID[:8], name), nil
			})
		},
	}
	return m, nil
}

// add starts syncing the directory selected in the file system. Its root
// is the known root it is under; without one, the root is asked for.
func (m Model) add() (tea.Model, tea.Cmd) {
	dir := m.path()
	if err := m.back.CheckAddable(dir); err != nil {
		m.dialog = message("Cannot add "+short(dir), err.Error())
		return m, nil
	}
	if name, rootDir, ok := m.back.RootAbove(dir); ok {
		return m.askAdd(dir, name, rootDir)
	}
	candidates := app.RootCandidates(dir)
	labels := make([]string, len(candidates))
	for i, c := range candidates {
		labels[i] = short(c)
	}
	m.dialog = &dialog{
		title:   "Which parent of " + filepath.Base(dir) + " is its root?",
		text:    "A folder is known to all clients by its root and its path under it. Each client maps the root to its own location.",
		choices: candidates,
		labels:  labels,
		done: func(m Model, rootDir string) (tea.Model, tea.Cmd) {
			return m.nameRoot(rootDir, filepath.Base(rootDir), func(m Model, name string) (tea.Model, tea.Cmd) {
				return m.askAdd(dir, name, rootDir)
			})
		},
	}
	return m, nil
}

// nameRoot goes on with what the root at rootDir is called: its name if
// this client knows it, else the suggested one, asking for another while a
// name cannot be used.
func (m Model) nameRoot(rootDir, suggested string, then func(Model, string) (tea.Model, tea.Cmd)) (tea.Model, tea.Cmd) {
	if name, ok := m.back.RootAt(rootDir); ok {
		return then(m, name)
	}
	problem := m.back.CheckRootName(suggested)
	if problem == "" {
		return then(m, suggested)
	}
	m.dialog = &dialog{
		title:   "Name for the root " + short(rootDir) + "?",
		text:    "Other clients know the root by this name.",
		typing:  true,
		typed:   suggested,
		problem: problem,
		done: func(m Model, typed string) (tea.Model, tea.Cmd) {
			return m.nameRoot(rootDir, typed, then)
		},
	}
	return m, nil
}

// askAdd confirms adding the directory under the root.
func (m Model) askAdd(dir, rootName, rootDir string) (tea.Model, tea.Cmd) {
	rel, err := filepath.Rel(rootDir, dir)
	if err != nil {
		m.dialog = message("Cannot add "+short(dir), err.Error())
		return m, nil
	}
	name := rootName + "/" + filepath.ToSlash(rel)
	m.dialog = &dialog{
		title:   "Add " + short(dir) + "?",
		text:    "It becomes " + name + ", with the root " + rootName + " at " + short(rootDir) + ". Nothing is uploaded until its first snapshot.",
		confirm: true,
		done: func(m Model, _ string) (tea.Model, tea.Cmd) {
			return m.start("adding "+name, "add "+short(dir), func(context.Context) (string, error) {
				f, err := m.back.Add(dir, rootName, rootDir)
				if err != nil {
					return "", err
				}
				return "added " + f.String() + ": s to take its first snapshot", nil
			})
		},
	}
	return m, nil
}
