package cli

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/ahaltindis/wipsync/internal/config"
	"github.com/ahaltindis/wipsync/internal/restic"
)

// resolveFolder finds the folder a command is about. arg is a folder as
// "Root/path", or a directory in a folder; empty means the current directory.
func resolveFolder(cfg *config.Config, arg string) (config.Folder, error) {
	for _, f := range cfg.Folders {
		if arg == f.String() {
			return f, nil
		}
	}
	dir, err := filepath.Abs(arg)
	if err != nil {
		return config.Folder{}, err
	}
	if f, ok := cfg.FolderContaining(dir); ok {
		return f, nil
	}
	return config.Folder{}, fmt.Errorf("%s is not in a synced folder; see wipsync add", dir)
}

// folderTags are the tags that tie a snapshot to its folder on every client.
func folderTags(f config.Folder) []string {
	return []string{"root=" + f.Root, "path=" + f.Path}
}

// splitFolderName reads a folder given as "Root/path".
func splitFolderName(name string) (root, path string, ok bool) {
	root, path, found := strings.Cut(name, "/")
	path = strings.TrimSuffix(path, "/")
	return root, path, found && root != "" && path != ""
}

// folderOf reads the folder a snapshot belongs to from its tags.
func folderOf(tags []string) (config.Folder, bool) {
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

// describe says which snapshot is meant, in full: its ID, age and client.
func describe(s restic.Snapshot) string {
	return fmt.Sprintf("%s, the snapshot from %s (%s) by %s",
		s.ID[:8], s.Time.Local().Format("2006-01-02 15:04"), age(time.Since(s.Time)), s.Host)
}

func checkRepository(cfg *config.Config) error {
	if cfg.Repository == "" {
		return errors.New("repository is not set")
	}
	return nil
}

func checkPassword(cfg *config.Config) error {
	switch {
	case cfg.PasswordFile == "" && cfg.PasswordCommand == "":
		return errors.New("neither password_file nor password_command is set")
	case cfg.PasswordFile != "" && cfg.PasswordCommand != "":
		return errors.New("password_file and password_command are both set, only one may be")
	}
	return nil
}

func resticClient(cfgPath string, cfg *config.Config) (*restic.Client, error) {
	for _, check := range []func(*config.Config) error{checkRepository, checkPassword} {
		if err := check(cfg); err != nil {
			return nil, fmt.Errorf("%w in %s", err, cfgPath)
		}
	}
	return &restic.Client{
		Repository:      cfg.Repository,
		PasswordFile:    cfg.PasswordFilePath(),
		PasswordCommand: cfg.PasswordCommand,
	}, nil
}

func formatBytes(n int64) string {
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
