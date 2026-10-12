# wipsync design

Work on one machine, continue on another. Syncs whole folders, including uncommitted and untracked
files and `.git`, through an always-on home server.

Status: design only. Nothing is built or tested yet.

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

- **Server**: storage reached over ssh. No daemon if the engine allows it.
- **Client**: a user service that watches the folders and snapshots after changes (debounced), plus a
  CLI: `add`, `status`, `log`, `diff`, `push`, `pull`, `restore`.
- **Log**: what was sent and when, per folder.
- **Ignore rules** per folder with defaults (`node_modules`, virtualenvs, build output).
- Later: waybar indicator, vicinae command.

## Engine

Don't write the file transfer. Candidate: **restic** (atomic snapshots tagged by host, deduplication
across clients, diff, restore, pruning, encrypted on the server, only needs ssh).

To verify with a prototype on one real project:

1. Time for a snapshot of a large folder after a small change.
2. Restore makes a folder exactly match a snapshot, including deletions.
3. Where parent and master pointers live (restic tags, or a small file next to the repository).

Fallback: rsync over ssh with hard-link snapshots on the server.

## Packaging

Own repo, released to the AUR. Archype only installs and configures it. Pairing a client with the
server is a manual one-time step.

## Open

- Language for the client.
- How many WIP snapshots to keep, and for how long.
- Whether secrets in synced folders (`.env`) need more than the engine's encryption.
