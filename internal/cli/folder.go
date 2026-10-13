package cli

import (
	"errors"
	"fmt"
	"path/filepath"

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
