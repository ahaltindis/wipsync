package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ahaltindis/wipsync/internal/db"
	"github.com/ahaltindis/wipsync/internal/master"
)

func TestPush(t *testing.T) {
	e := newEnv(t)
	repo := e.repo()
	const root, path = "Projects", "app"
	dir := filepath.Join(e.home, "Projects/app")
	e.write("Projects/app/a.txt", "one\n")
	must := func(args ...string) string {
		t.Helper()
		out, err := e.run("", args...)
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return out
	}
	must("add", dir)
	store := currentDB(t)
	ctx := context.Background()
	pointers, err := master.Open(repo)
	if err != nil {
		t.Fatal(err)
	}

	// Nothing to push yet.
	_, err = e.run("", "push", dir)
	if err == nil || !strings.Contains(err.Error(), "no snapshot from this client yet") {
		t.Errorf("err = %v", err)
	}

	// First push.
	must("snapshot", dir)
	first, _ := store.Base(root, path)
	out := must("push", dir)
	if !strings.HasPrefix(out, "Projects/app: master is now "+first[:8]+", the snapshot from ") ||
		!strings.Contains(out, "(just now) by client-a") {
		t.Errorf("unexpected output: %s", out)
	}
	if got, _ := pointers.Master(ctx, root, path); got != first {
		t.Errorf("server master = %q, want %q", got, first)
	}
	if got, _ := store.Master(root, path); got != first {
		t.Errorf("recorded master = %q, want %q", got, first)
	}
	if out := must("log", dir); !strings.Contains(out, "client-a  master, base") {
		t.Errorf("log does not mark master:\n%s", out)
	}

	// Pushing again changes nothing, and takes no snapshot of new edits.
	e.write("Projects/app/a.txt", "two!\n")
	if out := must("push", dir); !strings.Contains(out, "master is already "+first[:8]) {
		t.Errorf("unexpected output: %s", out)
	}
	if n := len(e.snapshots(repo)); n != 1 {
		t.Errorf("push took a snapshot: %d snapshots", n)
	}

	// A new snapshot moves master on the next push.
	must("snapshot", dir)
	second, _ := store.Base(root, path)
	if out := must("push", dir); !strings.Contains(out, "master is now "+second[:8]) {
		t.Errorf("unexpected output: %s", out)
	}
	log := strings.Split(strings.TrimSpace(must("log", dir)), "\n")
	if len(log) != 3 || !strings.HasSuffix(log[1], "master, base") || strings.Contains(log[2], "master") {
		t.Errorf("unexpected log:\n%s", strings.Join(log, "\n"))
	}

	// With master outside the listed snapshots, log still says where it is.
	if err := pointers.Move(ctx, root, path, second, first); err != nil {
		t.Fatal(err)
	}
	out = must("log", "-n", "1", dir)
	if !strings.Contains(out, "master is "+first[:8]+"  ") || !strings.Contains(out, "client-a  (older, not shown above)") {
		t.Errorf("log hides master:\n%s", out)
	}
	if strings.Contains(must("log", dir), "master is ") {
		t.Errorf("log repeats a master that is listed")
	}
	// A master that is none of the folder's snapshots.
	stray := strings.Repeat("d", 64)
	if err := pointers.Move(ctx, root, path, first, stray); err != nil {
		t.Fatal(err)
	}
	if out := must("log", dir); !strings.Contains(out, "master is dddddddd, which is not among this folder's snapshots") {
		t.Errorf("unexpected log:\n%s", out)
	}
	if err := pointers.Move(ctx, root, path, stray, second); err != nil {
		t.Fatal(err)
	}

	// Another client pushes in between: this client's push is refused.
	theirs := strings.Repeat("c", 64)
	if err := pointers.Move(ctx, root, path, second, theirs); err != nil {
		t.Fatal(err)
	}
	e.write("Projects/app/a.txt", "three\n")
	must("snapshot", dir)
	_, err = e.run("", "push", dir)
	if err == nil || !strings.Contains(err.Error(), "another client moved master to cccccccc; pull first") {
		t.Errorf("err = %v", err)
	}
	if got, _ := pointers.Master(ctx, root, path); got != theirs {
		t.Errorf("a refused push changed master to %q", got)
	}
	if got, _ := store.Master(root, path); got != second {
		t.Errorf("a refused push changed the recorded master to %q", got)
	}

	// A push that reached the server but was cut off before recording it
	// here: master is already this client's snapshot.
	third, _ := store.Base(root, path)
	if err := pointers.Move(ctx, root, path, theirs, third); err != nil {
		t.Fatal(err)
	}
	if out := must("push", dir); !strings.Contains(out, "master is already "+third[:8]) {
		t.Errorf("unexpected output: %s", out)
	}
	if got, _ := store.Master(root, path); got != third {
		t.Errorf("recorded master = %q, want %q", got, third)
	}
}

func TestPushUnsupportedRepository(t *testing.T) {
	e := newEnv(t)
	e.repo()
	dir := e.mkdir("Projects/app")
	if out, err := e.run("", "add", dir); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	e.set(func(s *db.Settings) { s.Repository = "rest:https://example.invalid/repo" })
	_, err := e.run("", "push", dir)
	if err == nil || !strings.Contains(err.Error(), "not rest:") {
		t.Errorf("err = %v", err)
	}
}
