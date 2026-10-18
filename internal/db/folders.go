package db

import (
	"path/filepath"
	"strings"
)

// Data is the client's settings and folders, as loaded.
type Data struct {
	Settings

	// Roots maps a root name to its location on this client. It is the only
	// part that differs between clients.
	Roots   map[string]string
	Folders []Folder
}

// Folder is identified across clients by its root name and its path under
// that root. Path uses forward slashes.
type Folder struct {
	Root   string
	Path   string
	Ignore []string
}

func (f Folder) String() string { return f.Root + "/" + f.Path }

// Dir returns where the folder lives on this client.
func (d *Data) Dir(f Folder) string {
	return filepath.Join(d.Roots[f.Root], filepath.FromSlash(f.Path))
}

// FolderAt returns the folder that lives at dir.
func (d *Data) FolderAt(dir string) (Folder, bool) {
	for _, f := range d.Folders {
		if d.Dir(f) == dir {
			return f, true
		}
	}
	return Folder{}, false
}

// FolderContaining returns the folder that dir is, or is inside.
func (d *Data) FolderContaining(dir string) (Folder, bool) {
	for _, f := range d.Folders {
		loc := d.Dir(f)
		if _, ok := Within(loc, dir); ok || loc == dir {
			return f, true
		}
	}
	return Folder{}, false
}

// Overlap returns an added folder that dir is inside, or that is inside dir.
func (d *Data) Overlap(dir string) (Folder, bool) {
	for _, f := range d.Folders {
		loc := d.Dir(f)
		if _, ok := Within(loc, dir); ok {
			return f, true
		}
		if _, ok := Within(dir, loc); ok {
			return f, true
		}
	}
	return Folder{}, false
}

// RootAt returns the name of the root located at dir.
func (d *Data) RootAt(dir string) (string, bool) {
	for name, loc := range d.Roots {
		if loc == dir {
			return name, true
		}
	}
	return "", false
}

// RootAbove returns the name of the known root that dir is inside. When
// several match, the deepest one wins.
func (d *Data) RootAbove(dir string) (string, bool) {
	best := ""
	for name, loc := range d.Roots {
		if _, ok := Within(loc, dir); ok && len(loc) > len(d.Roots[best]) {
			best = name
		}
	}
	return best, best != ""
}

// Within reports whether dir is inside parent, and returns its path relative
// to parent with forward slashes.
func Within(parent, dir string) (string, bool) {
	rel, err := filepath.Rel(parent, dir)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}
