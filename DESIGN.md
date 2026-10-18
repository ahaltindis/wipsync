# wipsync design

Work on one machine, continue on another. Syncs whole folders, including uncommitted and untracked
files and `.git`, through an always-on home server.

Status: `init`, `add`, `list`, `snapshot`, `push`, `pull`, `restore`, `log`, `watch` and `doctor`
work. Pull does not merge yet: when both sides changed it stops, and `restore` takes one side as it
is. `diff` is not built yet. `wipsync tui` does the same from one screen, and
adds folders from a directory browser. `wipsync watch`, run by a user service, snapshots folders
as they change, and shows its state as a tray icon.

## Model

- A **folder** is known to all clients by its **root** and its path under it, for example `Projects`
  and `Freelance/kadir/boxing-machine`. The root is the highest parent directory, and each client
  maps it to its own location. A folder doesn't have to be a git repo, and can't be inside another
  synced folder.
- A **snapshot** is the complete state of a folder at one moment. It records its client and its
  **parent**: the snapshot the folder was based on.
- **WIP**: each client snapshots its folders to the server automatically after local changes. A client
  only writes its own WIP line, so the automatic part can't conflict. No locks, no read-only mode.
- **master**: a pointer to one snapshot per folder, moved by a manual **push**. It marks a known good
  state. A push takes no snapshot and uploads nothing: it points master at the snapshot the client
  last took, so changes made since then are not part of it.
- **pull**: manual. Takes master or another client's latest WIP into the local folder. On a client
  that does not have the folder yet, pull asks where its root is, creates the folder, and adopts the
  ignore rules its snapshot was taken with.

## Rules

- A push is accepted only if master is still where this client last saw it: the snapshot it last
  pushed or pulled. Otherwise pull first.
- Before a pull or a restore changes anything, the local state is saved as a WIP snapshot, so both
  can be undone with `restore`.
- When another client has newer WIP or master moved, the client notifies. It never pulls by itself.
- Change detection uses snapshots and parents, not mtime (clocks differ, git rewrites mtimes).
- A WIP snapshot may catch a folder mid-save or mid-rebase. The next one corrects it; push at a quiet moment.
- Ignore rules apply to restore as well as snapshot. A restore deletes what is not in the snapshot,
  so an ignored directory would be wiped otherwise.
- A snapshot is never changed after it is created. Its ID is its name.
- No snapshot is made when nothing changed. wipsync decides that itself, from a fingerprint of the
  folder, before it calls the engine.
- Only one wipsync works on a folder at a time: snapshot, push, pull and restore take a lock on the
  folder in the client's state (`flock`, let go by the kernel however the process ends). They
  refuse a busy folder at once; the service tries it again later. The lock is always taken before
  restic's repository lock and the server's master lock, and nothing waits on it, so the three
  cannot deadlock.

## Pull when both sides changed

Not built yet: pull saves the local state and stops. It tells the cases apart by the snapshots'
parents: if the target descends from the folder's snapshot, only the other side changed, and the
folder is made to match it.

Compared per file against the common base snapshot:

| Change since base | Result |
|---|---|
| Only the other side | Take theirs |
| Only local | Keep local |
| Both, text file | Three-way merge, or show the conflict |
| Both, binary file | Keep both |
| Both changed `.git` | No merge: replace, or keep the other as a second folder |

## Parts

- **Server**: storage reached over ssh. Nothing of wipsync is installed there: for what must be
  atomic, the push check, the client runs a short shell script over ssh. The server needs only `sh`
  and `flock`, and neither restic nor the repository password. No daemon; a server-side command
  can be added later if needed. Only ssh and local-disk repositories are supported.
- **Client**: a user service that snapshots the folders after changes, plus a CLI: `init`, `add`,
  `list`, `snapshot`, `log`, `diff`, `push`, `pull`, `restore`, `watch`, and `doctor` to check the
  setup. Written in Go with cobra.
- **Service**: `wipsync watch`, run by the user unit `wipsync.service`. It polls: every minute it
  takes each folder's fingerprint (about 20 ms for a project folder), and snapshots a changed one
  once it has been quiet for 30 seconds, or after 5 minutes if it keeps changing. inotify was left
  out: it would only shorten that delay, at the cost of a watch per directory and rescans for what
  it misses. Before sending anything it asks the server, with one ssh call and a 10 second connect
  timeout; while the server does not answer it sends nothing and asks again after 5 minutes, then
  10, 20, and every 30. It logs to the journal, each failure once. It pulls or pushes only when
  asked: from the tray's menu, or on its socket by the CLI and the TUI.
  Every 5 minutes it reads the repository as the TUI does, and tells on the desktop
  (`notify-send`, one notification per check) what is new: master moved past this client's folder
  (urgent when the folder changed too), newer work on another client, a folder added on another
  client. What it told is kept only while it runs, so after a restart it tells again what is
  still due; folders not on the client when it starts are not told as new. Newer work is told at
  most once an hour per folder.
- **TUI**: `wipsync tui`, in the same binary, built with Bubble Tea. Folders under their roots on
  the left, coloured by what is due (snapshot, push, pull); the selected folder's snapshots on the
  right. Keys act on the selection: `s` snapshot, `P` push, `p` pull (master, or a client's latest),
  `r` restore; on a root, `P` and `p` push or pull all of it. Pull and restore ask first. Space marks lines, and
  the keys then act on everything marked. `/` searches the left pane as it is typed. `Tab` turns the left pane
  into a directory browser, where roots, tracked folders, other directories and files each have
  their colour, and `a` adds the selected directory. `o` opens the directory in the TUI's
  terminal until it exits (`yazi`), `O` in a window of its own (`xdg-open`); both commands are set in the
  config's `[open]` as `window` and `here`. The bottom line shows only `? help  q quit`; `?` lists
  every key, with those that do nothing where the selection is dimmed. Colours come from
  `theme.toml` beside the config.
- **Config**: `~/.config/wipsync/config.toml` holds only what the user sets: `[open]` and `[tray]`.
  wipsync reads it and never writes it, so it can be shipped ready made, and anything left out has
  a default. Colours are in `theme.toml` beside it.
- **Database**: `~/.local/share/wipsync/wipsync.db` (`$XDG_DATA_HOME`), SQLite through
  `modernc.org/sqlite` (pure Go, so the release stays without cgo), in WAL mode so that the
  service, the TUI and the CLI can write at once. It holds everything wipsync writes: the client
  name, the repository and where its password comes from (`init`), the roots and the folders with
  their ignore rules (`add`, `pull`), and per folder the snapshot it is based on, the master it
  last saw, its fingerprint and whether it was pulled since its last snapshot. A pull that adds a
  folder records it and its state in one transaction. Data, not state: losing it loses the folder
  list.
- **Password**: in a file of its own, `~/.local/share/wipsync/password` (mode 600), written by
  `init`; the database holds its path, or the command that prints it, handed to restic as is. Not
  in the database, so that copying or opening the database for a look does not hand it out.
- **Locks**: `~/.local/state/wipsync/`, one `flock` file per folder, outside the folder, where a
  pull would overwrite it. Not in the database: the kernel lets a `flock` go however the process
  ends.
- **Moving from config.toml**: when the database does not exist and `config.toml` has a
  repository or folders, the first command imports them and the per-folder files under
  `~/.local/state/wipsync/folders/`, in one transaction. A password file that `init` kept beside
  the config moves beside the database; one elsewhere stays where it is. Once the import is
  saved, the old password file and the per-folder files are removed, all but the locks.
  `config.toml` is not changed: while it still has those keys, every run warns that they are
  ignored and can be removed.
- **Tray**: `wipsync watch` shows an icon in the desktop's tray (StatusNotifierItem over D-Bus,
  `fyne.io/systray`), so any bar with a tray shows it, not only waybar. One symbol, a W whose tips
  run on into two arrows round a circle, coloured by state, so that a glance says whether anything is to be done; when several
  apply, the first wins: red server unreachable, yellow snapshotting or an action running, purple
  a pull is due (master moved past a folder here, or another client has newer work, as of the
  last check), blue a push is due (a folder has snapshots master does not as of the last check,
  or changed since), light grey (`#d0d0d0`, the bar's own colour) nothing to do. The tooltip is the state and the last 10 things the watcher logged, with
  their times. A right click opens a menu with Pull all and Push all, greyed out while one runs;
  the service runs it between its rounds, logs a line per folder, tells the summary on the
  desktop (urgent when a folder failed), and reads the repository again at once. A left click
  runs `[tray] open`, by default
  `systemd-run --user --collect xdg-terminal-exec wipsync tui`: the service's unit stops everything
  in it with it (`KillMode=mixed`), so what the click opens must run outside it. `[tray] hide = true`
  shows no icon; that is also where a bar-specific output would go. Without a session bus or a
  tray it does nothing, and registers when a tray appears. The tray settings are read at start.
- **Pull all, push all**: one command in `app`, `PullAll` and `PushAll`, which every front end
  runs with a scope: `wipsync pull --all` and `push --all` (every folder), the TUI's `P` and `p`
  on a root or on marked folders, and the tray's menu (every folder). It reads the repository
  once, acts on each folder that has something due, goes on past failures, and returns a line per
  folder and a summary, which each front end shows as is. Pull all takes master into folders that
  are behind it and into those not here yet whose root has a directory here; a folder that also
  changed here, or whose root has none, is left alone with a line on why. Push all makes the
  latest snapshot master for folders that have one master does not; a folder changed since that
  snapshot fails, as does one whose master another client moved, and a later push all takes it.
  The CLI exits non-zero when a folder failed. While `wipsync watch` runs, the CLI's `--all` and
  the TUI's pull and push of a root or of marked folders hand the work to it over a Unix socket,
  scope included, so that its icon shows it and the desktop is told, as with the tray's menu;
  they show the result the service sends back. The socket
  is `$XDG_RUNTIME_DIR/wipsync/watch-<hash>.sock`, named after the database, so a client with
  another database never reaches the service. A request is one JSON line, and so is the answer,
  sent when the action is done; requests wait in the same queue as the menu's. When nothing
  answers, the CLI or the TUI does the work itself; when the service stops before it answers,
  they say so and do not run it again.
- **Log**: what was sent and when, per folder.
- **Ignore rules** per folder with defaults (`node_modules`, virtualenvs, `__pycache__`).
- **Menu**: Archype opens a vicinae dmenu on `SUPER + W` with the TUI, pull all and push all,
  which run `wipsync pull --all` and `push --all`; through the socket, the tray icon shows them.

## Engine

Don't write the file transfer. **restic** does it: atomic snapshots per host, deduplication across
clients, diff, restore, pruning, encrypted on the server, only needs ssh.

wipsync runs the restic binary, 0.17.1 or newer. restic can't be used as a Go library: its packages
are all internal.

How wipsync uses it:

- Snapshot: from inside the folder, `backup . --host <client> --parent <base> --skip-if-unchanged`,
  with the folder's ignore rules. With an absolute path the snapshot includes the directories above
  the folder, and a change beside the folder makes a new snapshot.
- One repository for all folders. A snapshot is tagged with its folder when it is created:
  `root=Projects`, `path=Freelance/kadir/boxing-machine`.
- Parent: always passed with `--parent`, also when it is another client's snapshot. restic's own
  choice goes by host and path only.
- Restore: `restore <id> --target <folder> --delete`, with the same ignore rules. The folder's own
  mode and mtime are not restored.
- After a pull every file has a new ctime and inode. The next snapshot passes `--ignore-ctime
  --ignore-inode` so files are not reread.
- Fingerprint: every file's path, size, mode and timestamps, kept in the client's state. An
  unchanged fingerprint means no restic call. It leaves out what would make restic save a snapshot
  with nothing in it: directory timestamps, which every `git status` moves, and the stat cache in
  `.git/index`, which git rewrites after a pull. The index counts by what is staged in it.
- master: a file per folder holding a snapshot ID, in `<repository>.wipsync/` beside the
  repository. Only the push script writes it, under a lock: move it if it still equals what the
  caller expects, otherwise reject. The client checks that the snapshot exists; the script does not.
- Not tags: changing a tag gives the snapshot a new ID and leaves other snapshots' parents pointing
  at the old one.
- Not tree hashes for "same state": they cover each file's ctime and inode, which differ on every
  machine. Compare with `diff`.

### Prototype (2026-10-12)

restic 0.19.1, warm cache. Local disk, and over ssh to a server 7 ms away. One folder: 636 MiB in
3811 files; its `.venv` holds 3559 of them and nearly all the size.

| Measured | Local | Over ssh | Result |
|---|---|---|---|
| First snapshot | 4.4s | 7.0s | stored as 231M |
| Snapshot, nothing changed | 0.84s | 1.2s | nothing uploaded |
| Snapshot after a small change | 0.91s | 1.3s | a few KB stored |
| Snapshot without `.venv` | 0.68s | 1.1s | |
| Restore into an empty directory | 3.3s | 5.6s | identical |
| Restore over a drifted folder | 2.2s | 2.5s | identical, `.git` included |
| Restore of a snapshot without `.venv` | | | deletes the local `.venv` unless excluded on restore too |
| Change beside the folder, none inside | | | absolute path: new snapshot. Relative: skipped |
| First snapshot after a pull | 1.8s, 0.7s ignoring ctime and inode | | saved either way, 357 KiB |
| Second client, same content | 1.9s | | 3 KB added |
| Two clients at once | | | both succeed, `restic check` clean |
| 20 pushes racing on the master file | | | 1 accepted, 19 rejected, in 50 of 50 rounds |

Still to verify:

1. With a cold cache, on a much larger folder, and over a slow link.
2. Pruning while clients write (it takes an exclusive lock). A killed restic leaves its lock
   behind: snapshots ignore it, pruning refuses to start. So pruning runs `restic unlock` first,
   which removes only locks older than 30 minutes or from a process that is gone.
3. Changing an ignore rule after snapshots exist.

Fallback: rsync over ssh with hard-link snapshots on the server.

### Tray spike (2026-10-18)

`fyne.io/systray` 1.12.2, run as a transient user service on Hyprland with waybar's tray.

| Checked | Result |
|---|---|
| Registers with the tray from a user service | yes |
| Registers again after waybar restarts | yes, the library follows the watcher's name on the bus |
| Icon and tooltip change while running | yes |
| Tooltip with several lines | yes, all shown |
| Left click reaches our code | yes; with a click handler the item is not a menu |
| Right click | an empty menu |
| What a click opens outlives the service | yes, with `systemd-run --user` |

## Packaging

Own repo, released to the AUR as `wipsync-bin`: pushing a tag builds the binary in a GitHub
workflow, and `release.sh` updates the AUR package to it. Archype only installs and configures it.
Pairing a client with the server is a manual one-time step. Each client gets its own repository password, so one client can be
removed without touching the others.

## Open

- How many WIP snapshots to keep, and for how long.
- Whether secrets in synced folders (`.env`) need more than the engine's encryption.
- Whether the push check also verifies that the new snapshot exists and descends from the base.
