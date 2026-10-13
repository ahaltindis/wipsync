// Package state keeps what this client remembers about its folders. It lives
// outside the folders, where a pull would overwrite it.
package state

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type Store struct {
	Dir string
}

// Default returns the store in $XDG_STATE_HOME/wipsync, which is normally
// ~/.local/state/wipsync.
func Default() (*Store, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		base = filepath.Join(home, ".local", "state")
	}
	return &Store{Dir: filepath.Join(base, "wipsync")}, nil
}

func (s *Store) baseFile(root, path string) string {
	return filepath.Join(s.Dir, "folders", root, filepath.FromSlash(path), "base")
}

// Base returns the snapshot the folder is based on, or "" if there is none.
func (s *Store) Base(root, path string) (string, error) {
	data, err := os.ReadFile(s.baseFile(root, path))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	return strings.TrimSpace(string(data)), err
}

// SetBase records the snapshot the folder is based on, in one step.
func (s *Store) SetBase(root, path, id string) error {
	file := s.baseFile(root, path)
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, []byte(id+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}
