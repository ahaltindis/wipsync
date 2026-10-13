package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ahaltindis/wipsync/internal/config"
	"github.com/ahaltindis/wipsync/internal/restic"
	"github.com/ahaltindis/wipsync/internal/state"
)

func doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check that wipsync has what it needs to run",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath, err := cmd.Flags().GetString("config")
			if err != nil {
				return err
			}
			if cfgPath == "" {
				if cfgPath, err = config.DefaultPath(); err != nil {
					return err
				}
			}
			r := &report{out: cmd.OutOrStdout()}
			r.run(cmd.Context(), cfgPath)
			if r.failed > 0 {
				return fmt.Errorf("doctor found %d problem(s)", r.failed)
			}
			return nil
		},
	}
}

type report struct {
	out    io.Writer
	failed int
}

func (r *report) ok(format string, args ...any) {
	fmt.Fprintf(r.out, "ok    "+format+"\n", args...)
}

func (r *report) fail(format string, args ...any) {
	r.failed++
	fmt.Fprintf(r.out, "FAIL  "+format+"\n", args...)
}

func (r *report) skip(format string, args ...any) {
	fmt.Fprintf(r.out, "skip  "+format+"\n", args...)
}

func (r *report) run(ctx context.Context, cfgPath string) {
	resticOK := r.restic(ctx)

	cfg, err := config.Load(cfgPath)
	if _, statErr := os.Stat(cfgPath); errors.Is(statErr, fs.ErrNotExist) {
		r.fail("config: %s does not exist", cfgPath)
		cfg = nil
	} else if err != nil {
		r.fail("config: %v", err)
	} else {
		r.ok("config: %s, client %q", cfgPath, cfg.Client)
	}

	if cfg == nil {
		r.skip("repository: needs the config")
	} else {
		r.repository(ctx, cfg, resticOK)
		r.folders(cfg)
	}
	r.state()
}

func (r *report) restic(ctx context.Context) bool {
	v, err := restic.InstalledVersion(ctx)
	switch {
	case err != nil:
		r.fail("restic: %v", err)
	case v.Less(restic.MinVersion):
		r.fail("restic: %s is too old, %s or newer is needed", v, restic.MinVersion)
	default:
		r.ok("restic: %s (%s or newer is needed)", v, restic.MinVersion)
		return true
	}
	return false
}

func (r *report) repository(ctx context.Context, cfg *config.Config, resticOK bool) {
	ready := resticOK

	if err := checkRepository(cfg); err != nil {
		r.fail("repository: %v", err)
		ready = false
	} else if strings.HasPrefix(cfg.Repository, "sftp:") {
		if _, err := exec.LookPath("ssh"); err != nil {
			r.fail("ssh: not installed, and the repository is reached over sftp")
			ready = false
		}
	}

	if err := checkPassword(cfg); err != nil {
		r.fail("password: %v", err)
		ready = false
	} else if cfg.PasswordFile != "" {
		file := cfg.PasswordFilePath()
		if info, err := os.Stat(file); err != nil {
			r.fail("password: %v", err)
			ready = false
		} else if info.Mode().Perm()&0o077 != 0 {
			r.fail("password: %s is readable by other users (mode %o), it should be 600", file, info.Mode().Perm())
		} else {
			r.ok("password: file %s", file)
		}
	} else {
		r.ok("password: from a command")
	}

	if !ready {
		r.skip("repository access: needs the checks above to pass")
		return
	}
	client, _ := resticClient(cfg.Path, cfg)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := client.Ping(ctx); err != nil {
		r.fail("repository access: %s: %v", cfg.Repository, err)
		return
	}
	r.ok("repository access: %s opens with the password", cfg.Repository)
}

func (r *report) folders(cfg *config.Config) {
	if len(cfg.Folders) == 0 {
		r.ok("folders: none added yet")
		return
	}
	for _, f := range cfg.Folders {
		dir := cfg.Dir(f)
		if _, mapped := cfg.Roots[f.Root]; !mapped {
			r.fail("folder %s: root %q has no location in [roots]", f, f.Root)
		} else if info, err := os.Stat(dir); err != nil {
			r.fail("folder %s: %v", f, err)
		} else if !info.IsDir() {
			r.fail("folder %s: %s is not a directory", f, dir)
		} else {
			r.ok("folder %s: %s", f, dir)
		}
	}
}

func (r *report) state() {
	store, err := state.Default()
	if err == nil {
		err = os.MkdirAll(store.Dir, 0o700)
	}
	if err == nil {
		var probe *os.File
		if probe, err = os.CreateTemp(store.Dir, ".doctor-*"); err == nil {
			probe.Close()
			os.Remove(probe.Name())
		}
	}
	if err != nil {
		r.fail("state directory: %v", err)
		return
	}
	r.ok("state directory: %s", store.Dir)
}
