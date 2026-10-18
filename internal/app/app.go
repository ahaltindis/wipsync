// Package app holds what wipsync does, apart from how it is asked: the
// command line and the terminal interface both call it. Its operations
// return what happened as data and leave the wording of success to the
// caller; its errors are worded for the user.
package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ahaltindis/wipsync/internal/config"
	"github.com/ahaltindis/wipsync/internal/db"
	"github.com/ahaltindis/wipsync/internal/master"
	"github.com/ahaltindis/wipsync/internal/restic"
	"github.com/ahaltindis/wipsync/internal/state"
)

// App is one client: its config, and what it keeps in its database.
type App struct {
	ConfigPath string
	Config     *config.Config
	DB         *db.DB
	Data       *db.Data
	Locks      *state.Store

	// Notes are for the user, about how the client was opened: what was
	// moved into the database, and what in the config is no longer read.
	Notes []string

	// NoTerminal is set by a caller that owns the screen and the keyboard.
	// restic and ssh are then kept away from the terminal: one that would
	// have to ask for something fails.
	NoTerminal bool
}

// Open loads the config at configPath, or the default one if it is empty,
// and the database. The first time, it moves what an older config held
// into the database.
func Open(configPath string) (*App, error) {
	if configPath == "" {
		var err error
		if configPath, err = config.DefaultPath(); err != nil {
			return nil, err
		}
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}
	dbPath, err := db.DefaultPath()
	if err != nil {
		return nil, err
	}
	d, err := db.Open(dbPath)
	if err != nil {
		return nil, err
	}
	locks, err := state.Default()
	if err != nil {
		return nil, err
	}
	a := &App{ConfigPath: cfg.Path, Config: cfg, DB: d, Locks: locks}
	if err := a.moveOld(); err != nil {
		return nil, err
	}
	if a.Data, err = d.Load(); err != nil {
		return nil, err
	}
	return a, nil
}

func CheckRepository(s db.Settings) error {
	if s.Repository == "" {
		return errors.New("repository is not set; run wipsync init")
	}
	return nil
}

func CheckPassword(s db.Settings) error {
	switch {
	case s.PasswordFile == "" && s.PasswordCommand == "":
		return errors.New("no repository password is set; run wipsync init")
	case s.PasswordFile != "" && s.PasswordCommand != "":
		return errors.New("both a password file and a password command are set, only one may be; run wipsync init")
	}
	return nil
}

// lock takes the folder's lock for one operation, so that no two wipsync
// processes work on the folder at once. It does not wait.
func (a *App) lock(f db.Folder) (unlock func(), err error) {
	unlock, err = a.Locks.Lock(f.Root, f.Path)
	if err != nil {
		return nil, fmt.Errorf("%s is busy: %w", f, err)
	}
	return unlock, nil
}

// Client returns the restic client for the configured repository.
func (a *App) Client() (*restic.Client, error) {
	for _, check := range []func(db.Settings) error{CheckRepository, CheckPassword} {
		if err := check(a.Data.Settings); err != nil {
			return nil, err
		}
	}
	return &restic.Client{
		Repository:      a.Data.Repository,
		PasswordFile:    a.Data.PasswordFile,
		PasswordCommand: a.Data.PasswordCommand,
		NoTerminal:      a.NoTerminal,
	}, nil
}

// Pointers returns the master pointers of the configured repository.
func (a *App) Pointers() (master.Pointers, error) {
	if a.NoTerminal {
		return master.OpenNoTerminal(a.Data.Repository)
	}
	return master.Open(a.Data.Repository)
}

// Reach reports whether the repository's server answers, without restic and
// without the password: for one reached over ssh, a single ssh call.
func (a *App) Reach(ctx context.Context) error {
	pointers, err := a.Pointers()
	if err != nil {
		return err
	}
	return pointers.Check(ctx)
}

// ClientName is this client's name, as its snapshots are tagged.
func (a *App) ClientName() string { return a.Data.Client }

// Here returns the folders whose directory is on this client.
func (a *App) Here() []db.Folder {
	var here []db.Folder
	for _, f := range a.Data.Folders {
		if info, err := os.Stat(a.Data.Dir(f)); err == nil && info.IsDir() {
			here = append(here, f)
		}
	}
	return here
}

// remote returns what an operation on the repository needs: the restic
// client and the master pointers, after checking the installed restic.
func (a *App) remote(ctx context.Context) (*restic.Client, master.Pointers, error) {
	client, err := a.Client()
	if err != nil {
		return nil, nil, err
	}
	pointers, err := a.Pointers()
	if err != nil {
		return nil, nil, err
	}
	if err := restic.CheckVersion(ctx); err != nil {
		return nil, nil, err
	}
	return client, pointers, nil
}

// Folder finds the folder a request is about. arg is a folder as
// "Root/path", or a directory in a folder; empty means the current directory.
func (a *App) Folder(arg string) (db.Folder, error) {
	for _, f := range a.Data.Folders {
		if arg == f.String() {
			return f, nil
		}
	}
	dir, err := filepath.Abs(arg)
	if err != nil {
		return db.Folder{}, err
	}
	if f, ok := a.Data.FolderContaining(dir); ok {
		return f, nil
	}
	return db.Folder{}, fmt.Errorf("%s is not in a synced folder; see wipsync add", dir)
}

// Tags are the tags that tie a snapshot to its folder on every client.
func Tags(f db.Folder) []string {
	return []string{"root=" + f.Root, "path=" + f.Path}
}

// FolderOf reads the folder a snapshot belongs to from its tags.
func FolderOf(tags []string) (db.Folder, bool) {
	var f db.Folder
	for _, t := range tags {
		if v, ok := strings.CutPrefix(t, "root="); ok {
			f.Root = v
		} else if v, ok := strings.CutPrefix(t, "path="); ok {
			f.Path = v
		}
	}
	return f, f.Root != "" && f.Path != ""
}

// SplitName reads a folder given as "Root/path".
func SplitName(name string) (root, path string, ok bool) {
	root, path, found := strings.Cut(name, "/")
	path = strings.TrimSuffix(path, "/")
	return root, path, found && root != "" && path != ""
}

// Describe says which snapshot is meant, in full: its ID, age and client.
func Describe(s restic.Snapshot) string {
	return fmt.Sprintf("%s, the snapshot from %s (%s) by %s",
		s.ID[:8], s.Time.Local().Format("2006-01-02 15:04"), Age(time.Since(s.Time)), s.Host)
}

// Brief names a snapshot in a few words: its ID, client and age.
func Brief(s restic.Snapshot) string {
	return fmt.Sprintf("%s (%s, %s)", s.ID[:8], s.Host, Age(time.Since(s.Time)))
}

// Age describes how long ago something happened.
func Age(d time.Duration) string {
	plural := func(n int, unit string) string {
		if n == 1 {
			return fmt.Sprintf("1 %s ago", unit)
		}
		return fmt.Sprintf("%d %ss ago", n, unit)
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return plural(int(d/time.Minute), "minute")
	case d < 48*time.Hour:
		return plural(int(d/time.Hour), "hour")
	default:
		return plural(int(d/(24*time.Hour)), "day")
	}
}

// FormatBytes writes a size the way restic does.
func FormatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value, suffix := float64(n), ""
	for _, s := range []string{"KiB", "MiB", "GiB", "TiB"} {
		value, suffix = value/unit, s
		if value < unit {
			break
		}
	}
	return fmt.Sprintf("%.1f %s", value, suffix)
}
