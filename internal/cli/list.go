package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/ahaltindis/wipsync/internal/app"
)

func listCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the folders in the repository, and where each client and master are",
		Long: `List the folders in the repository, and where each client and master are.

Under each folder is every client's latest snapshot, newest first, with
this client in [brackets], and "master" on the snapshot that is master. A
folder that is not on this client yet can be fetched with "wipsync pull".`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := open(cmd)
			if err != nil {
				return err
			}
			o, err := a.List(cmd.Context())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(o.Folders) == 0 {
				fmt.Fprintln(out, "no folders yet")
				return nil
			}
			// The locations line up among the folders that have one.
			nameWidth := 0
			for _, v := range o.Folders {
				if v.Dir != "" {
					nameWidth = max(nameWidth, len(v.Folder.String()))
				}
			}
			for i, v := range o.Folders {
				if i > 0 {
					fmt.Fprintln(out)
				}
				if v.Dir == "" {
					fmt.Fprintln(out, v.Folder)
				} else {
					fmt.Fprintf(out, "%-*s    %s\n", nameWidth, v.Folder, v.Dir)
				}
				printFolder(out, v, a.Data.Client, o.MasterErr == nil)
			}
			if o.MasterErr != nil {
				fmt.Fprintf(out, "\nmaster is unknown: %v\n", o.MasterErr)
			}
			return nil
		},
	}
}

// printFolder writes the folder's lines: every client's latest snapshot,
// newest first, with master added if it is not one of those. Lines that
// are not a snapshot come last.
func printFolder(out io.Writer, v app.FolderView, client string, masterKnown bool) {
	name := func(c string) string {
		if c == client {
			return "[" + c + "]"
		}
		return c
	}
	type row struct{ who, what string }
	var rows []row
	for _, s := range v.Shown() {
		what := fmt.Sprintf("%s  %s", s.ID[:8], s.Time.Local().Format("2006-01-02 15:04"))
		if s.ID == v.Master {
			what += "  master"
		}
		rows = append(rows, row{name(s.Host), what})
	}
	if _, ok := v.Latest[client]; !ok {
		what := "not on this client yet"
		if v.Dir != "" {
			what = "no snapshot yet"
		}
		rows = append(rows, row{name(client), what})
	}
	switch {
	case !masterKnown || v.MasterSnapshot != nil:
	case v.Master == "":
		rows = append(rows, row{"*", "no master yet"})
	default:
		rows = append(rows, row{"*", fmt.Sprintf("%s  not among the folder's snapshots  master", v.Master[:8])})
	}

	width := 0
	for _, r := range rows {
		width = max(width, len(r.who))
	}
	for _, r := range rows {
		fmt.Fprintf(out, "  %-*s  %s\n", width, r.who, r.what)
	}
}
