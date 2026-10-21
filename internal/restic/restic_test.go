package restic

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseVersion(t *testing.T) {
	cases := []struct {
		out  string
		want Version
		ok   bool
	}{
		{"restic 0.19.1 compiled with go1.26.4 on linux/amd64\n", Version{0, 19, 1}, true},
		{"restic 0.17.0-dev (compiled manually) compiled with go1.22", Version{0, 17, 0}, true},
		{"restic 1.2.3", Version{1, 2, 3}, true},
		{"rustic 0.9.0", Version{}, false},
		{"restic unknown", Version{}, false},
		{"", Version{}, false},
	}
	for _, tc := range cases {
		got, err := ParseVersion(tc.out)
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("ParseVersion(%q) = %v, %v; want %v, ok=%v", tc.out, got, err, tc.want, tc.ok)
		}
	}
}

func TestVersionLess(t *testing.T) {
	cases := []struct {
		a, b Version
		want bool
	}{
		{Version{0, 16, 5}, Version{0, 17, 0}, true},
		{Version{0, 17, 0}, Version{0, 17, 0}, false},
		{Version{0, 19, 1}, Version{0, 17, 0}, false},
		{Version{1, 0, 0}, Version{0, 99, 99}, false},
		{Version{0, 17, 0}, Version{0, 17, 1}, true},
	}
	for _, tc := range cases {
		if got := tc.a.Less(tc.b); got != tc.want {
			t.Errorf("%v.Less(%v) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

// newRepo creates an empty repository, or skips the test without restic.
func newRepo(t *testing.T) *Client {
	t.Helper()
	if _, err := exec.LookPath(binary); err != nil {
		t.Skip("restic is not installed")
	}
	base := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(base, "cache"))
	pass := filepath.Join(base, "pass")
	if err := os.WriteFile(pass, []byte("test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := &Client{Repository: filepath.Join(base, "repo"), PasswordFile: pass}
	if err := c.Ping(context.Background()); !errors.Is(err, ErrNoRepository) {
		t.Fatalf("before init: err = %v, want ErrNoRepository", err)
	}
	if err := c.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	return c
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCheckVersion(t *testing.T) {
	newRepo(t)
	if err := CheckVersion(context.Background()); err != nil {
		t.Error(err)
	}
}

func TestBackup(t *testing.T) {
	c := newRepo(t)
	ctx := context.Background()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.txt"), "one")
	write(t, filepath.Join(dir, ".venv", "big"), "ignored")
	opts := BackupOptions{
		Dir:      dir,
		Host:     "client-a",
		Tags:     []string{"root=Projects", "path=Freelance/app"},
		Excludes: []string{".venv"},
	}

	first, err := c.Backup(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if first.SnapshotID == "" || first.FilesNew != 1 {
		t.Errorf("first = %+v, want a snapshot with 1 new file", first)
	}

	opts.Parent = first.SnapshotID
	same, err := c.Backup(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if same.SnapshotID != "" {
		t.Errorf("made snapshot %s although nothing changed", same.SnapshotID)
	}

	write(t, filepath.Join(dir, "a.txt"), "two!")
	second, err := c.Backup(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if second.SnapshotID == "" || second.FilesChanged != 1 || second.BytesStored == 0 {
		t.Errorf("second = %+v, want a snapshot with 1 changed file", second)
	}

	// The stored snapshot has the parent, host and tags it was given, and
	// holds the folder's contents at its top level.
	out, err := c.run(ctx, "", "snapshots", "--json", second.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"parent":"` + first.SnapshotID + `"`,
		`"hostname":"client-a"`,
		`"root=Projects"`,
		`"path=Freelance/app"`,
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("snapshot lacks %s:\n%s", want, out)
		}
	}
	ls, err := c.run(ctx, "", "ls", second.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ls), "\n/a.txt\n") || strings.Contains(string(ls), ".venv") {
		t.Errorf("unexpected snapshot contents:\n%s", ls)
	}
}

func TestBackupErrors(t *testing.T) {
	c := newRepo(t)
	ctx := context.Background()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.txt"), "one")

	_, err := c.Backup(ctx, BackupOptions{Dir: dir, Host: "h", Parent: "deadbeef"})
	if err == nil || !strings.Contains(err.Error(), "deadbeef") {
		t.Errorf("missing parent: err = %v", err)
	}

	write(t, c.PasswordFile, "wrong\n")
	if err := c.Ping(ctx); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("wrong password: err = %v, want ErrWrongPassword", err)
	}
}

func TestRestore(t *testing.T) {
	c := newRepo(t)
	ctx := context.Background()
	src := t.TempDir()
	write(t, filepath.Join(src, "a.txt"), "one")
	write(t, filepath.Join(src, "sub", "b.txt"), "two")
	write(t, filepath.Join(src, ".venv", "big"), "not synced")
	snap, err := c.Backup(ctx, BackupOptions{Dir: src, Host: "client-a", Excludes: []string{".venv"}})
	if err != nil {
		t.Fatal(err)
	}

	// Into a fresh directory, which keeps the mode it was created with.
	dst := filepath.Join(t.TempDir(), "copy")
	if err := os.Mkdir(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := c.Restore(ctx, RestoreOptions{ID: snap.SnapshotID, Dir: dst, Excludes: []string{".venv"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Restored == 0 || res.Deleted != 0 {
		t.Errorf("result = %+v", res)
	}
	if data, _ := os.ReadFile(filepath.Join(dst, "sub", "b.txt")); string(data) != "two" {
		t.Errorf("sub/b.txt = %q", data)
	}
	if info, _ := os.Stat(dst); info.Mode().Perm() != 0o755 {
		t.Errorf("folder mode = %o, want 755", info.Mode().Perm())
	}

	// Over a folder that drifted: extras go, changes revert, and an
	// excluded directory is left alone.
	write(t, filepath.Join(dst, "extra.txt"), "x")
	write(t, filepath.Join(dst, "a.txt"), "changed")
	write(t, filepath.Join(dst, ".venv", "local"), "keep me")
	if err := os.Remove(filepath.Join(dst, "sub", "b.txt")); err != nil {
		t.Fatal(err)
	}
	res, err = c.Restore(ctx, RestoreOptions{ID: snap.SnapshotID, Dir: dst, Excludes: []string{".venv"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Deleted != 1 {
		t.Errorf("deleted %d, want 1", res.Deleted)
	}
	if _, err := os.Stat(filepath.Join(dst, "extra.txt")); err == nil {
		t.Error("extra.txt survived")
	}
	if data, _ := os.ReadFile(filepath.Join(dst, "a.txt")); string(data) != "one" {
		t.Errorf("a.txt = %q", data)
	}
	if data, _ := os.ReadFile(filepath.Join(dst, "sub", "b.txt")); string(data) != "two" {
		t.Errorf("sub/b.txt = %q", data)
	}
	if data, _ := os.ReadFile(filepath.Join(dst, ".venv", "local")); string(data) != "keep me" {
		t.Errorf("the excluded directory was touched: %q", data)
	}

	// The snapshot carries the rules it was taken with.
	found, err := c.Find(ctx, snap.SnapshotID)
	if err != nil || len(found.Excludes) != 1 || found.Excludes[0] != ".venv" {
		t.Errorf("found = %+v, %v", found, err)
	}
}

func TestForget(t *testing.T) {
	c := newRepo(t)
	ctx := context.Background()
	src := t.TempDir()
	write(t, filepath.Join(src, "a.txt"), "one")
	first, err := c.Backup(ctx, BackupOptions{Dir: src, Host: "h"})
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(src, "a.txt"), "two")
	second, err := c.Backup(ctx, BackupOptions{Dir: src, Host: "h", Parent: first.SnapshotID})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Forget(ctx, second.SnapshotID); err != nil {
		t.Fatal(err)
	}
	list, err := c.Snapshots(ctx, nil)
	if err != nil || len(list) != 1 || list[0].ID != first.SnapshotID {
		t.Errorf("after forgetting the second: %+v, %v", list, err)
	}
}

// A restic that was killed leaves its lock in the repository; Forget is not
// refused for it.
func TestForgetStaleLock(t *testing.T) {
	c := newRepo(t)
	ctx := context.Background()
	src := t.TempDir()
	write(t, filepath.Join(src, "a.txt"), "one")
	b, err := c.Backup(ctx, BackupOptions{Dir: src, Host: "h"})
	if err != nil {
		t.Fatal(err)
	}
	// A backup from a stdin that stays open holds its lock until killed.
	cmd := exec.Command(binary, "backup", "--stdin", "--host", "h")
	cmd.Env = append(os.Environ(), "RESTIC_REPOSITORY="+c.Repository, "RESTIC_PASSWORD_FILE="+c.PasswordFile)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	locks := filepath.Join(c.Repository, "locks")
	for deadline := time.Now().Add(30 * time.Second); ; {
		if entries, _ := os.ReadDir(locks); len(entries) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("restic backup took no lock")
		}
		time.Sleep(50 * time.Millisecond)
	}
	cmd.Process.Kill()
	cmd.Wait()
	if err := c.Forget(ctx, b.SnapshotID); err != nil {
		t.Fatal(err)
	}
}

// Reads take no lock, so they work where none can be made.
func TestReadsTakeNoLock(t *testing.T) {
	c := newRepo(t)
	ctx := context.Background()
	src := t.TempDir()
	write(t, filepath.Join(src, "a.txt"), "one")
	b, err := c.Backup(ctx, BackupOptions{Dir: src, Host: "h"})
	if err != nil {
		t.Fatal(err)
	}
	locks := filepath.Join(c.Repository, "locks")
	if err := os.Chmod(locks, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locks, 0o700) })
	if list, err := c.Snapshots(ctx, nil); err != nil || len(list) != 1 {
		t.Errorf("Snapshots: %+v, %v", list, err)
	}
	if _, err := c.Find(ctx, b.SnapshotID); err != nil {
		t.Errorf("Find: %v", err)
	}
}

// A client kept away from the terminal runs restic in a session of its own,
// and works as before.
func TestNoTerminal(t *testing.T) {
	c := newRepo(t)
	c.NoTerminal = true
	ctx := context.Background()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.txt"), "one")
	res, err := c.Backup(ctx, BackupOptions{Dir: dir, Host: "client-a"})
	if err != nil || res.SnapshotID == "" {
		t.Fatalf("backup: %+v, %v", res, err)
	}
	// The session of the restic it starts is not this process's.
	out, err := exec.Command("sh", "-c", "ps -o sid= -p $$").Output()
	if err != nil {
		t.Skip("cannot read session IDs with ps")
	}
	own := strings.TrimSpace(string(out))
	c.PasswordCommand, c.PasswordFile = "sh -c 'ps -o sid= -p $$ >"+filepath.Join(dir, "sid")+"; echo test'", ""
	if err := c.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	theirs, err := os.ReadFile(filepath.Join(dir, "sid"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(theirs)); got == own || got == "" {
		t.Errorf("restic ran in session %q, the same as this process (%q)", got, own)
	}
}
