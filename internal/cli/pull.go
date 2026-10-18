package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ahaltindis/wipsync/internal/app"
	"github.com/ahaltindis/wipsync/internal/db"
	"github.com/ahaltindis/wipsync/internal/watch"
)

func pullCmd() *cobra.Command {
	var from, rootFlag string
	var all bool
	cmd := &cobra.Command{
		Use:   "pull [folder]",
		Short: "Bring master, or a client's latest snapshot, into a folder",
		Long: `Bring master, or with --from a client's latest snapshot, into a folder.

For a folder that is on this client, the one the current directory is in
unless another is given, pull first saves its current state as a snapshot.
It then makes the folder match the target, but only if the target is ahead:
if the folder also changed since the two were the same, pull stops and
changes nothing.

For a folder that is not on this client yet, name it as "wipsync list"
shows it, for example Projects/Freelance/kadir/boxing-machine. If this
client does not know where the folder's root is, here "Projects", pull asks
for its location once and remembers it.

With --all, pull brings master into every folder on this client that is
behind it, and into every folder that is not here yet but whose root is.
Folders that changed on both sides, and those whose root is not set up
here, are left alone. When wipsync watch runs, it does the work, so that
its tray icon shows it and the desktop is told how it went.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if all && (len(args) > 0 || from != "" || rootFlag != "") {
				return errors.New("--all pulls master into every folder; it takes no folder, --from or --root")
			}
			a, err := open(cmd)
			if err != nil {
				return err
			}
			if all {
				return runAll(cmd, a, watch.PullAll, func() (app.AllResult, error) {
					return a.PullAll(cmd.Context(), app.Scope{})
				})
			}
			arg := ""
			if len(args) == 1 {
				arg = args[0]
			}
			// Checked before the folder is looked at, as every command
			// that reaches the repository does.
			if _, err := a.Client(); err != nil {
				return err
			}
			ctx, out := cmd.Context(), cmd.OutOrStdout()

			if f, err := a.Folder(arg); err == nil {
				if rootFlag != "" {
					return fmt.Errorf("%s is already on this client, at %s; --root is for a folder that is not", f, a.Data.Dir(f))
				}
				res, err := a.Pull(ctx, f, from)
				if err != nil {
					if l := res.SavedLine(f); l != "" {
						fmt.Fprintln(out, l)
					}
					return err
				}
				fmt.Fprintln(out, res.Line(f))
				return nil
			} else if arg == "" {
				return fmt.Errorf("%w, or name a folder as Root/path to fetch it", err)
			}

			root, path, ok := app.SplitName(arg)
			if !ok {
				return fmt.Errorf("%q is not a folder name; give it as Root/path, see wipsync list", arg)
			}
			f := db.Folder{Root: root, Path: path}
			// Found before asking anything, so a mistyped name costs no answers.
			target, err := a.FindTarget(ctx, f, from)
			if err != nil {
				return err
			}
			rootDir, err := rootLocation(newPrompter(ctx, cmd.InOrStdin(), out), a, root, rootFlag)
			if err != nil {
				return err
			}
			res, err := a.Fetch(ctx, f, rootDir, target)
			if err != nil {
				return err
			}
			fmt.Fprintln(out, res.Line(f))
			return nil
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "take this client's latest snapshot instead of master")
	cmd.Flags().BoolVar(&all, "all", false, "pull master into every folder that is behind it or not here yet")
	cmd.Flags().StringVar(&rootFlag, "root", "", "where the folder's root is on this machine, for a folder that is not here yet")
	return cmd
}

// rootLocation returns where a root is on this client, asking if it is not
// known yet.
func rootLocation(p *prompter, a *app.App, root, flag string) (string, error) {
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
	if known, ok := a.RootDir(root); ok {
		if flag != "" {
			if dir, err := abs(flag); err != nil || dir != known {
				return "", fmt.Errorf("root %s is already at %s on this client", root, known)
			}
		}
		return known, nil
	}
	// The name is checked before asking where the root is.
	if err := a.CheckNewRoot(root, ""); err != nil {
		return "", err
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
	return dir, a.CheckNewRoot(root, dir)
}
