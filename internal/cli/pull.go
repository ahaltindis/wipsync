package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ahaltindis/wipsync/internal/config"
	"github.com/ahaltindis/wipsync/internal/fingerprint"
	"github.com/ahaltindis/wipsync/internal/master"
	"github.com/ahaltindis/wipsync/internal/restic"
	"github.com/ahaltindis/wipsync/internal/state"
)

func pullCmd() *cobra.Command {
	var from, rootFlag string
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
for its location once and remembers it.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath, cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			arg := ""
			if len(args) == 1 {
				arg = args[0]
			}
			p, err := newPuller(cmd, cfgPath, cfg)
			if err != nil {
				return err
			}
			p.from = from

			if f, err := resolveFolder(cfg, arg); err == nil {
				if rootFlag != "" {
					return fmt.Errorf("%s is already on this client, at %s; --root is for a folder that is not", f, cfg.Dir(f))
				}
				return p.existing(f)
			} else if arg == "" {
				return fmt.Errorf("%w, or name a folder as Root/path to fetch it", err)
			}
			root, path, ok := splitFolderName(arg)
			if !ok {
				return fmt.Errorf("%q is not a folder name; give it as Root/path, see wipsync list", arg)
			}
			prompt := newPrompter(p.ctx, cmd.InOrStdin(), p.out)
			return p.fresh(config.Folder{Root: root, Path: path}, prompt, rootFlag)
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "take this client's latest snapshot instead of master")
	cmd.Flags().StringVar(&rootFlag, "root", "", "where the folder's root is on this machine, for a folder that is not here yet")
	return cmd
}

// puller holds what both kinds of pull need.
type puller struct {
	cfgPath  string
	cfg      *config.Config
	ctx      context.Context
	out      io.Writer
	from     string
	client   *restic.Client
	pointers master.Pointers
	store    *state.Store
}

func newPuller(cmd *cobra.Command, cfgPath string, cfg *config.Config) (*puller, error) {
	p := &puller{cfgPath: cfgPath, cfg: cfg, ctx: cmd.Context(), out: cmd.OutOrStdout()}
	var err error
	if p.client, err = resticClient(cfgPath, cfg); err != nil {
		return nil, err
	}
	if p.pointers, err = master.Open(cfg.Repository); err != nil {
		return nil, err
	}
	if err := restic.CheckVersion(p.ctx); err != nil {
		return nil, err
	}
	if p.store, err = state.Default(); err != nil {
		return nil, err
	}
	return p, nil
}

// target returns the snapshot to pull, whether it is master, and all of
// the folder's snapshots.
func (p *puller) target(f config.Folder) (restic.Snapshot, bool, []restic.Snapshot, error) {
	list, err := p.client.Snapshots(p.ctx, folderTags(f))
	if err != nil {
		return restic.Snapshot{}, false, nil, err
	}
	if len(list) == 0 {
		return restic.Snapshot{}, false, nil, fmt.Errorf("there are no snapshots of %s in the repository; see wipsync list", f)
	}
	target, isMaster, err := pullTarget(p.ctx, p.pointers, list, f, p.from)
	return target, isMaster, list, err
}

// record notes that the folder at dir now matches the pulled snapshot.
func (p *puller) record(f config.Folder, dir string, target restic.Snapshot, isMaster bool) error {
	if err := p.store.SetBase(f.Root, f.Path, target.ID); err != nil {
		return err
	}
	if isMaster {
		if err := p.store.SetMaster(f.Root, f.Path, target.ID); err != nil {
			return err
		}
	}
	if err := p.store.SetAfterPull(f.Root, f.Path, true); err != nil {
		return err
	}
	// The folder as pulled is what later looks are compared with.
	if print, err := fingerprint.Of(p.ctx, dir, f.Ignore); err == nil {
		return p.store.SetFingerprint(f.Root, f.Path, print)
	}
	return nil
}

// fresh fetches a folder that is not on this client yet.
func (p *puller) fresh(f config.Folder, prompt *prompter, rootFlag string) error {
	target, isMaster, _, err := p.target(f)
	if err != nil {
		return err
	}
	rootDir, err := rootLocation(prompt, p.cfg, f.Root, rootFlag)
	if err != nil {
		return err
	}
	dir := filepath.Join(rootDir, filepath.FromSlash(f.Path))
	if other, ok := p.cfg.Overlap(dir); ok {
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
	if _, err := p.client.Restore(p.ctx, restic.RestoreOptions{ID: target.ID, Dir: dir, Excludes: target.Excludes}); err != nil {
		return fmt.Errorf("%w. %s may be partly filled; remove it before trying again", err, dir)
	}

	// The folder keeps the ignore rules its snapshot was taken with, so
	// that every client applies the same ones.
	f.Ignore = target.Excludes
	if f.Ignore == nil {
		f.Ignore = []string{}
	}
	p.cfg.Roots[f.Root] = rootDir
	p.cfg.Folders = append(p.cfg.Folders, f)
	if err := p.cfg.Save(p.cfgPath); err != nil {
		return err
	}
	if err := p.record(f, dir, target, isMaster); err != nil {
		return err
	}
	fmt.Fprintf(p.out, "pulled %s into %s: %s\n", f, dir, describe(target))
	return nil
}

// existing brings the target into a folder that is on this client, if the
// target is ahead of it.
func (p *puller) existing(f config.Folder) error {
	// Saved first, so that whatever the pull does can be undone.
	saved, err := takeSnapshot(p.ctx, p.cfgPath, p.cfg, p.store, f)
	if err != nil {
		return err
	}
	if saved.SnapshotID != "" {
		fmt.Fprintf(p.out, "%s: saved its current state as snapshot %s\n", f, saved.SnapshotID[:8])
	}
	base, err := p.store.Base(f.Root, f.Path)
	if err != nil {
		return err
	}
	if base == "" {
		return fmt.Errorf("%s has no snapshot from this client to compare with; run wipsync snapshot first", f)
	}
	target, isMaster, list, err := p.target(f)
	if err != nil {
		return err
	}
	parents := map[string]string{}
	for _, s := range list {
		parents[s.ID] = s.Parent
	}
	inStep := func() error {
		if !isMaster {
			return nil
		}
		return p.store.SetMaster(f.Root, f.Path, target.ID)
	}

	switch {
	case target.ID == base:
		fmt.Fprintf(p.out, "%s: already at %s\n", f, describe(target))
		return inStep()

	case descends(parents, base, target.ID):
		// The folder already contains the target and has moved on.
		fmt.Fprintf(p.out, "%s: nothing to pull, this client is ahead of %s\n", f, describe(target))
		return inStep()

	case !descends(parents, target.ID, base):
		since := "they were last the same"
		if common := commonAncestor(parents, base, target.ID); common != "" {
			since = "snapshot " + common[:8]
		}
		return fmt.Errorf("%s: both this folder and %s changed since %s. "+
			"Pull cannot merge yet and changed nothing; the folder's state is saved as snapshot %s. "+
			"To take the other side as it is: wipsync restore %s",
			f, describe(target), since, base[:8], target.ID[:8])
	}
	if err := p.apply(&f, target, isMaster, base); err != nil {
		return err
	}
	fmt.Fprintf(p.out, "pulled %s: %s\n", f, describe(target))
	return nil
}

// apply makes the folder match the target snapshot and records that. base
// is the snapshot holding the folder's state from before, for the message
// if it fails halfway. f is updated when its ignore rules change.
func (p *puller) apply(f *config.Folder, target restic.Snapshot, isMaster bool, base string) error {
	// Paths ignored on either side are left alone, so that nothing a
	// client keeps out of its snapshots is deleted here.
	dir := p.cfg.Dir(*f)
	excludes := slices.Clone(f.Ignore)
	for _, e := range target.Excludes {
		if !slices.Contains(excludes, e) {
			excludes = append(excludes, e)
		}
	}
	if _, err := p.client.Restore(p.ctx, restic.RestoreOptions{ID: target.ID, Dir: dir, Excludes: excludes}); err != nil {
		return fmt.Errorf("%w. %s may be partly updated; its state from before is snapshot %s", err, dir, base[:8])
	}

	// From here on the folder follows the rules its snapshot was taken with.
	if !slices.Equal(f.Ignore, target.Excludes) {
		for i := range p.cfg.Folders {
			if p.cfg.Folders[i].String() == f.String() {
				p.cfg.Folders[i].Ignore = slices.Clone(target.Excludes)
				if p.cfg.Folders[i].Ignore == nil {
					p.cfg.Folders[i].Ignore = []string{}
				}
				*f = p.cfg.Folders[i]
			}
		}
		if err := p.cfg.Save(p.cfgPath); err != nil {
			return err
		}
		fmt.Fprintf(p.out, "%s: its ignore rules are now %s\n", f, strings.Join(f.Ignore, ", "))
	}
	return p.record(*f, dir, target, isMaster)
}

// descends reports whether snapshot id has ancestor somewhere up its chain
// of parents. parents maps each snapshot to its parent.
func descends(parents map[string]string, id, ancestor string) bool {
	for steps, cur := 0, parents[id]; cur != "" && steps <= len(parents); steps, cur = steps+1, parents[cur] {
		if cur == ancestor {
			return true
		}
	}
	return false
}

// commonAncestor returns the nearest snapshot both a and b descend from,
// or "" if their chains never meet.
func commonAncestor(parents map[string]string, a, b string) string {
	seen := map[string]bool{}
	for steps, cur := 0, a; cur != "" && steps <= len(parents); steps, cur = steps+1, parents[cur] {
		seen[cur] = true
	}
	for steps, cur := 0, b; cur != "" && steps <= len(parents); steps, cur = steps+1, parents[cur] {
		if seen[cur] {
			return cur
		}
	}
	return ""
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
