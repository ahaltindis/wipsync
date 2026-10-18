package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// The way out after a refused pull: give up the local changes, take
// master, and be able to push again.
func TestRestoreMasterAfterDrift(t *testing.T) {
	a := newEnv(t)
	a.repo()
	a.write("Projects/app/main.py", "print(1)\n")
	a.write("Projects/app/.venv/lib.py", "a's own\n")
	dirA := filepath.Join(a.home, "Projects/app")
	a.must("", "add", dirA)
	a.must("", "snapshot", dirA)
	a.must("", "push", dirA)

	b := a.secondClient("client-b")
	b.must("\n", "pull", "Projects/app")
	dirB := filepath.Join(b.home, "Projects/app")
	b.write("Projects/app/main.py", "print('b')\n")
	b.write("Projects/app/b-only.txt", "from b\n")
	b.must("", "snapshot", dirB)
	b.must("", "push", dirB)
	masterID := b.base("Projects", "app")

	// client-a drifted: an edit and a file of its own, not snapshotted.
	a.write("Projects/app/main.py", "print('a')\n")
	a.write("Projects/app/a-only.txt", "from a\n")
	a.use()
	_, err := a.run("", "pull", dirA)
	if err == nil || !strings.Contains(err.Error(), "wipsync restore "+masterID[:8]) {
		t.Fatalf("the refusal does not say how to go on: %v", err)
	}
	drifted := a.base("Projects", "app")

	out := a.must("", "restore", "master", dirA)
	if !strings.Contains(out, "restored Projects/app to "+masterID[:8]) || !strings.Contains(out, "by client-b") ||
		!strings.Contains(out, "to undo: wipsync restore "+drifted[:8]) {
		t.Errorf("unexpected output:\n%s", out)
	}
	if strings.Contains(out, "saved its current state") {
		t.Errorf("the pull had saved the state already:\n%s", out)
	}
	if got := a.read("Projects/app/main.py"); got != "print('b')\n" {
		t.Errorf("main.py = %q", got)
	}
	if !a.exists("Projects/app/b-only.txt") || a.exists("Projects/app/a-only.txt") {
		t.Error("the folder does not match master")
	}
	if got := a.read("Projects/app/.venv/lib.py"); got != "a's own\n" {
		t.Errorf("the ignored .venv was touched: %q", got)
	}

	// In step with master again: work continues and can be pushed.
	store := currentDB(t)
	if m, _ := store.Master("Projects", "app"); m != masterID {
		t.Errorf("recorded master = %q, want %q", m, masterID)
	}
	if out := a.must("", "snapshot", dirA); !strings.Contains(out, "no changes") {
		t.Errorf("snapshot after the restore: %s", out)
	}
	a.write("Projects/app/main.py", "print('a again')\n")
	a.must("", "snapshot", dirA)
	if out := a.must("", "push", dirA); !strings.Contains(out, "master is now ") {
		t.Errorf("push after the restore: %s", out)
	}

	// The drifted state was kept, and restoring it brings everything back.
	a.must("", "restore", drifted[:8], dirA)
	if got := a.read("Projects/app/main.py"); got != "print('a')\n" {
		t.Errorf("main.py = %q", got)
	}
	if !a.exists("Projects/app/a-only.txt") || a.exists("Projects/app/b-only.txt") {
		t.Error("the folder does not match the drifted snapshot")
	}
}

func TestRestoreUndoesPull(t *testing.T) {
	a := newEnv(t)
	a.repo()
	a.write("Projects/app/main.py", "print(1)\n")
	dirA := filepath.Join(a.home, "Projects/app")
	a.must("", "add", dirA)
	a.must("", "snapshot", dirA)
	a.must("", "push", dirA)
	before := a.base("Projects", "app")

	b := a.secondClient("client-b")
	b.must("\n", "pull", "Projects/app")
	dirB := filepath.Join(b.home, "Projects/app")
	b.write("Projects/app/main.py", "print('b')\n")
	b.must("", "snapshot", dirB)
	b.must("", "push", dirB)
	a.must("", "pull", dirA)
	pulled := a.base("Projects", "app")

	// Run from inside the folder, naming only the snapshot.
	a.use()
	t.Chdir(dirA)
	out := a.must("", "restore", before[:8])
	if !strings.Contains(out, "restored Projects/app to "+before[:8]) || !strings.Contains(out, "to undo: wipsync restore "+pulled[:8]) {
		t.Errorf("unexpected output:\n%s", out)
	}
	if got := a.read("Projects/app/main.py"); got != "print(1)\n" {
		t.Errorf("main.py = %q", got)
	}
	if got := a.base("Projects", "app"); got != before {
		t.Errorf("base = %q, want %q", got, before)
	}
	// Already there.
	if out := a.must("", "restore", before[:8]); !strings.Contains(out, "already at "+before[:8]) {
		t.Errorf("unexpected output:\n%s", out)
	}
	// Unsaved edits are saved before a restore replaces them.
	a.write("Projects/app/main.py", "unsaved\n")
	out = a.must("", "restore", pulled[:8])
	if !strings.Contains(out, "saved its current state as snapshot ") {
		t.Errorf("unexpected output:\n%s", out)
	}
	if got := a.read("Projects/app/main.py"); got != "print('b')\n" {
		t.Errorf("main.py = %q", got)
	}
	undo := out[strings.Index(out, "to undo: wipsync restore ")+len("to undo: wipsync restore "):]
	a.must("", "restore", strings.TrimSpace(undo))
	if got := a.read("Projects/app/main.py"); got != "unsaved\n" {
		t.Errorf("main.py = %q", got)
	}
}

func TestRestoreRefusals(t *testing.T) {
	e := newEnv(t)
	repo := e.repo()
	e.write("Projects/app/a.txt", "one\n")
	e.write("Projects/other/b.txt", "two\n")
	app, other := filepath.Join(e.home, "Projects/app"), filepath.Join(e.home, "Projects/other")
	e.must("", "add", app)
	e.must("", "add", other)
	e.must("", "snapshot", app)
	e.must("", "snapshot", other)
	otherID := e.base("Projects", "other")
	e.write("Projects/app/a.txt", "unsaved\n")
	snapshots := len(e.snapshots(repo))

	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"no master":         {[]string{"restore", "master", app}, "has no master yet"},
		"unknown ID":        {[]string{"restore", "zzzz", app}, `no snapshot of Projects/app starts with "zzzz"`},
		"another folder's":  {[]string{"restore", otherID[:8], app}, "no snapshot of Projects/app starts with"},
		"not in a folder":   {[]string{"restore", otherID[:8], e.home}, "not in a synced folder"},
		"nothing to act on": {[]string{"restore"}, "arg"},
	} {
		_, err := e.run("", tc.args...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	// A refused restore neither touched the folder nor took a snapshot.
	if got := e.read("Projects/app/a.txt"); got != "unsaved\n" {
		t.Errorf("a.txt = %q", got)
	}
	if n := len(e.snapshots(repo)); n != snapshots {
		t.Errorf("%d snapshots, want %d", n, snapshots)
	}
}

func TestFindSnapshotAmbiguous(t *testing.T) {
	e := newEnv(t)
	e.repo()
	e.write("Projects/app/a.txt", "one\n")
	app := filepath.Join(e.home, "Projects/app")
	e.must("", "add", app)
	e.must("", "snapshot", app)
	e.write("Projects/app/a.txt", "two!\n")
	e.must("", "snapshot", app)
	// The empty prefix matches both snapshots.
	_, err := e.run("", "restore", "", app)
	if err == nil || !strings.Contains(err.Error(), "2 snapshots of Projects/app start with") {
		t.Errorf("err = %v", err)
	}
}
