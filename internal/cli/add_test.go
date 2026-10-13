package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ahaltindis/wipsync/internal/config"
)

// env is a fake home directory with a config file beside it.
type env struct {
	t    *testing.T
	home string
	cfg  string
}

func newEnv(t *testing.T) *env {
	base := t.TempDir()
	e := &env{t: t, home: filepath.Join(base, "home"), cfg: filepath.Join(base, "config.toml")}
	t.Setenv("HOME", e.home)
	return e
}

func (e *env) mkdir(rel string) string {
	dir := filepath.Join(e.home, rel)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		e.t.Fatal(err)
	}
	return dir
}

func (e *env) run(stdin string, args ...string) (string, error) {
	return e.runIn(strings.NewReader(stdin), args...)
}

func (e *env) runIn(stdin io.Reader, args ...string) (string, error) {
	cmd := Root()
	var out bytes.Buffer
	cmd.SetIn(stdin)
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"--config", e.cfg}, args...))
	err := cmd.Execute()
	return out.String(), err
}

// cfgFile makes sure the config file exists and returns its path.
func (e *env) cfgFile() string {
	if err := e.config().Save(e.cfg); err != nil {
		e.t.Fatal(err)
	}
	return e.cfg
}

func (e *env) config() *config.Config {
	c, err := config.Load(e.cfg)
	if err != nil {
		e.t.Fatal(err)
	}
	return c
}

func TestAddAsksForRoot(t *testing.T) {
	e := newEnv(t)
	dir := e.mkdir("Projects/Freelance/kadir/boxing-machine")

	out, err := e.run("\n", "add", dir)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"1) " + e.home + "/Projects\n", "3) " + e.home + "/Projects/Freelance/kadir\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	c := e.config()
	if c.Roots["Projects"] != filepath.Join(e.home, "Projects") {
		t.Errorf("roots = %v", c.Roots)
	}
	if len(c.Folders) != 1 || c.Folders[0].Root != "Projects" || c.Folders[0].Path != "Freelance/kadir/boxing-machine" {
		t.Errorf("folders = %+v", c.Folders)
	}
	if len(c.Folders[0].Ignore) == 0 {
		t.Error("new folder has no ignore rules")
	}
}

func TestAddChoosesLowerRoot(t *testing.T) {
	e := newEnv(t)
	dir := e.mkdir("Projects/Freelance/kadir/boxing-machine")

	if out, err := e.run("9\n2\n", "add", dir); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	f := e.config().Folders[0]
	if f.Root != "Freelance" || f.Path != "kadir/boxing-machine" {
		t.Errorf("folder = %+v", f)
	}
}

func TestAddWithoutTerminal(t *testing.T) {
	e := newEnv(t)
	// A regular file as stdin: nobody is there to answer.
	stdin, err := os.Open(e.cfgFile())
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()

	_, err = e.runIn(stdin, "add", e.mkdir("Projects/Freelance/app"))
	if err == nil || !strings.Contains(err.Error(), "pass --root") {
		t.Errorf("err = %v", err)
	}
	if n := len(e.config().Folders); n != 0 {
		t.Errorf("%d folders, want 0", n)
	}

	// With a single possible root there is nothing to ask.
	if out, err := e.runIn(stdin, "add", e.mkdir("Other/app")); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := e.config().Folders[0].String(); got != "Other/app" {
		t.Errorf("folder = %s", got)
	}
}

func TestAddUsesKnownRoot(t *testing.T) {
	e := newEnv(t)
	first := e.mkdir("Projects/Freelance/kadir/boxing-machine")
	second := e.mkdir("Projects/aha/wipsync")
	if out, err := e.run("\n", "add", first); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	out, err := e.run("", "add", second)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if strings.Contains(out, "Which parent") {
		t.Errorf("asked for a root that is already known:\n%s", out)
	}
	c := e.config()
	if len(c.Folders) != 2 || c.Folders[1].String() != "Projects/aha/wipsync" {
		t.Errorf("folders = %+v", c.Folders)
	}
}

func TestAddRootFlag(t *testing.T) {
	e := newEnv(t)
	dir := e.mkdir("Projects/Freelance/kadir/boxing-machine")

	if out, err := e.run("", "add", dir, "--root", filepath.Join(e.home, "Projects/Freelance")); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := e.config().Folders[0].String(); got != "Freelance/kadir/boxing-machine" {
		t.Errorf("folder = %s", got)
	}

	other := e.mkdir("Elsewhere/thing")
	if _, err := e.run("", "add", other, "--root", filepath.Join(e.home, "Projects")); err == nil {
		t.Error("accepted a root the folder is not inside")
	}
}

func TestAddTwice(t *testing.T) {
	e := newEnv(t)
	dir := e.mkdir("Projects/app")
	if out, err := e.run("", "add", dir); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	_, err := e.run("", "add", dir)
	if err == nil || !strings.Contains(err.Error(), "already added as Projects/app") {
		t.Errorf("err = %v", err)
	}
	if n := len(e.config().Folders); n != 1 {
		t.Errorf("%d folders, want 1", n)
	}
}

func TestAddOverlap(t *testing.T) {
	e := newEnv(t)
	if out, err := e.run("\n", "add", e.mkdir("Projects/group/app")); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, rel := range []string{"Projects/group/app/src", "Projects/group"} {
		_, err := e.run("\n", "add", e.mkdir(rel))
		if err == nil || !strings.Contains(err.Error(), "overlaps Projects/group/app") {
			t.Errorf("add %s: err = %v", rel, err)
		}
	}
	// A sibling whose name only starts the same way is fine.
	if out, err := e.run("\n", "add", e.mkdir("Projects/group/app2")); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if n := len(e.config().Folders); n != 2 {
		t.Errorf("%d folders, want 2", n)
	}
}

func TestAddRootNameConflict(t *testing.T) {
	e := newEnv(t)
	if out, err := e.run("", "add", e.mkdir("Projects/app")); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	clash := e.mkdir("mnt/Projects/tool")

	// Asked for a new name, and the first answer is taken as well.
	out, err := e.run("Projects\nWork\n", "add", clash, "--root", filepath.Join(e.home, "mnt/Projects"))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, `root name "Projects" is already used`) {
		t.Errorf("output does not explain the clash:\n%s", out)
	}
	c := e.config()
	if c.Roots["Work"] != filepath.Join(e.home, "mnt/Projects") || c.Folders[1].String() != "Work/tool" {
		t.Errorf("roots = %v, folders = %+v", c.Roots, c.Folders)
	}
	if c.Roots["Projects"] != filepath.Join(e.home, "Projects") {
		t.Errorf("the existing root was changed: %v", c.Roots)
	}
}

func TestAddRootNameFlag(t *testing.T) {
	e := newEnv(t)
	dir := e.mkdir("code/app")
	if out, err := e.run("", "add", dir, "--root-name", "Projects"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := e.config().Folders[0].String(); got != "Projects/app" {
		t.Errorf("folder = %s", got)
	}
	if _, err := e.run("", "add", e.mkdir("other/app"), "--root-name", "a,b"); err == nil {
		// stdin is empty, so the prompt for another name must fail.
		t.Error("accepted a root name with a comma")
	}
}

func TestAddNotADirectory(t *testing.T) {
	e := newEnv(t)
	file := filepath.Join(e.mkdir("Projects"), "notes.txt")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := e.run("", "add", file); err == nil {
		t.Error("accepted a file")
	}
	if _, err := e.run("", "add", filepath.Join(e.home, "missing")); err == nil {
		t.Error("accepted a missing path")
	}
}
