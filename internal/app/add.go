package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ahaltindis/wipsync/internal/config"
)

// CheckAddable reports why the directory cannot become a synced folder.
func (a *App) CheckAddable(dir string) error {
	if info, err := os.Stat(dir); err != nil {
		return err
	} else if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	if f, ok := a.Config.FolderAt(dir); ok {
		return fmt.Errorf("%s is already added as %s", dir, f)
	}
	if f, ok := a.Config.Overlap(dir); ok {
		return fmt.Errorf("%s overlaps %s, which is already added; a synced folder cannot be inside another", dir, f)
	}
	return nil
}

// RootCandidates lists the parents of dir, highest first: the directories
// that could be its root. Inside the home directory it stops below home.
func RootCandidates(dir string) []string {
	stop := string(filepath.Separator)
	if home, err := os.UserHomeDir(); err == nil {
		if _, ok := config.Within(home, dir); ok {
			stop = home
		}
	}
	var out []string
	for p := filepath.Dir(dir); p != stop && p != filepath.Dir(p); p = filepath.Dir(p) {
		out = append([]string{p}, out...)
	}
	if len(out) == 0 {
		out = []string{filepath.Dir(dir)}
	}
	return out
}

// CheckRootName returns why name cannot be used for a new root, or "".
func (a *App) CheckRootName(name string) string {
	// The name becomes a restic tag, and restic separates tags with commas.
	if name == "" || strings.ContainsAny(name, "/,") {
		return fmt.Sprintf("%q is not a usable root name", name)
	}
	if loc, taken := a.Config.Roots[name]; taken {
		return fmt.Sprintf("root name %q is already used for %s", name, loc)
	}
	return ""
}

// Add starts syncing the directory, as a folder under the root called
// rootName at rootDir. The root may be new or known.
func (a *App) Add(dir, rootName, rootDir string) (config.Folder, error) {
	rel, ok := config.Within(rootDir, dir)
	if !ok {
		return config.Folder{}, fmt.Errorf("%s is not inside %s", dir, rootDir)
	}
	// The path becomes a restic tag, and restic separates tags with commas.
	if strings.Contains(rel, ",") {
		return config.Folder{}, fmt.Errorf("%s has a comma in its path, which wipsync cannot sync", dir)
	}
	f := config.Folder{Root: rootName, Path: rel, Ignore: config.DefaultIgnore}
	a.Config.Roots[rootName] = rootDir
	a.Config.Folders = append(a.Config.Folders, f)
	return f, a.Config.Save(a.ConfigPath)
}

// RootAbove returns the known root that dir is inside: its name and where
// it is on this client.
func (a *App) RootAbove(dir string) (name, rootDir string, ok bool) {
	name, ok = a.Config.RootAbove(dir)
	return name, a.Config.Roots[name], ok
}

// RootAt returns the name of the root located at dir.
func (a *App) RootAt(dir string) (string, bool) {
	return a.Config.RootAt(dir)
}

// Roots returns where each known root is on this client.
func (a *App) Roots() map[string]string {
	roots := make(map[string]string, len(a.Config.Roots))
	for name, dir := range a.Config.Roots {
		roots[name] = dir
	}
	return roots
}
