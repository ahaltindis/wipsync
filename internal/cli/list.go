package cli

import (
	"fmt"
	"io"
	"slices"

	"github.com/spf13/cobra"

	"github.com/ahaltindis/wipsync/internal/config"
	"github.com/ahaltindis/wipsync/internal/master"
	"github.com/ahaltindis/wipsync/internal/restic"
)

func listCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the folders in the repository, and where each client and master are",
		Long: `List the folders in the repository, and where each client and master are.

Under each folder is every client's latest snapshot, newest first, with
this client in [brackets], and "master" on the snapshot that is master. A folder that is
not on this client yet can be fetched with "wipsync pull".`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath, cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			client, err := resticClient(cfgPath, cfg)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			if err := restic.CheckVersion(ctx); err != nil {
				return err
			}
			all, err := client.Snapshots(ctx, nil)
			if err != nil {
				return err
			}
			// The listing is still useful when master cannot be read.
			var masters map[string]string
			pointers, masterErr := master.Open(cfg.Repository)
			if masterErr == nil {
				masters, masterErr = pointers.All(ctx)
			}

			folders := map[string]*listed{}
			folder := func(name string) *listed {
				if folders[name] == nil {
					folders[name] = &listed{latest: map[string]restic.Snapshot{}}
				}
				return folders[name]
			}
			byID := map[string]restic.Snapshot{}
			// Snapshots come oldest first, so the last one seen per client
			// is its latest.
			for _, s := range all {
				if f, ok := folderOf(s.Tags); ok {
					folder(f.String()).latest[s.Host] = s
					byID[s.ID] = s
				}
			}
			for _, f := range cfg.Folders {
				folder(f.String()).dir = cfg.Dir(f)
			}

			out := cmd.OutOrStdout()
			if len(folders) == 0 {
				fmt.Fprintln(out, "no folders yet")
				return nil
			}
			names := make([]string, 0, len(folders))
			nameWidth := 0
			// The locations line up among the folders that have one.
			for name, f := range folders {
				names = append(names, name)
				if f.dir != "" {
					nameWidth = max(nameWidth, len(name))
				}
			}
			slices.Sort(names)
			for i, name := range names {
				if i > 0 {
					fmt.Fprintln(out)
				}
				f := folders[name]
				if f.dir == "" {
					fmt.Fprintln(out, name)
				} else {
					fmt.Fprintf(out, "%-*s    %s\n", nameWidth, name, f.dir)
				}
				f.print(out, cfg, masters[name], byID, masterErr == nil)
			}
			if masterErr != nil {
				fmt.Fprintf(out, "\nmaster is unknown: %v\n", masterErr)
			}
			return nil
		},
	}
}

// listed is what list shows for one folder.
type listed struct {
	dir    string                     // where it is on this client, if it is
	latest map[string]restic.Snapshot // each client's latest snapshot
}

// print writes the folder's lines: every client's latest snapshot, newest
// first, with master added if it is not one of those. Lines that are not a
// snapshot come last.
func (f *listed) print(out io.Writer, cfg *config.Config, masterID string, byID map[string]restic.Snapshot, masterKnown bool) {
	name := func(client string) string {
		if client == cfg.Client {
			return "[" + client + "]"
		}
		return client
	}

	shown := make([]restic.Snapshot, 0, len(f.latest)+1)
	for _, s := range f.latest {
		shown = append(shown, s)
	}
	masterSnap, masterFound := byID[masterID]
	if masterFound && f.latest[masterSnap.Host].ID != masterID {
		// Master is an older snapshot of its client, which gets a second line.
		shown = append(shown, masterSnap)
	}
	slices.SortFunc(shown, func(a, b restic.Snapshot) int { return b.Time.Compare(a.Time) })

	type row struct{ who, what string }
	var rows []row
	for _, s := range shown {
		what := fmt.Sprintf("%s  %s", s.ID[:8], s.Time.Local().Format("2006-01-02 15:04"))
		if s.ID == masterID {
			what += "  master"
		}
		rows = append(rows, row{name(s.Host), what})
	}
	if _, ok := f.latest[cfg.Client]; !ok {
		what := "not on this client yet"
		if f.dir != "" {
			what = "no snapshot yet"
		}
		rows = append(rows, row{name(cfg.Client), what})
	}
	switch {
	case !masterKnown || masterFound:
	case masterID == "":
		rows = append(rows, row{"*", "no master yet"})
	default:
		rows = append(rows, row{"*", fmt.Sprintf("%s  not among the folder's snapshots  master", masterID[:8])})
	}

	width := 0
	for _, r := range rows {
		width = max(width, len(r.who))
	}
	for _, r := range rows {
		fmt.Fprintf(out, "  %-*s  %s\n", width, r.who, r.what)
	}
}
