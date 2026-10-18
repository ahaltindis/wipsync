package app

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ahaltindis/wipsync/internal/db"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestMoveOld(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(base, "data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(base, "state"))
	cfg := filepath.Join(base, "config", "config.toml")
	write(t, cfg, `
client = "laptop"
repository = "sftp:user@host:/repo"
password_file = "password"

[tray]
hide = true

[roots]
Projects = "/home/u/Projects"

[[folder]]
root = "Projects"
path = "aha/app"
ignore = [".venv"]

[[folder]]
root = "Projects"
path = "web"
ignore = []
`)
	write(t, filepath.Join(base, "config", "password"), "secret\n")
	old := filepath.Join(base, "state", "wipsync", "folders", "Projects", "aha", "app")
	write(t, filepath.Join(old, "base"), "abc123\n")
	write(t, filepath.Join(old, "master"), "aaa111\n")
	write(t, filepath.Join(old, "fingerprint"), "print\n")
	write(t, filepath.Join(old, "pulled"), "yes\n")
	write(t, filepath.Join(old, "lock"), "")

	a, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	pass := filepath.Join(base, "data", "wipsync", "password")
	want := &db.Data{
		Settings: db.Settings{Client: "laptop", Repository: "sftp:user@host:/repo", PasswordFile: pass},
		Roots:    map[string]string{"Projects": "/home/u/Projects"},
		Folders: []db.Folder{
			{Root: "Projects", Path: "aha/app", Ignore: []string{".venv"}},
			{Root: "Projects", Path: "web", Ignore: []string{}},
		},
	}
	if !reflect.DeepEqual(a.Data, want) {
		t.Errorf("got  %+v\nwant %+v", a.Data, want)
	}
	st, err := a.DB.State("Projects", "aha/app")
	if want := (db.State{Base: "abc123", Master: "aaa111", Fingerprint: "print", AfterPull: true}); err != nil || st != want {
		t.Errorf("state = %+v, %v; want %+v", st, err, want)
	}
	if st, _ := a.DB.State("Projects", "web"); st != (db.State{}) {
		t.Errorf("a folder without files has state %+v", st)
	}
	if !a.Config.Tray.Hide {
		t.Error("the user's settings were not read")
	}

	// The password moved; the old files are gone, but the lock and the config.
	if data, err := os.ReadFile(pass); err != nil || string(data) != "secret\n" {
		t.Errorf("password file = %q, %v", data, err)
	}
	if info, err := os.Stat(pass); err == nil && info.Mode().Perm() != 0o600 {
		t.Errorf("password file mode %o", info.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(base, "config", "password")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the old password file stayed: %v", err)
	}
	if entries, _ := os.ReadDir(old); len(entries) != 1 || entries[0].Name() != "lock" {
		t.Errorf("left %v, want only the lock", entries)
	}
	if data, _ := os.ReadFile(cfg); !strings.Contains(string(data), `repository = "sftp:user@host:/repo"`) {
		t.Errorf("the config was changed:\n%s", data)
	}
	notes := strings.Join(a.Notes, "\n")
	for _, want := range []string{"moved 2 folders", "moved the password file", "client, repository, password_file, roots, folder are kept in"} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes lack %q:\n%s", want, notes)
		}
	}

	// Later runs leave the database alone, and only warn.
	if err := a.DB.SetBase("Projects", "aha/app", "def456"); err != nil {
		t.Fatal(err)
	}
	again, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if id, _ := again.DB.Base("Projects", "aha/app"); id != "def456" {
		t.Errorf("base = %q after opening again", id)
	}
	if len(again.Notes) != 1 || !strings.Contains(again.Notes[0], "ignored here") {
		t.Errorf("notes = %q", again.Notes)
	}
}

// A password file the user put somewhere else stays there.
func TestMoveOldKeepsPasswordElsewhere(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(base, "data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(base, "state"))
	pass := filepath.Join(base, "secrets", "wipsync")
	write(t, pass, "secret\n")
	cfg := filepath.Join(base, "config", "config.toml")
	write(t, cfg, `repository = "/srv/repo"
password_file = "`+pass+`"
`)
	a, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if a.Data.PasswordFile != pass {
		t.Errorf("password file = %q, want %q", a.Data.PasswordFile, pass)
	}
	if _, err := os.Stat(pass); err != nil {
		t.Errorf("the password file was moved: %v", err)
	}
}

func TestOpenWithoutOldConfig(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(base, "data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(base, "state"))
	cfg := filepath.Join(base, "config.toml")
	write(t, cfg, "[tray]\nhide = true\n")
	a, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Notes) != 0 {
		t.Errorf("notes = %q", a.Notes)
	}
	if empty, _ := a.DB.Empty(); !empty {
		t.Error("something was saved")
	}
}
