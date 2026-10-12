# wipsync design

Work on one machine, continue on another. Syncs whole folders, including uncommitted and untracked
files and `.git`, through an always-on home server.

Status: design, plus a local prototype of the engine (see Engine). No client or server code yet.

## Model

- A **folder** is added on each client with the same path. It doesn't have to be a git repo.
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
  CLI: `add`, `status`, `log`, `diff`, `push`, `pull`, `restore`. Written in Go, one binary for both
  sides.
- **Log**: what was sent and when, per folder.
- **Ignore rules** per folder with defaults (`node_modules`, virtualenvs, build output).
- Later: waybar indicator, vicinae command.

## Engine

Don't write the file transfer. **restic** does it: atomic snapshots per host, deduplication across
clients, diff, restore, pruning, encrypted on the server, only needs ssh.

How wipsync uses it:

- Snapshot: `backup --host <client> --skip-if-unchanged`, with the folder's ignore rules.
- Parent: restic records one per snapshot. After a pull, the next snapshot names the pulled one with
  `--parent`, also when it is another client's.
- Restore: `restore <id>:<folder> --delete`, with the same ignore rules. The folder's own mode and
  mtime are not restored.
- master: a file per folder next to the repository, holding a snapshot ID. Only the server command
  writes it, under a lock: move it if it still equals the caller's base, otherwise reject.
- Not tags: changing a tag gives the snapshot a new ID and leaves other snapshots' parents pointing
  at the old one.
- Not tree hashes for "same state": directory timestamps change them, including directories above
  the folder.

### Prototype (2026-10-12)

restic 0.19.1, local disk, warm cache, single runs. One folder: 636 MiB in 3811 files; its `.venv`
holds 3559 of them and nearly all the size.

| Measured | Result |
|---|---|
| First snapshot | 4.4s, stored as 231M |
| Snapshot, nothing changed | 0.84s, nothing uploaded |
| Snapshot after a small change | 0.91s, 5 KB stored |
| Snapshot without `.venv` | 0.68s |
| Restore into an empty directory | 3.3s, identical |
| Restore over a drifted folder | 2.2s, identical, `.git` included |
| Restore of a snapshot without `.venv` | deletes the local `.venv` unless excluded on restore too |
| Second client, same content | 1.9s, 3 KB added |
| Two clients at once | both succeed, `restic check` clean |
| 20 pushes racing on the master file | 1 accepted, 19 rejected, in 50 of 50 rounds |

Still to verify:

1. The same over ssh, with a cold cache, and on a much larger folder.
2. Pruning while clients write (it takes an exclusive lock).
3. Snapshots with relative paths, so directories above the folder stay out.
4. Changing an ignore rule after snapshots exist.

Fallback: rsync over ssh with hard-link snapshots on the server.

## Packaging

Own repo, released to the AUR. Archype only installs and configures it. Pairing a client with the
server is a manual one-time step.

## Open

- How many WIP snapshots to keep, and for how long.
- Whether secrets in synced folders (`.env`) need more than the engine's encryption.
- Whether the push check also verifies that the new snapshot exists and descends from the base.
