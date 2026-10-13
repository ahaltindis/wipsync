# wipsync design

Work on one machine, continue on another. Syncs whole folders, including uncommitted and untracked
files and `.git`, through an always-on home server.

Status: design, plus a local prototype of the engine (see Engine). No client or server code yet.

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
  state; nothing is uploaded twice.
- **pull**: manual. Takes master or another client's latest WIP into the local folder.

## Rules

- A push is accepted only if master is still the snapshot the local folder is based on. Otherwise pull
  first.
- Before a pull changes anything, the local state is saved as a WIP snapshot, so every pull can be undone.
- When another client has newer WIP or master moved, the client notifies. It never pulls by itself.
- Change detection uses snapshots and parents, not mtime (clocks differ, git rewrites mtimes).
- A WIP snapshot may catch a folder mid-save or mid-rebase. The next one corrects it; push at a quiet moment.
- Ignore rules apply to restore as well as snapshot. A restore deletes what is not in the snapshot,
  so an ignored directory would be wiped otherwise.
- A snapshot is never changed after it is created. Its ID is its name.
- No snapshot is made when nothing changed.

## Pull when both sides changed

Compared per file against the common base snapshot:

| Change since base | Result |
|---|---|
| Only the other side | Take theirs |
| Only local | Keep local |
| Both, text file | Three-way merge, or show the conflict |
| Both, binary file | Keep both |
| Both changed `.git` | No merge: replace, or keep the other as a second folder |

## Parts

- **Server**: storage reached over ssh, plus the same CLI run over ssh for what must be atomic: the
  push check. No daemon; one can be added later if needed.
- **Client**: a user service that watches the folders and snapshots after changes (debounced), plus a
  CLI: `init`, `add`, `snapshot`, `status`, `log`, `diff`, `push`, `pull`, `restore`, and `doctor`
  to check the setup. Written in Go with cobra, one binary for both sides.
- **Config**: `~/.config/wipsync/config.toml` holds the server, the folders and their ignore rules,
  and where the repository password comes from: `password_file` or `password_command`, handed to
  restic as is.
- **State**: `~/.local/state/wipsync/` holds, per folder, the snapshot it is based on. Not inside the
  folder, where a pull would overwrite it.
- **Log**: what was sent and when, per folder.
- **Ignore rules** per folder with defaults (`node_modules`, virtualenvs, `__pycache__`).
- Later: waybar indicator, vicinae command.

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
  --ignore-inode` so files are not reread; it is still saved, with unchanged content.
- master: a file per folder next to the repository, holding a snapshot ID. Only the server command
  writes it, under a lock: move it if it still equals the caller's base, otherwise reject.
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

## Packaging

Own repo, released to the AUR. Archype only installs and configures it. Pairing a client with the
server is a manual one-time step. Each client gets its own repository password, so one client can be
removed without touching the others.

## Open

- How many WIP snapshots to keep, and for how long.
- Whether secrets in synced folders (`.env`) need more than the engine's encryption.
- Whether the push check also verifies that the new snapshot exists and descends from the base.
