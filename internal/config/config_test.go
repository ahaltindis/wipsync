package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadMissingFile(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "none.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Old, Old{}) || c.Tray != (Tray{}) || c.Open != (Open{}) {
		t.Errorf("expected an empty config, got %+v", c)
	}
}

func TestLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(`
[tray]
open = "footclient wipsync tui"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Tray.Open != "footclient wipsync tui" {
		t.Errorf("tray open = %q", c.Tray.Open)
	}
	if len(c.Old.Keys) != 0 {
		t.Errorf("old keys %v in a config without any", c.Old.Keys)
	}
}

func TestLoadOld(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(`
client = "laptop"
repository = "sftp:user@host:/repo"
password_file = "password"

[roots]
Projects = "/home/u/Projects"

[[folder]]
root = "Projects"
path = "Freelance/kadir/boxing-machine"
ignore = [".venv"]
`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := Old{
		Client:       "laptop",
		Repository:   "sftp:user@host:/repo",
		PasswordFile: "password",
		Roots:        map[string]string{"Projects": "/home/u/Projects"},
		Folders:      []OldFolder{{Root: "Projects", Path: "Freelance/kadir/boxing-machine", Ignore: []string{".venv"}}},
		Keys:         []string{"client", "repository", "password_file", "roots", "folder"},
	}
	if !reflect.DeepEqual(c.Old, want) {
		t.Errorf("got  %+v\nwant %+v", c.Old, want)
	}
}

func TestOldPasswordFile(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	cases := map[string]string{
		"~/.config/wipsync/password": "/home/u/.config/wipsync/password",
		"/etc/wipsync/password":      "/etc/wipsync/password",
		"password":                   "/conf/dir/password",
		"keys/password":              "/conf/dir/keys/password",
		"":                           "",
	}
	for in, want := range cases {
		c := &Config{Path: "/conf/dir/config.toml", Old: Old{PasswordFile: in}}
		if got := c.OldPasswordFile(); got != want {
			t.Errorf("OldPasswordFile(%q) = %q, want %q", in, got, want)
		}
	}
}
