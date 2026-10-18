package cli

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/ahaltindis/wipsync/internal/app"
	"github.com/ahaltindis/wipsync/internal/db"
)

func addCmd() *cobra.Command {
	var rootFlag, nameFlag string
	cmd := &cobra.Command{
		Use:   "add [path]",
		Short: "Start syncing a folder",
		Long: `Start syncing a folder (default: the current directory).

A folder is known to all clients by its root and its path under that root.
The root is the highest parent directory, for example ~/Projects, and each
client maps it to its own location.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "."
			if len(args) == 1 {
				path = args[0]
			}
			dir, err := filepath.Abs(path)
			if err != nil {
				return err
			}
			a, err := open(cmd)
			if err != nil {
				return err
			}
			if err := a.CheckAddable(dir); err != nil {
				return err
			}
			p := newPrompter(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout())
			name, rootDir, err := resolveRoot(p, a, dir, rootFlag, nameFlag)
			if err != nil {
				return err
			}
			f, err := a.Add(dir, name, rootDir)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "added %s (root %s = %s)\n", f, name, rootDir)
			return nil
		},
	}
	cmd.Flags().StringVar(&rootFlag, "root", "", "highest parent directory of the folder")
	cmd.Flags().StringVar(&nameFlag, "root-name", "", "name for the root (default: its directory name)")
	return cmd
}

// resolveRoot decides which parent of dir is its root, and what it is
// called, asking when that cannot be told.
func resolveRoot(p *prompter, a *app.App, dir, rootFlag, nameFlag string) (name, rootDir string, err error) {
	data := a.Data
	if rootFlag != "" {
		if rootDir, err = filepath.Abs(rootFlag); err != nil {
			return "", "", err
		}
		if _, ok := db.Within(rootDir, dir); !ok {
			return "", "", fmt.Errorf("%s is not inside %s", dir, rootDir)
		}
	} else if name, ok := data.RootAbove(dir); ok {
		return name, data.Roots[name], nil
	} else {
		candidates := app.RootCandidates(dir)
		if len(candidates) > 1 && !p.interactive() {
			return "", "", fmt.Errorf("cannot tell which parent of %s is its root; pass --root", dir)
		}
		if rootDir, err = p.chooseRoot(candidates); err != nil {
			return "", "", err
		}
	}

	if name, ok := data.RootAt(rootDir); ok {
		return name, rootDir, nil
	}
	name = nameFlag
	if name == "" {
		name = filepath.Base(rootDir)
	}
	for {
		problem := a.CheckRootName(name)
		if problem == "" {
			return name, rootDir, nil
		}
		if !p.interactive() {
			return "", "", fmt.Errorf("%s; pass --root-name", problem)
		}
		fmt.Fprintf(p.out, "%s.\n", problem)
		if name, err = p.ask("Name for " + rootDir + ": "); err != nil {
			return "", "", err
		}
	}
}

func (p *prompter) chooseRoot(candidates []string) (string, error) {
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	n, err := p.choose("Which parent is the root? Each client maps it to its own location.", "Root", candidates)
	if err != nil {
		return "", err
	}
	return candidates[n], nil
}
