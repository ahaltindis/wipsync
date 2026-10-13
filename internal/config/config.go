// Package config reads and writes the client's config file.
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

// DefaultIgnore is the ignore list a newly added folder starts with.
var DefaultIgnore = []string{"node_modules", ".venv", "venv", "__pycache__"}

type Config struct {
	Client          string `toml:"client"`
	Repository      string `toml:"repository"`
	PasswordFile    string `toml:"password_file,omitempty"`
	PasswordCommand string `toml:"password_command,omitempty"`

	// Roots maps a root name to its location on this client. It is the only
	// part of the config that differs between clients.
	Roots   map[string]string `toml:"roots"`
	Folders []Folder          `toml:"folder"`
}

// Folder is identified across clients by its root name and its path under
// that root. Path uses forward slashes.
type Folder struct {
	Root   string   `toml:"root"`
	Path   string   `toml:"path"`
	Ignore []string `toml:"ignore"`
}

func (f Folder) String() string { return f.Root + "/" + f.Path }

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
	c := &Config{}
	if _, err := toml.DecodeFile(path, c); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("read config: %w", err)
	}
	if c.Roots == nil {
		c.Roots = map[string]string{}
	}
	if c.Client == "" {
		host, err := os.Hostname()
		if err != nil {
			return nil, fmt.Errorf("client name: %w", err)
		}
		c.Client = host
	}
	return c, nil
}

// Save writes the config to path, replacing it in one step.
func (c *Config) Save(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".config-*.toml")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := toml.NewEncoder(tmp).Encode(c); err != nil {
		tmp.Close()
		return fmt.Errorf("write config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Dir returns where the folder lives on this client.
func (c *Config) Dir(f Folder) string {
	return filepath.Join(c.Roots[f.Root], filepath.FromSlash(f.Path))
}

// FolderAt returns the folder that lives at dir.
func (c *Config) FolderAt(dir string) (Folder, bool) {
	for _, f := range c.Folders {
		if c.Dir(f) == dir {
			return f, true
		}
	}
	return Folder{}, false
}

// Overlap returns an added folder that dir is inside, or that is inside dir.
func (c *Config) Overlap(dir string) (Folder, bool) {
	for _, f := range c.Folders {
		loc := c.Dir(f)
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
func (c *Config) RootAt(dir string) (string, bool) {
	for name, loc := range c.Roots {
		if loc == dir {
			return name, true
		}
	}
	return "", false
}

// RootAbove returns the name of the known root that dir is inside. When
// several match, the deepest one wins.
func (c *Config) RootAbove(dir string) (string, bool) {
	best := ""
	for name, loc := range c.Roots {
		if _, ok := Within(loc, dir); ok && len(loc) > len(c.Roots[best]) {
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
