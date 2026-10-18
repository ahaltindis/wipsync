# wipsync
Sync work-in-progress between different machines. See [DESIGN.md](DESIGN.md).

## Install

From the AUR package `wipsync-bin`, which installs the released binary and shell completions:

```sh
yay -S wipsync-bin
wipsync init      # once per machine: the repository password and location
wipsync doctor    # check the setup
```

What wipsync keeps (the repository, the folders, what it last saw of each) is in
`~/.local/share/wipsync/wipsync.db`, with the repository password beside it. `~/.config/wipsync/config.toml`
is only for your settings, and wipsync never writes it.

To snapshot folders automatically as they change:

```sh
systemctl --user enable --now wipsync
journalctl --user -u wipsync -f   # what it does
```

The service runs without a terminal, so ssh must reach the server without asking anything: a key
without a passphrase, or an agent the user session already has.

The service shows an icon in the desktop's tray: blue up to date, purple behind, yellow
snapshotting, red server unreachable. Hovering lists what it did last; a left click opens
`wipsync tui` in a terminal. To change the click or hide the icon, in the config:

```toml
[tray]
open = "systemd-run --user --collect footclient wipsync tui"  # start it outside the service
hide = true
```

The server needs nothing installed beyond ssh, `sh` and `flock`.

## Development

Needs Go, and restic 0.17.1 or newer. Tests that use restic are skipped without it.

```sh
go build -o wipsync ./cmd/wipsync   # build
go test ./...                       # test
go vet ./... && gofmt -l .          # check
```

To try it without touching your real config, database and state:

```sh
export XDG_DATA_HOME=/tmp/try-data XDG_STATE_HOME=/tmp/try-state
./wipsync --config /tmp/try.toml init
./wipsync --config /tmp/try.toml add <folder>
./wipsync --config /tmp/try.toml snapshot <folder>
./wipsync --config /tmp/try.toml doctor
```

To install the working tree as a package:

```sh
pkg/test-local
```

Builds wipsync, packs it like a release and installs it with `pkg/PKGBUILD` via `makepkg -si`, with the
tarball from the working tree instead of GitHub.

## Releasing

```sh
./release.sh v0.1.0
```

From a clean, pushed `main`, it:

1. Sets the version in `pkg/PKGBUILD`, commits and pushes.
2. Creates and pushes the tag, then waits for the release workflow to build wipsync and attach
   `wipsync-<version>-x86_64.tar.gz` (and its `.sha256`) to a GitHub release.
3. Puts the checksum in `pkg/PKGBUILD`, regenerates `pkg/.SRCINFO`, commits and pushes.
4. Clones the AUR package to a temporary folder, copies `PKGBUILD` and `.SRCINFO` there, commits and pushes.
