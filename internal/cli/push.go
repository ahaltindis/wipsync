package cli

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/ahaltindis/wipsync/internal/master"
	"github.com/ahaltindis/wipsync/internal/restic"
	"github.com/ahaltindis/wipsync/internal/state"
)

func pushCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "push [folder]",
		Short: "Mark a folder's latest snapshot as master",
		Long: `Mark a folder's latest snapshot as master, its known good state.

Push takes no snapshot and uploads nothing: it moves master to the snapshot
this client last took of the folder. Changes made since that snapshot are
not part of it; run "wipsync snapshot" first to include them.

The server accepts the push only if master is still where this client last
saw it. If another client pushed in between, pull first.`,
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
			f, err := resolveFolder(cfg, arg)
			if err != nil {
				return err
			}
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
			store, err := state.Default()
			if err != nil {
				return err
			}
			base, err := store.Base(f.Root, f.Path)
			if err != nil {
				return err
			}
			if base == "" {
				return fmt.Errorf("%s has no snapshot from this client yet; run wipsync snapshot first", f)
			}
			expect, err := store.Master(f.Root, f.Path)
			if err != nil {
				return err
			}
			// Looked up to say exactly what is being pushed, and because a
			// snapshot that is gone must not become master.
			snap, err := client.Find(ctx, base)
			if errors.Is(err, restic.ErrNoSnapshot) {
				return fmt.Errorf("%s: its latest snapshot %s is not in the repository", f, base[:8])
			} else if err != nil {
				return err
			}
			what := describe(snap)

			out := cmd.OutOrStdout()
			err = pointers.Move(ctx, f.Root, f.Path, expect, base)
			var moved *master.MovedError
			switch {
			case errors.As(err, &moved) && moved.Current == base:
				// Master is already there: an earlier push moved it but
				// was cut off before recording that here.
				err = nil
				fallthrough
			case err == nil && expect == base:
				fmt.Fprintf(out, "%s: master is already %s\n", f, what)
			case err == nil:
				fmt.Fprintf(out, "%s: master is now %s\n", f, what)
			case errors.As(err, &moved) && moved.Current == "":
				return fmt.Errorf("%s: push rejected: this client was in step with master %s, but the server has no master for the folder", f, expect[:8])
			case errors.As(err, &moved):
				return fmt.Errorf("%s: push rejected: another client moved master to %s; pull first", f, moved.Current[:8])
			default:
				return err
			}
			return store.SetMaster(f.Root, f.Path, base)
		},
	}
}

// age describes how long ago something happened.
func age(d time.Duration) string {
	plural := func(n int, unit string) string {
		if n == 1 {
			return fmt.Sprintf("1 %s ago", unit)
		}
		return fmt.Sprintf("%d %ss ago", n, unit)
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return plural(int(d/time.Minute), "minute")
	case d < 48*time.Hour:
		return plural(int(d/time.Hour), "hour")
	default:
		return plural(int(d/(24*time.Hour)), "day")
	}
}
