package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/ahaltindis/wipsync/internal/db"
)

// moveOld moves what an older wipsync kept in the config and in files per
// folder into the database, when the database has nothing yet. It leaves
// the config alone, and notes the keys there that are no longer read.
func (a *App) moveOld() error {
	old := a.Config.Old
	if len(old.Keys) == 0 {
		return nil
	}
	defer func() {
		a.Notes = append(a.Notes, fmt.Sprintf("%s: %s are kept in %s now, and ignored here; remove them from the config",
			a.ConfigPath, strings.Join(old.Keys, ", "), a.DB.Path))
	}()
	if empty, err := a.DB.Empty(); err != nil || !empty {
		return err
	}
	if old.Repository == "" && len(old.Folders) == 0 {
		return nil
	}

	settings := db.Settings{Client: old.Client, Repository: old.Repository, PasswordCommand: old.PasswordCommand}
	// A password file that init kept beside the config moves beside the
	// database; one the user put elsewhere stays there.
	oldPass := a.Config.OldPasswordFile()
	settings.PasswordFile = oldPass
	moved := oldPass != "" && filepath.Dir(oldPass) == filepath.Dir(a.ConfigPath)
	if moved {
		settings.PasswordFile = filepath.Join(filepath.Dir(a.DB.Path), "password")
		if err := copyPassword(oldPass, settings.PasswordFile); err != nil {
			return fmt.Errorf("move the password file into %s: %w", filepath.Dir(settings.PasswordFile), err)
		}
	}

	states := map[string]db.State{}
	for _, f := range old.Folders {
		values, err := a.Locks.Old(f.Root, f.Path)
		if err != nil {
			return err
		}
		states[f.Root+"/"+f.Path] = db.State{
			Base:        values["base"],
			Master:      values["master"],
			Fingerprint: values["fingerprint"],
			AfterPull:   values["pulled"] != "",
		}
	}
	err := a.DB.Update(func(w db.Writer) error {
		if err := w.SetSettings(settings); err != nil {
			return err
		}
		for _, f := range old.Folders {
			if err := w.AddFolder(db.Folder{Root: f.Root, Path: f.Path, Ignore: f.Ignore}, old.Roots[f.Root]); err != nil {
				return fmt.Errorf("%s/%s: %w", f.Root, f.Path, err)
			}
			if err := w.SetState(f.Root, f.Path, states[f.Root+"/"+f.Path]); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("move the config's folders into %s: %w", a.DB.Path, err)
	}

	// Saved: what it was moved from goes.
	if moved {
		if err := os.Remove(oldPass); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	if err := a.Locks.RemoveOld(); err != nil {
		return err
	}
	a.Notes = append(a.Notes, fmt.Sprintf("moved %d folders and the repository setup from %s into %s",
		len(old.Folders), a.ConfigPath, a.DB.Path))
	if moved {
		a.Notes = append(a.Notes, fmt.Sprintf("moved the password file %s to %s", oldPass, settings.PasswordFile))
	}
	return nil
}

// copyPassword copies the password file from to to, with mode 600, in one
// step. A file already at to, from a move that was cut off, is replaced.
func copyPassword(from, to string) error {
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	dir := filepath.Dir(to)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// CreateTemp makes the file with mode 600.
	tmp, err := os.CreateTemp(dir, ".password-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), to)
}
