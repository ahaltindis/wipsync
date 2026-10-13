package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ahaltindis/wipsync/internal/state"
)

// secondClient returns another client of the same repository.
func (e *env) secondClient(name string) *env {
	e.t.Helper()
	first := e.config()
	b := newEnv(e.t)
	c := b.config()
	c.Client = name
	c.Repository = first.Repository
	c.PasswordFile = first.PasswordFilePath()
	if err := c.Save(b.cfg); err != nil {
		e.t.Fatal(err)
	}
	return b
}

func (e *env) must(stdin string, args ...string) string {
	e.t.Helper()
	e.use()
	out, err := e.run(stdin, args...)
	if err != nil {
		e.t.Fatalf("%v: %v\n%s", args, err, out)
	}
	return out
}

func (e *env) read(rel string) string {
	e.t.Helper()
	data, err := os.ReadFile(filepath.Join(e.home, rel))
	if err != nil {
		e.t.Fatal(err)
	}
	return string(data)
}

// A folder made on one client is fetched on a second one, worked on there,
// and pushed back.
func TestPullOnNewClient(t *testing.T) {
	a := newEnv(t)
	repo := a.repo()
	a.write("Projects/Freelance/app/main.py", "print(1)\n")
	a.write("Projects/Freelance/app/sub/data.txt", "data\n")
	a.write("Projects/Freelance/app/.venv/lib.py", "not synced\n")
	dirA := filepath.Join(a.home, "Projects/Freelance/app")
	a.must("\n", "add", dirA)
	a.must("", "snapshot", dirA)
	a.must("", "push", dirA)
	const name = "Projects/Freelance/app"

	b := a.secondClient("client-b")
	if out := b.must("", "list"); !strings.HasPrefix(out, name+"\n") || !strings.Contains(out, "[client-b]  not on this client yet") {
		t.Errorf("unexpected list:\n%s", out)
	}

	// Enter accepts the suggested location for the root.
	out := b.must("\n", "pull", name)
	rootB := filepath.Join(b.home, "Projects")
	dirB := filepath.Join(rootB, "Freelance/app")
	if !strings.Contains(out, "Where is Projects on this machine? ["+rootB+"]") ||
		!strings.Contains(out, "pulled "+name+" into "+dirB+": ") || !strings.Contains(out, "by client-a") {
		t.Errorf("unexpected output:\n%s", out)
	}
	if got := b.read("Projects/Freelance/app/main.py"); got != "print(1)\n" {
		t.Errorf("main.py = %q", got)
	}
	if got := b.read("Projects/Freelance/app/sub/data.txt"); got != "data\n" {
		t.Errorf("sub/data.txt = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dirB, ".venv")); err == nil {
		t.Error("the ignored .venv was pulled")
	}
	if info, _ := os.Stat(dirB); info.Mode().Perm() != 0o755 {
		t.Errorf("folder mode = %o, want 755", info.Mode().Perm())
	}

	// The client now knows the root and the folder, with the same ignore rules.
	c := b.config()
	if c.Roots["Projects"] != rootB || len(c.Folders) != 1 || c.Folders[0].String() != name {
		t.Errorf("config = %+v", c)
	}
	if strings.Join(c.Folders[0].Ignore, " ") != strings.Join(a.config().Folders[0].Ignore, " ") {
		t.Errorf("ignore rules = %v", c.Folders[0].Ignore)
	}
	pulled := a.snapshots(repo)[0].ID
	store, _ := state.Default()
	if base, _ := store.Base("Projects", "Freelance/app"); base != pulled {
		t.Errorf("base = %q, want %q", base, pulled)
	}
	if m, _ := store.Master("Projects", "Freelance/app"); m != pulled {
		t.Errorf("recorded master = %q, want %q", m, pulled)
	}
	if out := b.must("", "list"); !strings.HasPrefix(out, name+"    "+dirB+"\n") || !strings.Contains(out, "[client-b]  no snapshot yet") {
		t.Errorf("unexpected list:\n%s", out)
	}
	if out := b.must("", "log", dirB); !strings.Contains(out, "client-a  master, base") {
		t.Errorf("unexpected log:\n%s", out)
	}

	// Nothing changed since the pull, so there is nothing to snapshot.
	if out := b.must("", "snapshot", dirB); !strings.Contains(out, "no changes") {
		t.Errorf("snapshot right after the pull: %s", out)
	}
	if n := len(a.snapshots(repo)); n != 1 {
		t.Errorf("%d snapshots after an unchanged pull, want 1", n)
	}

	// Work on the second client is saved with the pulled snapshot as its
	// parent, and goes back as master.
	b.write("Projects/Freelance/app/main.py", "print(2222)\n")
	if out := b.must("", "snapshot", dirB); !strings.Contains(out, "0 new, 1 changed") {
		t.Errorf("snapshot after an edit: %s", out)
	}
	all := a.snapshots(repo)
	if len(all) != 2 || all[1].Hostname != "client-b" || all[1].Parent != pulled {
		t.Fatalf("snapshots = %+v", all)
	}
	if out := b.must("", "push", dirB); !strings.Contains(out, "master is now ") || !strings.Contains(out, "by client-b") {
		t.Errorf("unexpected push output: %s", out)
	}

	// The first client is behind now, and its next push is refused.
	a.write("Projects/Freelance/app/main.py", "print(3)\n")
	a.must("", "snapshot", dirA)
	a.use()
	if _, err := a.run("", "push", dirA); err == nil || !strings.Contains(err.Error(), "pull first") {
		t.Errorf("err = %v", err)
	}
}

func TestPullFromClient(t *testing.T) {
	a := newEnv(t)
	a.repo()
	a.write("Projects/app/a.txt", "one\n")
	dirA := filepath.Join(a.home, "Projects/app")
	a.must("", "add", dirA)
	a.must("", "snapshot", dirA)
	a.write("Projects/app/a.txt", "two!\n")
	a.must("", "snapshot", dirA)

	b := a.secondClient("client-b")
	b.use()
	// Nothing was pushed, so there is no master to take.
	_, err := b.run("", "pull", "Projects/app", "--root", filepath.Join(b.home, "code"))
	if err == nil || !strings.Contains(err.Error(), "has no master yet") || !strings.Contains(err.Error(), "client-a") {
		t.Errorf("err = %v", err)
	}
	_, err = b.run("", "pull", "Projects/app", "--from", "nobody", "--root", filepath.Join(b.home, "code"))
	if err == nil || !strings.Contains(err.Error(), `no snapshots from client "nobody"`) {
		t.Errorf("err = %v", err)
	}
	if len(b.config().Folders) != 0 {
		t.Error("a failed pull added the folder")
	}

	// The client's latest snapshot, into a root at another location.
	b.must("", "pull", "Projects/app", "--from", "client-a", "--root", filepath.Join(b.home, "code"))
	if got := b.read("code/app/a.txt"); got != "two!\n" {
		t.Errorf("a.txt = %q", got)
	}
	if got := b.config().Roots["Projects"]; got != filepath.Join(b.home, "code") {
		t.Errorf("root = %q", got)
	}
	store, _ := state.Default()
	if m, _ := store.Master("Projects", "app"); m != "" {
		t.Errorf("recorded master = %q after pulling a client's snapshot", m)
	}
}

func TestPullRefusals(t *testing.T) {
	a := newEnv(t)
	a.repo()
	a.write("Projects/app/a.txt", "one\n")
	a.write("Projects/other/b.txt", "two\n")
	dirA := filepath.Join(a.home, "Projects/app")
	a.must("", "add", dirA)
	a.must("", "add", filepath.Join(a.home, "Projects/other"))
	a.must("", "snapshot", dirA)
	a.must("", "push", dirA)
	a.must("", "snapshot", "Projects/other")
	a.must("", "push", "Projects/other")

	// On the client that already has it.
	_, err := a.run("", "pull", "Projects/app")
	if err == nil || !strings.Contains(err.Error(), "already on this client") {
		t.Errorf("err = %v", err)
	}

	b := a.secondClient("client-b")
	b.use()
	for arg, want := range map[string]string{
		"Projects/nothing": "no snapshots of Projects/nothing",
		"app":              "not a folder name",
	} {
		if _, err := b.run("", "pull", arg); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("pull %s: err = %v", arg, err)
		}
	}

	// A directory with something in it is not replaced.
	b.write("Projects/app/mine.txt", "precious\n")
	_, err = b.run("\n", "pull", "Projects/app")
	if err == nil || !strings.Contains(err.Error(), "exists and is not empty") {
		t.Errorf("err = %v", err)
	}
	if got := b.read("Projects/app/mine.txt"); got != "precious\n" {
		t.Errorf("mine.txt = %q", got)
	}
	if len(b.config().Folders) != 0 || len(b.config().Roots) != 0 {
		t.Errorf("a refused pull changed the config: %+v", b.config())
	}

	// Once the root is known, a second folder under it is not asked about.
	out := b.must("\n", "pull", "Projects/other")
	if !strings.Contains(out, "Where is Projects") {
		t.Errorf("did not ask for the root:\n%s", out)
	}
	if err := os.RemoveAll(filepath.Join(b.home, "Projects/app")); err != nil {
		t.Fatal(err)
	}
	if out := b.must("", "pull", "Projects/app"); strings.Contains(out, "Where is") {
		t.Errorf("asked for a root that is known:\n%s", out)
	}
	// And the root cannot be placed somewhere else.
	_, err = b.run("", "pull", "Projects/app", "--root", filepath.Join(b.home, "elsewhere"))
	if err == nil {
		t.Error("pulled a folder a second time")
	}
}

func TestList(t *testing.T) {
	a := newEnv(t)
	repo := a.repo()
	if out := a.must("", "list"); out != "no folders yet\n" {
		t.Errorf("empty list: %q", out)
	}
	a.write("Projects/app/a.txt", "one\n")
	a.write("Projects/tool/b.txt", "two\n")
	appA, toolA := filepath.Join(a.home, "Projects/app"), filepath.Join(a.home, "Projects/tool")
	a.must("", "add", appA)
	a.must("", "add", toolA)
	a.must("", "snapshot", appA)
	a.must("", "push", appA)
	a.must("", "snapshot", toolA)

	// line is how a snapshot shows up: its short ID and its time.
	line := func(s snapshot) string {
		return s.ID[:8] + "  " + s.Time.Local().Format("2006-01-02 15:04")
	}
	check := func(e *env, want string) {
		t.Helper()
		if got := e.must("", "list"); got != want {
			t.Errorf("list as %s:\n%s\nwant:\n%s", e.config().Client, got, want)
		}
	}
	snaps := a.snapshots(repo)
	app1, tool1 := snaps[0], snaps[1]

	// A client that has neither folder; one folder has no master.
	b := a.secondClient("client-b")
	check(b, "Projects/app\n"+
		"  client-a    "+line(app1)+"  master\n"+
		"  [client-b]  not on this client yet\n"+
		"\n"+
		"Projects/tool\n"+
		"  client-a    "+line(tool1)+"\n"+
		"  [client-b]  not on this client yet\n"+
		"  *           no master yet\n")

	// The same repository seen from the client that made them.
	check(a, "Projects/app     "+appA+"\n"+
		"  [client-a]  "+line(app1)+"  master\n"+
		"\n"+
		"Projects/tool    "+toolA+"\n"+
		"  [client-a]  "+line(tool1)+"\n"+
		"  *           no master yet\n")

	// client-b pulls app and works on it, and client-a moves on without
	// pushing: master is now an older snapshot of client-a, on its own line.
	b.must("\n", "pull", "Projects/app")
	appB := filepath.Join(b.home, "Projects/app")
	check(b, "Projects/app    "+appB+"\n"+
		"  client-a    "+line(app1)+"  master\n"+
		"  [client-b]  no snapshot yet\n"+
		"\n"+
		"Projects/tool\n"+
		"  client-a    "+line(tool1)+"\n"+
		"  [client-b]  not on this client yet\n"+
		"  *           no master yet\n")
	b.write("Projects/app/a.txt", "from b\n")
	b.must("", "snapshot", appB)
	a.write("Projects/app/a.txt", "from a\n")
	a.must("", "snapshot", appA)
	snaps = a.snapshots(repo)
	appB1, appA2 := snaps[2], snaps[3]
	check(b, "Projects/app    "+appB+"\n"+
		"  client-a    "+line(appA2)+"\n"+
		"  [client-b]  "+line(appB1)+"\n"+
		"  client-a    "+line(app1)+"  master\n"+
		"\n"+
		"Projects/tool\n"+
		"  client-a    "+line(tool1)+"\n"+
		"  [client-b]  not on this client yet\n"+
		"  *           no master yet\n")
	// From client-a the duplicated line is its own.
	if out := a.must("", "list"); !strings.Contains(out, "  [client-a]  "+line(appA2)+"\n"+
		"  client-b    "+line(appB1)+"\n"+
		"  [client-a]  "+line(app1)+"  master\n") {
		t.Errorf("list as client-a:\n%s", out)
	}
}
