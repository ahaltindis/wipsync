package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ahaltindis/wipsync/internal/config"
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
			if info, err := os.Stat(dir); err != nil {
				return err
			} else if !info.IsDir() {
				return fmt.Errorf("%s is not a directory", dir)
			}

			cfgPath, cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			if f, ok := cfg.FolderAt(dir); ok {
				return fmt.Errorf("%s is already added as %s", dir, f)
			}
			if f, ok := cfg.Overlap(dir); ok {
				return fmt.Errorf("%s overlaps %s, which is already added; a synced folder cannot be inside another", dir, f)
			}

			p := &prompter{in: cmd.InOrStdin(), out: cmd.OutOrStdout()}
			name, rootDir, err := resolveRoot(p, cfg, dir, rootFlag, nameFlag)
			if err != nil {
				return err
			}
			rel, _ := config.Within(rootDir, dir)

			f := config.Folder{Root: name, Path: rel, Ignore: config.DefaultIgnore}
			cfg.Roots[name] = rootDir
			cfg.Folders = append(cfg.Folders, f)
			if err := cfg.Save(cfgPath); err != nil {
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

// resolveRoot decides which parent of dir is its root, and what it is called.
func resolveRoot(p *prompter, cfg *config.Config, dir, rootFlag, nameFlag string) (name, rootDir string, err error) {
	if rootFlag != "" {
		if rootDir, err = filepath.Abs(rootFlag); err != nil {
			return "", "", err
		}
		if _, ok := config.Within(rootDir, dir); !ok {
			return "", "", fmt.Errorf("%s is not inside %s", dir, rootDir)
		}
	} else if name, ok := cfg.RootAbove(dir); ok {
		return name, cfg.Roots[name], nil
	} else {
		candidates := rootCandidates(dir)
		if len(candidates) > 1 && !p.interactive() {
			return "", "", fmt.Errorf("cannot tell which parent of %s is its root; pass --root", dir)
		}
		if rootDir, err = p.chooseRoot(candidates); err != nil {
			return "", "", err
		}
	}

	if name, ok := cfg.RootAt(rootDir); ok {
		return name, rootDir, nil
	}
	name = nameFlag
	if name == "" {
		name = filepath.Base(rootDir)
	}
	for {
		problem := checkRootName(cfg, name)
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

// checkRootName returns why name cannot be used for a new root, or "".
func checkRootName(cfg *config.Config, name string) string {
	// The name becomes a restic tag, and restic separates tags with commas.
	if name == "" || strings.ContainsAny(name, "/,") {
		return fmt.Sprintf("%q is not a usable root name", name)
	}
	if loc, taken := cfg.Roots[name]; taken {
		return fmt.Sprintf("root name %q is already used for %s", name, loc)
	}
	return ""
}

// rootCandidates lists the parents of dir, highest first. Inside the home
// directory it stops below home.
func rootCandidates(dir string) []string {
	stop := string(filepath.Separator)
	if home, err := os.UserHomeDir(); err == nil {
		if _, ok := config.Within(home, dir); ok {
			stop = home
		}
	}
	var out []string
	for p := filepath.Dir(dir); p != stop && p != filepath.Dir(p); p = filepath.Dir(p) {
		out = append([]string{p}, out...)
	}
	if len(out) == 0 {
		out = []string{filepath.Dir(dir)}
	}
	return out
}

type prompter struct {
	in  io.Reader
	out io.Writer
	r   *bufio.Reader
}

// interactive reports whether there is someone to answer a question. Input
// that is not a file was set by the caller, so it counts.
func (p *prompter) interactive() bool {
	f, ok := p.in.(*os.File)
	if !ok {
		return true
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func (p *prompter) ask(question string) (string, error) {
	if p.r == nil {
		p.r = bufio.NewReader(p.in)
	}
	fmt.Fprint(p.out, question)
	line, err := p.r.ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("no answer: %w", err)
	}
	return strings.TrimSpace(line), nil
}

func (p *prompter) chooseRoot(candidates []string) (string, error) {
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	fmt.Fprintln(p.out, "Which parent is the root? Each client maps it to its own location.")
	for i, c := range candidates {
		fmt.Fprintf(p.out, "  %d) %s\n", i+1, c)
	}
	for {
		answer, err := p.ask("Root [1]: ")
		if err != nil {
			return "", err
		}
		if answer == "" {
			return candidates[0], nil
		}
		if n, err := strconv.Atoi(answer); err == nil && n >= 1 && n <= len(candidates) {
			return candidates[n-1], nil
		}
		fmt.Fprintf(p.out, "Enter a number from 1 to %d.\n", len(candidates))
	}
}
