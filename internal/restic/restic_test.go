package restic

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
