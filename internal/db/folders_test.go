package db

import "testing"

func TestDirAndFolderAt(t *testing.T) {
	c := &Data{
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
	c := &Data{
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

func TestRootAbovePicksDeepest(t *testing.T) {
	c := &Data{Roots: map[string]string{
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
