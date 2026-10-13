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

func (s *Store) file(root, path, name string) string {
	return filepath.Join(s.Dir, "folders", root, filepath.FromSlash(path), name)
}

func (s *Store) get(root, path, name string) (string, error) {
	data, err := os.ReadFile(s.file(root, path, name))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	return strings.TrimSpace(string(data)), err
}

// set replaces the value in one step.
func (s *Store) set(root, path, name, id string) error {
	file := s.file(root, path, name)
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, []byte(id+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

// Base returns the snapshot the folder is based on, or "" if there is none.
func (s *Store) Base(root, path string) (string, error) {
	return s.get(root, path, "base")
}

// SetBase records the snapshot the folder is based on.
func (s *Store) SetBase(root, path, id string) error {
	return s.set(root, path, "base", id)
}

// Master returns the master snapshot the folder was last in step with: the
// one it last pushed or pulled. It is "" if it has done neither.
func (s *Store) Master(root, path string) (string, error) {
	return s.get(root, path, "master")
}

// SetMaster records the master snapshot the folder is in step with.
func (s *Store) SetMaster(root, path, id string) error {
	return s.set(root, path, "master", id)
}

// AfterPull reports whether the folder was pulled and not snapshotted since.
func (s *Store) AfterPull(root, path string) (bool, error) {
	v, err := s.get(root, path, "pulled")
	return v != "", err
}

// SetAfterPull records or clears that mark.
func (s *Store) SetAfterPull(root, path string, pulled bool) error {
	if pulled {
		return s.set(root, path, "pulled", "yes")
	}
	err := os.Remove(s.file(root, path, "pulled"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
