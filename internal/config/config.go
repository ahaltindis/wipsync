// Package config reads the client's config file: what the user sets. wipsync
// never writes it; what it writes is in the database.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

type Config struct {
	// Path is the absolute path of the file the config was loaded from.
	Path string `toml:"-"`

	// Open is what the TUI opens a directory with.
	Open Open `toml:"open,omitempty"`

	// Tray is the icon the service shows.
	Tray Tray `toml:"tray,omitempty"`

	// Old is what the config held before the database did.
	Old Old `toml:"-"`
}

// Open holds the commands the TUI opens a directory with. Each is run by sh,
// in the directory and with it as the last argument.
type Open struct {
	Window string `toml:"window,omitempty"` // O: on its own, beside the TUI
	Here   string `toml:"here,omitempty"`   // o: in the TUI's terminal, until it exits
}

// Tray is the icon `wipsync watch` shows in the desktop's tray. Changes take
// effect when the service restarts.
type Tray struct {
	Hide bool   `toml:"hide,omitempty"` // show no icon
	Open string `toml:"open,omitempty"` // run by sh on a left click; it opens the TUI when empty
}

// The commands used when the config names none.
const (
	DefaultOpenWindow = "xdg-open"
	DefaultOpenHere   = "yazi"
)

// Old is the setup and the folders as config files before the database had
// them. They are read once, to move them into the database.
type Old struct {
	Client          string            `toml:"client"`
	Repository      string            `toml:"repository"`
	PasswordFile    string            `toml:"password_file"`
	PasswordCommand string            `toml:"password_command"`
	Roots           map[string]string `toml:"roots"`
	Folders         []OldFolder       `toml:"folder"`

	// Keys are the ones the file has, as written there.
	Keys []string `toml:"-"`
}

type OldFolder struct {
	Root   string   `toml:"root"`
	Path   string   `toml:"path"`
	Ignore []string `toml:"ignore"`
}

var oldKeys = []string{"client", "repository", "password_file", "password_command", "roots", "folder"}

// DefaultPath returns ~/.config/wipsync/config.toml.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "wipsync", "config.toml"), nil
}

// Load reads the config at path. A missing file gives an empty config.
func Load(path string) (*Config, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	c := &Config{Path: path}
	if _, err := toml.DecodeFile(path, c); errors.Is(err, fs.ErrNotExist) {
		return c, nil
	} else if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	md, err := toml.DecodeFile(path, &c.Old)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	for _, k := range oldKeys {
		if md.IsDefined(k) {
			c.Old.Keys = append(c.Old.Keys, k)
		}
	}
	return c, nil
}

// OldPasswordFile returns where the old password_file points. A leading ~/
// is the home directory, and a relative path starts at the config's
// directory.
func (c *Config) OldPasswordFile() string {
	p := c.Old.PasswordFile
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(filepath.Dir(c.Path), p)
}
