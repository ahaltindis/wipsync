package config

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadMissingFile(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "none.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Client == "" {
		t.Error("client should default to the hostname")
	}
	if len(c.Folders) != 0 || len(c.Roots) != 0 {
		t.Errorf("expected an empty config, got %+v", c)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.toml")
	want := &Config{
		Path:            path,
		Client:          "laptop",
		Repository:      "sftp:user@host:/repo",
		PasswordCommand: "secret-tool lookup service wipsync",
		Roots:           map[string]string{"Projects": "/home/u/Projects"},
		Folders: []Folder{
			{Root: "Projects", Path: "Freelance/kadir/boxing-machine", Ignore: []string{".venv"}},
		},
	}
	if err := want.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

func TestDirAndFolderAt(t *testing.T) {
	c := &Config{
		Roots:   map[string]string{"Projects": "/home/u/Projects"},
		Folders: []Folder{{Root: "Projects", Path: "Freelance/kadir/boxing-machine"}},
	}
	dir := "/home/u/Projects/Freelance/kadir/boxing-machine"
	if got := c.Dir(c.Folders[0]); got != dir {
		t.Errorf("Dir = %q, want %q", got, dir)
	}
	if _, ok := c.FolderAt(dir); !ok {
		t.Error("FolderAt did not find the folder")
	}
	if _, ok := c.FolderAt("/home/u/Projects/Freelance"); ok {
		t.Error("FolderAt matched a parent directory")
	}
}

func TestFolderContaining(t *testing.T) {
	c := &Config{
		Roots:   map[string]string{"Projects": "/home/u/Projects"},
		Folders: []Folder{{Root: "Projects", Path: "app"}},
	}
	for _, dir := range []string{"/home/u/Projects/app", "/home/u/Projects/app/src/deep"} {
		if f, ok := c.FolderContaining(dir); !ok || f.Path != "app" {
			t.Errorf("FolderContaining(%q) = %+v, %v", dir, f, ok)
		}
	}
	for _, dir := range []string{"/home/u/Projects", "/home/u/Projects/app2"} {
		if _, ok := c.FolderContaining(dir); ok {
			t.Errorf("FolderContaining(%q) matched", dir)
		}
	}
}

func TestPasswordFilePath(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	cases := map[string]string{
		"~/.config/wipsync/password": "/home/u/.config/wipsync/password",
		"/etc/wipsync/password":      "/etc/wipsync/password",
		"password":                   "/conf/dir/password",
		"keys/password":              "/conf/dir/keys/password",
		"":                           "",
	}
	for in, want := range cases {
		if got := (&Config{Path: "/conf/dir/config.toml", PasswordFile: in}).PasswordFilePath(); got != want {
			t.Errorf("PasswordFilePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRootAbovePicksDeepest(t *testing.T) {
	c := &Config{Roots: map[string]string{
		"Projects":  "/home/u/Projects",
		"Freelance": "/home/u/Projects/Freelance",
		"Other":     "/home/u/Other",
	}}
	if name, ok := c.RootAbove("/home/u/Projects/Freelance/kadir"); !ok || name != "Freelance" {
		t.Errorf("got %q %v, want Freelance", name, ok)
	}
	if name, ok := c.RootAbove("/home/u/Projects/x"); !ok || name != "Projects" {
		t.Errorf("got %q %v, want Projects", name, ok)
	}
	if _, ok := c.RootAbove("/home/u/Projects"); ok {
		t.Error("a root is not above itself")
	}
	if _, ok := c.RootAbove("/home/u/ProjectsX/y"); ok {
		t.Error("matched a directory that only shares a name prefix")
	}
}

func TestWithin(t *testing.T) {
	cases := []struct {
		parent, dir, rel string
		ok               bool
	}{
		{"/a", "/a/b/c", "b/c", true},
		{"/a", "/a", "", false},
		{"/a/b", "/a", "", false},
		{"/a", "/ab", "", false},
		{"/a", "/a/..b", "..b", true},
	}
	for _, tc := range cases {
		rel, ok := Within(tc.parent, tc.dir)
		if rel != tc.rel || ok != tc.ok {
			t.Errorf("Within(%q, %q) = %q, %v; want %q, %v", tc.parent, tc.dir, rel, ok, tc.rel, tc.ok)
		}
	}
}
