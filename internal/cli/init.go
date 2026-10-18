package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ahaltindis/wipsync/internal/db"
	"github.com/ahaltindis/wipsync/internal/restic"
)

func initCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Set up this client: the repository password and location",
		Long: `Set up this client: the repository password and location.

Asks where the repository password comes from and where the repository is,
creates the repository if there is none yet, and saves both in the database.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := open(cmd)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			p := newPrompter(cmd.Context(), cmd.InOrStdin(), out)
			if !p.interactive() {
				return errNoTerminal
			}
			ctx := cmd.Context()
			if err := restic.CheckVersion(ctx); err != nil {
				return err
			}
			if a.Data.Repository != "" {
				again, err := p.confirm("Already set up for "+a.Data.Repository+". Set it up again?", false)
				if err != nil || !again {
					return err
				}
			}

			client := &restic.Client{}
			passFile := filepath.Join(filepath.Dir(a.DB.Path), "password")
			method, err := p.choose("Where does the repository password come from?", "Method", []string{
				"a file kept by wipsync (" + passFile + ")",
				"a command that prints it, for example from a keyring",
			})
			if err != nil {
				return err
			}
			if method == 0 {
				if err := passwordFile(p, passFile); err != nil {
					return err
				}
				client.PasswordFile = passFile
			} else if client.PasswordCommand, err = p.askRequired("Command: "); err != nil {
				return err
			}

			answer, err := p.askRequired("Repository (user@host:/path, or a local directory): ")
			if err != nil {
				return err
			}
			if client.Repository, err = normalizeRepository(answer); err != nil {
				return err
			}

			err = client.Ping(ctx)
			switch {
			case err == nil:
				fmt.Fprintf(out, "Found the repository at %s, and the password opens it.\n", client.Repository)
			case errors.Is(err, restic.ErrNoRepository):
				create, err := p.confirm("There is no repository at "+client.Repository+". Create it?", true)
				if err != nil {
					return err
				}
				if !create {
					return errors.New("not set up: there is no repository")
				}
				if err := client.Init(ctx); err != nil {
					return err
				}
				fmt.Fprintf(out, "Created the repository at %s.\n", client.Repository)
			case errors.Is(err, restic.ErrWrongPassword):
				return fmt.Errorf("not set up: there is a repository at %s, but this password does not open it. "+
					"On a client that already has access, add this client's password with \"restic key add\", then run init again",
					client.Repository)
			default:
				return err
			}

			settings := db.Settings{
				Client:          a.Data.Client,
				Repository:      client.Repository,
				PasswordFile:    client.PasswordFile,
				PasswordCommand: client.PasswordCommand,
			}
			if err := a.DB.SetSettings(settings); err != nil {
				return err
			}
			fmt.Fprintf(out, "Saved in %s for client %q.\n", a.DB.Path, settings.Client)
			return nil
		},
	}
}

// passwordFile makes sure the password file exists, asking for the password
// unless the file is there and the user keeps it.
func passwordFile(p *prompter, file string) error {
	if _, err := os.Stat(file); err == nil {
		replace, err := p.confirm("A password file exists. Replace it?", false)
		if err != nil || !replace {
			return err
		}
	}
	password, err := p.newPassword()
	if err != nil {
		return err
	}
	dir := filepath.Dir(file)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// CreateTemp makes the file with mode 600.
	tmp, err := os.CreateTemp(dir, ".password-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(password + "\n"); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), file)
}

// resticSchemes are the repository prefixes restic understands.
var resticSchemes = []string{"sftp:", "rest:", "s3:", "b2:", "azure:", "gs:", "swift:", "rclone:", "local:"}

// normalizeRepository turns what the user typed into a restic repository.
// "user@host:/path" is reached over ssh, and anything else without a restic
// prefix is a local directory.
func normalizeRepository(s string) (string, error) {
	for _, scheme := range resticSchemes {
		if strings.HasPrefix(s, scheme) {
			return s, nil
		}
	}
	if rest, ok := strings.CutPrefix(s, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, rest), nil
	}
	local := strings.HasPrefix(s, "/") || strings.HasPrefix(s, ".")
	if host, _, remote := strings.Cut(s, ":"); remote && !local && !strings.Contains(host, "/") {
		return "sftp:" + s, nil
	}
	return filepath.Abs(s)
}
