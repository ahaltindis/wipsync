package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ahaltindis/wipsync/internal/config"
	"github.com/ahaltindis/wipsync/internal/master"
	"github.com/ahaltindis/wipsync/internal/restic"
	"github.com/ahaltindis/wipsync/internal/state"
)

func pullCmd() *cobra.Command {
	var from, rootFlag string
	cmd := &cobra.Command{
		Use:   "pull <Root/path>",
		Short: "Fetch a folder that is not on this client yet",
		Long: `Fetch a folder that is not on this client yet.

The folder is named as "wipsync list" shows it, for example
Projects/Freelance/kadir/boxing-machine. Pull takes its master, or with
--from the latest snapshot of one client.

If this client does not know where the folder's root is, here "Projects",
pull asks for its location once and remembers it.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath, cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			if f, err := resolveFolder(cfg, args[0]); err == nil {
				return fmt.Errorf("%s is already on this client, and pulling into an existing folder is not built yet", f)
			}
			root, path, ok := splitFolderName(args[0])
			if !ok {
				return fmt.Errorf("%q is not a folder name; give it as Root/path, see wipsync list", args[0])
			}
			f := config.Folder{Root: root, Path: path}

			client, err := resticClient(cfgPath, cfg)
			if err != nil {
				return err
			}
			pointers, err := master.Open(cfg.Repository)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			if err := restic.CheckVersion(ctx); err != nil {
				return err
			}
			list, err := client.Snapshots(ctx, folderTags(f))
			if err != nil {
				return err
			}
			if len(list) == 0 {
				return fmt.Errorf("there are no snapshots of %s in the repository; see wipsync list", f)
			}
			target, isMaster, err := pullTarget(ctx, pointers, list, f, from)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			p := newPrompter(ctx, cmd.InOrStdin(), out)
			rootDir, err := rootLocation(p, cfg, root, rootFlag)
			if err != nil {
				return err
			}
			dir := filepath.Join(rootDir, filepath.FromSlash(path))
			if other, ok := cfg.Overlap(dir); ok {
				return fmt.Errorf("%s overlaps %s, which is already on this client", dir, other)
			}
			if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
				return fmt.Errorf("%s exists and is not empty; pull would replace what is in it. Move it away first", dir)
			} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			// Created here, because a directory restic creates is closed to
			// everyone but its owner.
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}

			if _, err := client.Restore(ctx, restic.RestoreOptions{ID: target.ID, Dir: dir, Excludes: target.Excludes}); err != nil {
				return fmt.Errorf("%w. %s may be partly filled; remove it before trying again", err, dir)
			}

			// The folder keeps the ignore rules its snapshot was taken with,
			// so that every client applies the same ones.
			f.Ignore = target.Excludes
			if f.Ignore == nil {
				f.Ignore = []string{}
			}
			cfg.Roots[root] = rootDir
			cfg.Folders = append(cfg.Folders, f)
			if err := cfg.Save(cfgPath); err != nil {
				return err
			}
			store, err := state.Default()
			if err != nil {
				return err
			}
			if err := store.SetBase(root, path, target.ID); err != nil {
				return err
			}
			if isMaster {
				if err := store.SetMaster(root, path, target.ID); err != nil {
					return err
				}
			}
			if err := store.SetAfterPull(root, path, true); err != nil {
				return err
			}
			fmt.Fprintf(out, "pulled %s into %s: %s\n", f, dir, describe(target))
			return nil
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "take this client's latest snapshot instead of master")
	cmd.Flags().StringVar(&rootFlag, "root", "", "where the folder's root is on this machine")
	return cmd
}

// pullTarget picks the snapshot to pull from a folder's snapshots, oldest
// first: master, or the latest one of the named client.
func pullTarget(ctx context.Context, pointers master.Pointers, list []restic.Snapshot, f config.Folder, from string) (restic.Snapshot, bool, error) {
	var clients []string
	for _, s := range list {
		if !slices.Contains(clients, s.Host) {
			clients = append(clients, s.Host)
		}
	}
	slices.Sort(clients)
	have := strings.Join(clients, ", ")

	if from != "" {
		for _, s := range slices.Backward(list) {
			if s.Host == from {
				return s, false, nil
			}
		}
		return restic.Snapshot{}, false, fmt.Errorf("%s has no snapshots from client %q; it has them from: %s", f, from, have)
	}
	id, err := pointers.Master(ctx, f.Root, f.Path)
	if err != nil {
		return restic.Snapshot{}, false, err
	}
	if id == "" {
		return restic.Snapshot{}, false, fmt.Errorf("%s has no master yet; take a client's latest snapshot with --from (clients: %s)", f, have)
	}
	for _, s := range list {
		if s.ID == id {
			return s, true, nil
		}
	}
	return restic.Snapshot{}, false, fmt.Errorf("master of %s is %s, which is not among the folder's snapshots", f, id[:8])
}

// rootLocation returns where a root is on this client, asking if it is not
// known yet.
func rootLocation(p *prompter, cfg *config.Config, root, flag string) (string, error) {
	abs := func(dir string) (string, error) {
		if rest, ok := strings.CutPrefix(dir, "~/"); ok {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			dir = filepath.Join(home, rest)
		}
		return filepath.Abs(dir)
	}
	if known, ok := cfg.Roots[root]; ok {
		if flag != "" {
			if dir, err := abs(flag); err != nil || dir != known {
				return "", fmt.Errorf("root %s is already at %s on this client", root, known)
			}
		}
		return known, nil
	}
	if problem := checkRootName(cfg, root); problem != "" {
		return "", errors.New(problem)
	}

	answer := flag
	if answer == "" {
		if !p.interactive() {
			return "", fmt.Errorf("cannot tell where %s is on this machine; pass --root", root)
		}
		suggestion := root
		if home, err := os.UserHomeDir(); err == nil {
			suggestion = filepath.Join(home, root)
		}
		var err error
		if answer, err = p.ask(fmt.Sprintf("Where is %s on this machine? [%s]: ", root, suggestion)); err != nil {
			return "", err
		}
		if answer == "" {
			answer = suggestion
		}
	}
	dir, err := abs(answer)
	if err != nil {
		return "", err
	}
	if name, ok := cfg.RootAt(dir); ok {
		return "", fmt.Errorf("%s is already the root %s on this client", dir, name)
	}
	return dir, nil
}
