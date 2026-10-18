// Package state holds the folders' locks. They live outside the folders,
// where a pull would overwrite them, and not in the database: the kernel
// lets a lock go however the process ends.
package state

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// ErrBusy says another process holds a folder's lock.
var ErrBusy = errors.New("another wipsync is working on it")

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

// The files each folder's state was kept in before the database, by name:
// base, master, fingerprint and pulled.
var oldFiles = []string{"base", "master", "fingerprint", "pulled"}

// Old reads the files the folder's state was kept in before the database.
// A missing file reads as "".
func (s *Store) Old(root, path string) (map[string]string, error) {
	values := map[string]string{}
	for _, name := range oldFiles {
		data, err := os.ReadFile(s.file(root, path, name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, err
		}
		values[name] = strings.TrimSpace(string(data))
	}
	return values, nil
}

// RemoveOld removes all the files under the store but the locks, and the
// directories left empty.
func (s *Store) RemoveOld() error {
	top := filepath.Join(s.Dir, "folders")
	var dirs []string
	err := filepath.WalkDir(top, func(p string, d fs.DirEntry, err error) error {
		switch {
		case errors.Is(err, fs.ErrNotExist) && p == top:
			return fs.SkipAll
		case err != nil:
			return err
		case d.IsDir():
			dirs = append(dirs, p)
		case d.Name() != "lock":
			return os.Remove(p)
		}
		return nil
	})
	if err != nil {
		return err
	}
	// Deepest first; one that still holds a lock stays.
	for i := len(dirs) - 1; i >= 0; i-- {
		os.Remove(dirs[i])
	}
	return nil
}

// Lock takes the folder's lock without waiting, or returns ErrBusy if
// another process has it. It is held until unlock is called, or until the
// process ends, however it ends: the kernel lets it go with the file.
// Locking twice in one process also returns ErrBusy.
func (s *Store) Lock(root, path string) (unlock func(), err error) {
	file := s.file(root, path, "lock")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return nil, err
	}
	fd, err := os.OpenFile(file, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(fd.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		fd.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrBusy
		}
		return nil, err
	}
	return func() { fd.Close() }, nil
}
