// Package db keeps what wipsync writes about this client: its setup, its
// folders and what it remembers about each. It is one SQLite database, so
// that the service, the TUI and the CLI can write at once.
package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"
)

// DefaultIgnore is the ignore list a newly added folder starts with.
var DefaultIgnore = []string{"node_modules", ".venv", "venv", "__pycache__"}

const schema = `
CREATE TABLE settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
CREATE TABLE roots (
	name TEXT PRIMARY KEY,
	dir  TEXT NOT NULL
);
CREATE TABLE folders (
	root   TEXT NOT NULL REFERENCES roots (name),
	path   TEXT NOT NULL,
	ignore TEXT NOT NULL, -- a JSON list
	PRIMARY KEY (root, path)
);
CREATE TABLE state (
	root        TEXT NOT NULL,
	path        TEXT NOT NULL,
	base        TEXT NOT NULL DEFAULT '',
	master      TEXT NOT NULL DEFAULT '',
	fingerprint TEXT NOT NULL DEFAULT '',
	after_pull  INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (root, path)
);
`

// DefaultPath returns $XDG_DATA_HOME/wipsync/wipsync.db, which is normally
// ~/.local/share/wipsync/wipsync.db.
func DefaultPath() (string, error) {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "wipsync", "wipsync.db"), nil
}

type DB struct {
	Writer
	Path string
	sql  *sql.DB
}

// Opened databases, by path: a process opens each once, however often it
// asks.
var (
	openMu sync.Mutex
	opened = map[string]*DB{}
)

// Open opens the database at path, creating it if there is none.
func Open(path string) (*DB, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	openMu.Lock()
	defer openMu.Unlock()
	if d := opened[path]; d != nil {
		return d, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	// Writes take the lock when they start, and a busy database is waited
	// for, so that two processes writing at once take turns.
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(10000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Set("_txlock", "immediate")
	s, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	if err := migrate(s); err != nil {
		s.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	d := &DB{Writer: Writer{s}, Path: path, sql: s}
	opened[path] = d
	return d, nil
}

// migrate brings the schema up to date.
func migrate(s *sql.DB) error {
	tx, err := s.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version == 0 {
		if _, err := tx.Exec(schema + "PRAGMA user_version = 1;"); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Update runs fn in one transaction: everything it writes is saved, or
// nothing is.
func (d *DB) Update(fn func(w Writer) error) error {
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(Writer{tx}); err != nil {
		return err
	}
	return tx.Commit()
}

// Settings are how the client is set up.
type Settings struct {
	Client          string // its snapshots' host; the hostname when not set
	Repository      string
	PasswordFile    string // absolute
	PasswordCommand string
}

var settingKeys = []string{"client", "repository", "password_file", "password_command"}

func (s *Settings) fields() []*string {
	return []*string{&s.Client, &s.Repository, &s.PasswordFile, &s.PasswordCommand}
}

// Load reads the settings and the folders.
func (d *DB) Load() (*Data, error) {
	data := &Data{Roots: map[string]string{}}
	rows, err := d.sql.Query("SELECT key, value FROM settings")
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			rows.Close()
			return nil, err
		}
		values[k] = v
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, f := range data.fields() {
		*f = values[settingKeys[i]]
	}
	if data.Client == "" {
		host, err := os.Hostname()
		if err != nil {
			return nil, fmt.Errorf("client name: %w", err)
		}
		data.Client = host
	}

	rows, err = d.sql.Query("SELECT name, dir FROM roots")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var name, dir string
		if err := rows.Scan(&name, &dir); err != nil {
			rows.Close()
			return nil, err
		}
		data.Roots[name] = dir
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// In the order they were added.
	rows, err = d.sql.Query("SELECT root, path, ignore FROM folders ORDER BY rowid")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var f Folder
		var ignore string
		if err := rows.Scan(&f.Root, &f.Path, &ignore); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(ignore), &f.Ignore); err != nil {
			return nil, fmt.Errorf("%s: ignore rules: %w", f, err)
		}
		data.Folders = append(data.Folders, f)
	}
	return data, rows.Err()
}

// Empty reports whether nothing was ever saved: no setup and no folders.
func (d *DB) Empty() (bool, error) {
	var n int
	err := d.sql.QueryRow("SELECT (SELECT count(*) FROM settings) + (SELECT count(*) FROM folders)").Scan(&n)
	return n == 0, err
}

// State is what the client remembers about one of its folders.
type State struct {
	Base        string // the snapshot the folder is based on
	Master      string // the master it was last in step with: the one it last pushed or pulled
	Fingerprint string // at its last snapshot or pull
	AfterPull   bool   // pulled, and not snapshotted since
}

// State returns what is remembered about the folder; all of it is empty if
// nothing is.
func (d *DB) State(root, path string) (State, error) {
	var s State
	err := d.sql.QueryRow("SELECT base, master, fingerprint, after_pull FROM state WHERE root = ? AND path = ?", root, path).
		Scan(&s.Base, &s.Master, &s.Fingerprint, &s.AfterPull)
	if errors.Is(err, sql.ErrNoRows) {
		return State{}, nil
	}
	return s, err
}

// Base returns the snapshot the folder is based on, or "" if there is none.
func (d *DB) Base(root, path string) (string, error) {
	s, err := d.State(root, path)
	return s.Base, err
}

// Master returns the master snapshot the folder was last in step with: the
// one it last pushed or pulled. It is "" if it has done neither.
func (d *DB) Master(root, path string) (string, error) {
	s, err := d.State(root, path)
	return s.Master, err
}

// Fingerprint returns the folder's fingerprint at its last snapshot or
// pull, or "" if there is none.
func (d *DB) Fingerprint(root, path string) (string, error) {
	s, err := d.State(root, path)
	return s.Fingerprint, err
}

// AfterPull reports whether the folder was pulled and not snapshotted since.
func (d *DB) AfterPull(root, path string) (bool, error) {
	s, err := d.State(root, path)
	return s.AfterPull, err
}

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// Writer writes to the database, on its own or inside Update.
type Writer struct {
	q execer
}

// SetSettings replaces the settings.
func (w Writer) SetSettings(s Settings) error {
	for i, f := range s.fields() {
		if _, err := w.q.Exec("INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value",
			settingKeys[i], *f); err != nil {
			return err
		}
	}
	return nil
}

// AddFolder adds the folder, and its root at rootDir. A known root is moved
// to rootDir.
func (w Writer) AddFolder(f Folder, rootDir string) error {
	ignore, err := ignoreJSON(f.Ignore)
	if err != nil {
		return err
	}
	if _, err := w.q.Exec("INSERT INTO roots (name, dir) VALUES (?, ?) ON CONFLICT (name) DO UPDATE SET dir = excluded.dir",
		f.Root, rootDir); err != nil {
		return err
	}
	_, err = w.q.Exec("INSERT INTO folders (root, path, ignore) VALUES (?, ?, ?)", f.Root, f.Path, ignore)
	return err
}

// SetIgnore replaces the folder's ignore rules.
func (w Writer) SetIgnore(f Folder, ignore []string) error {
	s, err := ignoreJSON(ignore)
	if err != nil {
		return err
	}
	_, err = w.q.Exec("UPDATE folders SET ignore = ? WHERE root = ? AND path = ?", s, f.Root, f.Path)
	return err
}

func ignoreJSON(ignore []string) (string, error) {
	if ignore == nil {
		ignore = []string{}
	}
	b, err := json.Marshal(ignore)
	return string(b), err
}

func (w Writer) setState(root, path, column string, value any) error {
	_, err := w.q.Exec("INSERT INTO state (root, path, "+column+") VALUES (?, ?, ?) "+
		"ON CONFLICT (root, path) DO UPDATE SET "+column+" = excluded."+column, root, path, value)
	return err
}

// SetState replaces all that is remembered about the folder.
func (w Writer) SetState(root, path string, s State) error {
	_, err := w.q.Exec("INSERT INTO state (root, path, base, master, fingerprint, after_pull) VALUES (?, ?, ?, ?, ?, ?) "+
		"ON CONFLICT (root, path) DO UPDATE SET base = excluded.base, master = excluded.master, "+
		"fingerprint = excluded.fingerprint, after_pull = excluded.after_pull",
		root, path, s.Base, s.Master, s.Fingerprint, s.AfterPull)
	return err
}

// SetBase records the snapshot the folder is based on.
func (w Writer) SetBase(root, path, id string) error { return w.setState(root, path, "base", id) }

// SetMaster records the master snapshot the folder is in step with.
func (w Writer) SetMaster(root, path, id string) error { return w.setState(root, path, "master", id) }

// SetFingerprint records the folder's fingerprint.
func (w Writer) SetFingerprint(root, path, print string) error {
	return w.setState(root, path, "fingerprint", print)
}

// SetAfterPull records or clears that the folder was pulled and not
// snapshotted since.
func (w Writer) SetAfterPull(root, path string, pulled bool) error {
	return w.setState(root, path, "after_pull", pulled)
}
