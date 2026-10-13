package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ahaltindis/wipsync/internal/state"
)

// repo creates an empty restic repository and points the config at it, or
// skips the test without restic.
func (e *env) repo() string {
	e.t.Helper()
	if _, err := exec.LookPath("restic"); err != nil {
		e.t.Skip("restic is not installed")
	}
	base := filepath.Dir(e.cfg)
	repo, pass := filepath.Join(base, "repo"), filepath.Join(base, "pass")
	if err := os.WriteFile(pass, []byte("test\n"), 0o600); err != nil {
		e.t.Fatal(err)
	}
	e.restic(repo, pass, "init")

	c := e.config()
	c.Client = "client-a"
	c.Repository = repo
	c.PasswordFile = pass
	if err := c.Save(e.cfg); err != nil {
		e.t.Fatal(err)
	}
	return repo
}

func (e *env) restic(repo, pass string, args ...string) []byte {
	e.t.Helper()
	cmd := exec.Command("restic", append([]string{"-r", repo, "--password-file", pass}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		e.t.Fatalf("restic %v: %v\n%s", args, err, out)
	}
	return out
}

type snapshot struct {
	ID       string    `json:"id"`
	Time     time.Time `json:"time"`
	Parent   string    `json:"parent"`
	Hostname string    `json:"hostname"`
	Tags     []string  `json:"tags"`
	Excludes []string  `json:"excludes"`
}

func (e *env) snapshots(repo string) []snapshot {
	e.t.Helper()
	var list []snapshot
	out := e.restic(repo, filepath.Join(filepath.Dir(e.cfg), "pass"), "snapshots", "--json")
	if err := json.Unmarshal(out, &list); err != nil {
		e.t.Fatal(err)
	}
	return list
}

func (e *env) write(rel, content string) {
	e.t.Helper()
	file := filepath.Join(e.home, rel)
	e.mkdir(filepath.Dir(rel))
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func TestSnapshot(t *testing.T) {
	e := newEnv(t)
	repo := e.repo()
	e.write("Projects/Freelance/app/main.py", "print(1)\n")
	e.write("Projects/Freelance/app/.venv/lib.py", "ignored\n")
	dir := filepath.Join(e.home, "Projects/Freelance/app")
	if out, err := e.run("\n", "add", dir); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	// First snapshot.
	out, err := e.run("", "snapshot", dir)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.HasPrefix(out, "Projects/Freelance/app: snapshot ") || !strings.Contains(out, "1 new, 0 changed") {
		t.Errorf("unexpected output: %s", out)
	}
	list := e.snapshots(repo)
	if len(list) != 1 {
		t.Fatalf("%d snapshots, want 1", len(list))
	}
	first := list[0]
	if first.Hostname != "client-a" || strings.Join(first.Tags, " ") != "root=Projects path=Freelance/app" {
		t.Errorf("snapshot = %+v", first)
	}
	if len(first.Excludes) == 0 {
		t.Error("the ignore rules were not passed to restic")
	}
	store, _ := state.Default()
	if base, _ := store.Base("Projects", "Freelance/app"); base != first.ID {
		t.Errorf("base = %q, want %q", base, first.ID)
	}

	// Nothing changed: no snapshot, addressed as Root/path this time.
	out, err = e.run("", "snapshot", "Projects/Freelance/app")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if out != "Projects/Freelance/app: no changes\n" {
		t.Errorf("unexpected output: %s", out)
	}
	if n := len(e.snapshots(repo)); n != 1 {
		t.Errorf("%d snapshots after an unchanged run, want 1", n)
	}

	// A change gives a snapshot whose parent is the first one.
	e.write("Projects/Freelance/app/main.py", "print(2222)\n")
	out, err = e.run("", "snapshot", filepath.Join(dir, ".venv"))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "0 new, 1 changed") {
		t.Errorf("unexpected output: %s", out)
	}
	list = e.snapshots(repo)
	if len(list) != 2 || list[1].Parent != first.ID {
		t.Fatalf("snapshots = %+v", list)
	}
	if base, _ := store.Base("Projects", "Freelance/app"); base != list[1].ID {
		t.Errorf("base = %q, want %q", base, list[1].ID)
	}
}

func TestSnapshotErrors(t *testing.T) {
	e := newEnv(t)
	dir := e.mkdir("Projects/app")

	_, err := e.run("", "snapshot", dir)
	if err == nil || !strings.Contains(err.Error(), "not in a synced folder") {
		t.Errorf("unknown folder: err = %v", err)
	}

	if out, err := e.run("", "add", dir); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	_, err = e.run("", "snapshot", dir)
	if err == nil || !strings.Contains(err.Error(), "repository is not set in "+e.cfg) {
		t.Errorf("no repository: err = %v", err)
	}

	c := e.config()
	c.Repository = "/nowhere"
	if err := c.Save(e.cfg); err != nil {
		t.Fatal(err)
	}
	_, err = e.run("", "snapshot", dir)
	if err == nil || !strings.Contains(err.Error(), "neither password_file nor password_command") {
		t.Errorf("no password: err = %v", err)
	}
}

func TestDoctor(t *testing.T) {
	e := newEnv(t)

	// Nothing set up.
	out, err := e.run("", "doctor")
	if err == nil {
		t.Errorf("doctor passed without a config:\n%s", out)
	}
	if !strings.Contains(out, "FAIL  config: "+e.cfg+" does not exist") {
		t.Errorf("unexpected output:\n%s", out)
	}

	// Config without repository or password.
	dir := e.mkdir("Projects/app")
	if out, err := e.run("", "add", dir); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err = e.run("", "doctor")
	if err == nil || !strings.Contains(err.Error(), "2 problem(s)") {
		t.Errorf("err = %v\n%s", err, out)
	}
	for _, want := range []string{
		"FAIL  repository: repository is not set",
		"FAIL  password: neither",
		"skip  repository access",
		"ok    folder Projects/app: " + dir,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	// Everything in place.
	e.repo()
	out, err = e.run("", "doctor")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if strings.Contains(out, "FAIL") || strings.Contains(out, "skip") || !strings.Contains(out, "ok    repository access") {
		t.Errorf("unexpected output:\n%s", out)
	}

	// A password file others can read, and a folder that is gone.
	pass := filepath.Join(filepath.Dir(e.cfg), "pass")
	if err := os.Chmod(pass, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	out, err = e.run("", "doctor")
	if err == nil || !strings.Contains(out, "readable by other users (mode 644)") || !strings.Contains(out, "FAIL  folder Projects/app") {
		t.Errorf("err = %v\n%s", err, out)
	}
}

func TestLog(t *testing.T) {
	e := newEnv(t)
	e.repo()
	app := filepath.Join(e.home, "Projects/app")
	other := filepath.Join(e.home, "Projects/other")
	e.write("Projects/app/a.txt", "one\n")
	e.write("Projects/other/b.txt", "other\n")
	for _, dir := range []string{app, other} {
		if out, err := e.run("", "add", dir); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	}

	out, err := e.run("", "log", app)
	if err != nil || out != "Projects/app: no snapshots yet\n" {
		t.Errorf("err = %v\n%s", err, out)
	}

	// Two snapshots of app from this client, one from another client, and
	// one of a different folder.
	snap := func(dir string) {
		t.Helper()
		if out, err := e.run("", "snapshot", dir); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	}
	snap(app)
	e.write("Projects/app/a.txt", "two!\n")
	snap(app)
	snap(other)
	c := e.config()
	c.Client = "client-b"
	if err := c.Save(e.cfg); err != nil {
		t.Fatal(err)
	}
	e.write("Projects/app/a.txt", "three\n")
	snap(app)

	out, err = e.run("", "log", app)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 5 || lines[0] != "Projects/app" || lines[4] != "master is not set" {
		t.Fatalf("unexpected output:\n%s", out)
	}
	lines = lines[:4]
	// Newest first: client-b's, which is also the base, then client-a's two.
	if f := strings.Fields(lines[1]); len(f) != 5 || f[3] != "client-b" || f[4] != "base" {
		t.Errorf("line 1 = %q", lines[1])
	}
	for _, line := range lines[2:] {
		if f := strings.Fields(line); len(f) != 4 || f[3] != "client-a" {
			t.Errorf("line = %q", line)
		}
	}

	out, err = e.run("", "log", "-n", "1", "Projects/app")
	if err != nil || !strings.Contains(out, "client-b") || strings.Contains(out, "client-a") || !strings.Contains(out, "2 older not shown") {
		t.Errorf("err = %v\n%s", err, out)
	}
}
