// Package app holds what wipsync does, apart from how it is asked: the
// command line and the terminal interface both call it. Its operations
// return what happened as data and leave the wording of success to the
// caller; its errors are worded for the user.
package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/ahaltindis/wipsync/internal/config"
	"github.com/ahaltindis/wipsync/internal/master"
	"github.com/ahaltindis/wipsync/internal/restic"
	"github.com/ahaltindis/wipsync/internal/state"
)

// App is one client: its config and its state.
type App struct {
	ConfigPath string
	Config     *config.Config
	State      *state.Store

	// NoTerminal is set by a caller that owns the screen and the keyboard.
	// restic and ssh are then kept away from the terminal: one that would
	// have to ask for something fails.
	NoTerminal bool
}

// Open loads the config at configPath, or the default one if it is empty.
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
	store, err := state.Default()
	if err != nil {
		return nil, err
	}
	return &App{ConfigPath: cfg.Path, Config: cfg, State: store}, nil
}

func CheckRepository(cfg *config.Config) error {
	if cfg.Repository == "" {
		return errors.New("repository is not set")
	}
	return nil
}

func CheckPassword(cfg *config.Config) error {
	switch {
	case cfg.PasswordFile == "" && cfg.PasswordCommand == "":
		return errors.New("neither password_file nor password_command is set")
	case cfg.PasswordFile != "" && cfg.PasswordCommand != "":
		return errors.New("password_file and password_command are both set, only one may be")
	}
	return nil
}

// Client returns the restic client for the configured repository.
func (a *App) Client() (*restic.Client, error) {
	for _, check := range []func(*config.Config) error{CheckRepository, CheckPassword} {
		if err := check(a.Config); err != nil {
			return nil, fmt.Errorf("%w in %s", err, a.ConfigPath)
		}
	}
	return &restic.Client{
		Repository:      a.Config.Repository,
		PasswordFile:    a.Config.PasswordFilePath(),
		PasswordCommand: a.Config.PasswordCommand,
		NoTerminal:      a.NoTerminal,
	}, nil
}

// Pointers returns the master pointers of the configured repository.
func (a *App) Pointers() (master.Pointers, error) {
	if a.NoTerminal {
		return master.OpenNoTerminal(a.Config.Repository)
	}
	return master.Open(a.Config.Repository)
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
func (a *App) Folder(arg string) (config.Folder, error) {
	for _, f := range a.Config.Folders {
		if arg == f.String() {
			return f, nil
		}
	}
	dir, err := filepath.Abs(arg)
	if err != nil {
		return config.Folder{}, err
	}
	if f, ok := a.Config.FolderContaining(dir); ok {
		return f, nil
	}
	return config.Folder{}, fmt.Errorf("%s is not in a synced folder; see wipsync add", dir)
}

// Tags are the tags that tie a snapshot to its folder on every client.
func Tags(f config.Folder) []string {
	return []string{"root=" + f.Root, "path=" + f.Path}
}

// FolderOf reads the folder a snapshot belongs to from its tags.
func FolderOf(tags []string) (config.Folder, bool) {
	var f config.Folder
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
