package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func needRestic(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic is not installed")
	}
}

func TestInit(t *testing.T) {
	needRestic(t)
	e := newEnv(t)
	base := filepath.Dir(e.cfg)
	repo, pass := filepath.Join(base, "repo"), filepath.Join(base, "password")

	// File method; the first pair of passwords differs; then create the repository.
	out, err := e.run("1\nsecret\nsecrte\nsecret\nsecret\n"+repo+"\ny\n", "init")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"The two passwords differ.", "Created the repository at " + repo, "Saved " + e.cfg} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	c := e.config()
	if c.Repository != repo || c.PasswordFile != "password" || c.PasswordFilePath() != pass || c.PasswordCommand != "" {
		t.Errorf("config = %+v", c)
	}
	data, err := os.ReadFile(pass)
	if err != nil || string(data) != "secret\n" {
		t.Errorf("password file = %q, %v", data, err)
	}
	if info, _ := os.Stat(pass); info.Mode().Perm() != 0o600 {
		t.Errorf("password file mode = %o, want 600", info.Mode().Perm())
	}
	if out, err := e.run("", "doctor"); err != nil {
		t.Errorf("doctor after init: %v\n%s", err, out)
	}

	// Already set up: Enter leaves everything as it is.
	out, err = e.run("\n", "init")
	if err != nil || !strings.Contains(out, "Already set up for "+repo) || strings.Contains(out, "Saved") {
		t.Errorf("err = %v\n%s", err, out)
	}
}

func TestInitExistingRepository(t *testing.T) {
	needRestic(t)
	first := newEnv(t)
	repo := filepath.Join(filepath.Dir(first.cfg), "repo")
	if out, err := first.run("1\nsecret\nsecret\n"+repo+"\n\n", "init"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	// A second client whose password the repository does not know.
	second := newEnv(t)
	out, err := second.run("1\nother\nother\n"+repo+"\n", "init")
	if err == nil || !strings.Contains(err.Error(), "this password does not open it") {
		t.Errorf("err = %v\n%s", err, out)
	}
	if got := second.config().Repository; got != "" {
		t.Errorf("config was saved with repository %q", got)
	}

	// Again with the right one: the password file is replaced, nothing is created.
	out, err = second.run("1\ny\nsecret\nsecret\n"+repo+"\n", "init")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "Found the repository") || strings.Contains(out, "Create it?") {
		t.Errorf("unexpected output:\n%s", out)
	}
	if got := second.config().Repository; got != repo {
		t.Errorf("repository = %q", got)
	}
}

func TestInitPasswordCommand(t *testing.T) {
	needRestic(t)
	e := newEnv(t)
	base := filepath.Dir(e.cfg)
	repo, secret := filepath.Join(base, "repo"), filepath.Join(base, "elsewhere")
	if err := os.WriteFile(secret, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := e.run("2\n\ncat "+secret+"\n"+repo+"\n\n", "init")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	c := e.config()
	if c.PasswordCommand != "cat "+secret || c.PasswordFile != "" || c.Repository != repo {
		t.Errorf("config = %+v", c)
	}
	if _, err := os.Stat(filepath.Join(base, "password")); err == nil {
		t.Error("a password file was written for the command method")
	}
	if out, err := e.run("", "doctor"); err != nil {
		t.Errorf("doctor after init: %v\n%s", err, out)
	}
}

func TestInitDeclineCreate(t *testing.T) {
	needRestic(t)
	e := newEnv(t)
	repo := filepath.Join(filepath.Dir(e.cfg), "repo")
	_, err := e.run("1\nsecret\nsecret\n"+repo+"\nn\n", "init")
	if err == nil || !strings.Contains(err.Error(), "not set up") {
		t.Errorf("err = %v", err)
	}
	if _, err := os.Stat(repo); err == nil {
		t.Error("the repository was created anyway")
	}
	if got := e.config().Repository; got != "" {
		t.Errorf("config was saved with repository %q", got)
	}
}

func TestInitWithoutTerminal(t *testing.T) {
	e := newEnv(t)
	stdin, err := os.Open(e.cfgFile())
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	if _, err := e.runIn(stdin, "init"); err != errNoTerminal {
		t.Errorf("err = %v", err)
	}
}

func TestNormalizeRepository(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"ahmet@s2.ahmt.me:/opt/p2/wipsync":      "sftp:ahmet@s2.ahmt.me:/opt/p2/wipsync",
		"server:backups/wipsync":                "sftp:server:backups/wipsync",
		"sftp:ahmet@s2.ahmt.me:/opt/p2/wipsync": "sftp:ahmet@s2.ahmt.me:/opt/p2/wipsync",
		"rest:https://host:8000/repo":           "rest:https://host:8000/repo",
		"/srv/wipsync":                          "/srv/wipsync",
		"/srv/odd:name":                         "/srv/odd:name",
		"~/backups/wipsync":                     "/home/u/backups/wipsync",
		"./repo":                                filepath.Join(cwd, "repo"),
		"repo":                                  filepath.Join(cwd, "repo"),
		"dir/odd:name":                          filepath.Join(cwd, "dir/odd:name"),
	}
	for in, want := range cases {
		if got, err := normalizeRepository(in); err != nil || got != want {
			t.Errorf("normalizeRepository(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

// The config was given as a relative path, and later commands run from
// another directory.
func TestInitRelativeConfig(t *testing.T) {
	needRestic(t)
	e := newEnv(t)
	base := filepath.Dir(e.cfg)
	abs := filepath.Join(base, "conf", "try.toml")
	repo := filepath.Join(base, "repo")

	t.Chdir(base)
	e.cfg = filepath.Join("conf", "try.toml")
	if out, err := e.run("1\nsecret\nsecret\n"+repo+"\n\n", "init"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(base, "conf", "password")); err != nil {
		t.Errorf("password file is not beside the config: %v", err)
	}

	dir := e.mkdir("Projects/app")
	e.write("Projects/app/a.txt", "one\n")
	t.Chdir(e.home)
	e.cfg = abs
	if got := e.config().PasswordFile; got != "password" {
		t.Errorf("password_file = %q, want it relative to the config", got)
	}
	if out, err := e.run("", "doctor"); err != nil {
		t.Errorf("doctor from another directory: %v\n%s", err, out)
	}
	if out, err := e.run("", "add", dir); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if out, err := e.run("", "snapshot", dir); err != nil {
		t.Errorf("snapshot from another directory: %v\n%s", err, out)
	}
}
