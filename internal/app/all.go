package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/ahaltindis/wipsync/internal/db"
)

// Scope is what PullAll and PushAll act on: every folder, or those under
// one root, or the ones named.
type Scope struct {
	Root    string
	Folders []db.Folder

	// NewRoot is placed at NewRootDir on this client, so that folders
	// under it that are not here yet can be pulled.
	NewRoot, NewRootDir string
}

func (s Scope) has(f db.Folder) bool {
	if s.Root != "" && f.Root != s.Root {
		return false
	}
	if s.Folders == nil {
		return true
	}
	for _, g := range s.Folders {
		if g.Root == f.Root && g.Path == f.Path {
			return true
		}
	}
	return false
}

// AllResult is how a pull or push of many folders went. The CLI, the TUI
// and the service all run PullAll and PushAll, and show what these say.
type AllResult struct {
	Verb string // "pull" or "push"
	Root string // the root it was limited to, if any

	Lines   []string // what happened, folder by folder
	Done    int      // the folders it went through for
	Skipped int      // the folders left alone, each with a line on why
	Failed  []string // a line for each folder it failed for; these are in Lines too
}

func (r *AllResult) fail(f db.Folder, err error) {
	line := err.Error()
	if !strings.HasPrefix(line, f.String()+":") {
		line = f.String() + ": " + line
	}
	r.Lines = append(r.Lines, line)
	r.Failed = append(r.Failed, line)
}

func (r *AllResult) skip(line string) {
	r.Lines = append(r.Lines, line)
	r.Skipped++
}

// Summary is the result in one line, such as "pulled 2 of 3 folders under
// Projects, 1 failed".
func (r AllResult) Summary() string {
	scope := ""
	if r.Root != "" {
		scope = " under " + r.Root
	}
	n := r.Done + len(r.Failed)
	var s string
	switch {
	case n == 0:
		s = "nothing to " + r.Verb + scope
	case len(r.Failed) == 0:
		s = fmt.Sprintf("%sed %s%s", r.Verb, folders(n), scope)
	default:
		s = fmt.Sprintf("%sed %d of %s%s, %d failed", r.Verb, r.Done, folders(n), scope, len(r.Failed))
	}
	if r.Skipped > 0 {
		s += fmt.Sprintf("; %s left alone", folders(r.Skipped))
	}
	return s
}

// Err is nil when no folder failed.
func (r AllResult) Err() error {
	if len(r.Failed) == 0 {
		return nil
	}
	return fmt.Errorf("%d of %s failed", len(r.Failed), folders(r.Done+len(r.Failed)))
}

func folders(n int) string {
	if n == 1 {
		return "1 folder"
	}
	return fmt.Sprintf("%d folders", n)
}

// PullAll brings master into every folder in scope that is behind it, and
// into those that are not here yet. One that is not here goes under its
// root's directory on this client. Folders that changed on both sides,
// and those whose root has no directory here, are left alone.
func (a *App) PullAll(ctx context.Context, scope Scope) (AllResult, error) {
	res := AllResult{Verb: "pull", Root: scope.Root}
	if scope.NewRoot != "" {
		if _, known := a.RootDir(scope.NewRoot); !known {
			if err := a.CheckNewRoot(scope.NewRoot, scope.NewRootDir); err != nil {
				return res, err
			}
		}
	}
	world, err := a.survey(ctx)
	if err != nil {
		return res, err
	}
	for _, st := range world.Folders {
		f := st.Folder
		if !scope.has(f) {
			continue
		}
		switch {
		case st.Status == Behind:
			pr, err := a.Pull(ctx, f, "")
			if err != nil {
				if l := pr.SavedLine(f); l != "" {
					res.Lines = append(res.Lines, l)
				}
				res.fail(f, err)
				continue
			}
			res.Lines = append(res.Lines, pr.Line(f))
			res.Done++
		case st.Status == Diverged:
			res.skip(f.String() + ": changed here as well as on master; pull it on its own")
		case st.Status == NotHere && st.Master != "":
			dir, ok := a.RootDir(f.Root)
			if !ok && f.Root == scope.NewRoot {
				dir, ok = scope.NewRootDir, true
			}
			if !ok {
				res.skip(fmt.Sprintf("%s: not on this client, and %s has no directory here; pull it on its own to choose one", f, f.Root))
				continue
			}
			pr, err := a.fetch(ctx, f, dir)
			if err != nil {
				res.fail(f, err)
				continue
			}
			res.Lines = append(res.Lines, pr.Line(f))
			res.Done++
		}
	}
	return res, nil
}

func (a *App) fetch(ctx context.Context, f db.Folder, rootDir string) (PullResult, error) {
	t, err := a.FindTarget(ctx, f, "")
	if err != nil {
		return PullResult{}, err
	}
	return a.Fetch(ctx, f, rootDir, t)
}

// PushAll makes the latest snapshot of every folder here in scope master,
// where it is not yet. A folder
// that changed since its latest snapshot fails: that snapshot does not
// have the change, and a later push takes it once it is snapshotted. So
// does one whose master another client moved.
func (a *App) PushAll(ctx context.Context, scope Scope) (AllResult, error) {
	res := AllResult{Verb: "push", Root: scope.Root}
	world, err := a.survey(ctx)
	if err != nil {
		return res, err
	}
	for _, st := range world.Folders {
		f := st.Folder
		if st.Dir == "" || !scope.has(f) {
			continue
		}
		switch st.Status {
		case Changed:
			res.fail(f, fmt.Errorf("%s: changed since its latest snapshot, so not pushed; push again once it is snapshotted", f))
		case Ahead, Diverged:
			// A diverged folder is refused by the server, which says why.
			pr, err := a.Push(ctx, f)
			if err != nil {
				res.fail(f, err)
				continue
			}
			res.Lines = append(res.Lines, pr.Line(f))
			res.Done++
		}
	}
	return res, nil
}

// survey is Survey for acting on: without the masters, what is due is not
// known.
func (a *App) survey(ctx context.Context) (World, error) {
	world, err := a.Survey(ctx)
	if err == nil && world.MasterErr != nil {
		err = fmt.Errorf("cannot read the masters: %w", world.MasterErr)
	}
	return world, err
}

// Line says how a push went.
func (r PushResult) Line(f db.Folder) string {
	state := "now"
	if r.Already {
		state = "already"
	}
	return fmt.Sprintf("%s: master is %s %s", f, state, Brief(r.Snapshot))
}

// Line says how a pull went.
func (r PullResult) Line(f db.Folder) string {
	var s string
	switch {
	case r.Outcome == AlreadyThere:
		s = fmt.Sprintf("%s: already at %s", f, Brief(r.Target))
	case r.Outcome == AheadOfTarget:
		s = fmt.Sprintf("%s: nothing to pull, this client is ahead of %s", f, Brief(r.Target))
	case r.New:
		s = fmt.Sprintf("pulled %s into %s: %s", f, r.Dir, Brief(r.Target))
	default:
		s = fmt.Sprintf("pulled %s: %s", f, Brief(r.Target))
	}
	if r.IgnoreChanged {
		s += "; its ignore rules are now " + strings.Join(r.Ignore, ", ")
	}
	return s + r.saved()
}

// SavedLine says what a pull that failed saved first, if anything.
func (r PullResult) SavedLine(f db.Folder) string {
	if r.Saved == "" {
		return ""
	}
	return f.String() + ":" + strings.TrimPrefix(r.saved(), ";")
}

func (r PullResult) saved() string {
	if r.Saved == "" {
		return ""
	}
	return "; its state before is saved as snapshot " + r.Saved[:8]
}
