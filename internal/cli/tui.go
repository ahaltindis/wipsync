package cli

import (
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/ahaltindis/wipsync/internal/tui"
)

func tuiCmd() *cobra.Command {
	var printTheme bool
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Browse and sync the folders in the terminal",
		Long: `Browse and sync the folders in the terminal.

The left pane lists the folders under their roots, coloured by what is due:
a snapshot, a push or a pull. The right pane shows the selected folder's
snapshots from all clients. The bottom line names the keys that act on what
is selected.

Colours come from theme.toml beside the config file. Start one with:
  wipsync tui --print-theme > ~/.config/wipsync/theme.toml`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if printTheme {
				fmt.Fprint(cmd.OutOrStdout(), tui.DefaultThemeFile())
				return nil
			}
			a, err := open(cmd)
			if err != nil {
				return err
			}
			// The interface owns the terminal: nothing it starts may use it.
			a.NoTerminal = true
			if _, err := a.Client(); err != nil {
				return err
			}
			theme, err := tui.LoadTheme(filepath.Join(filepath.Dir(a.ConfigPath), "theme.toml"))
			if err != nil {
				return err
			}
			if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
				return errNoTerminal
			}
			_, err = tea.NewProgram(tui.New(a, service{a}, theme), tea.WithAltScreen(), tea.WithContext(cmd.Context())).Run()
			return err
		},
	}
	cmd.Flags().BoolVar(&printTheme, "print-theme", false, "print the default theme and exit")
	return cmd
}
