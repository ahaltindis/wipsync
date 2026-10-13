package fingerprint

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func of(t *testing.T, dir string, ignore ...string) string {
	t.Helper()
	fp, err := Of(context.Background(), dir, ignore)
	if err != nil {
		t.Fatal(err)
	}
	return fp
}

func TestChanges(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.txt"), "one")
	write(t, filepath.Join(dir, "sub", "b.txt"), "two")
	if err := os.Symlink("a.txt", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	last := of(t, dir)
	if again := of(t, dir); again != last {
		t.Fatal("the fingerprint differs between two looks at the same folder")
	}

	// Each of these must change the fingerprint.
	changes := []struct {
		name string
		do   func() error
	}{
		{"edit with a new size", func() error { return os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one!"), 0o644) }},
		{"edit with the same size", func() error { return os.WriteFile(filepath.Join(dir, "a.txt"), []byte("ONE!"), 0o644) }},
		{"new file", func() error { return os.WriteFile(filepath.Join(dir, "sub", "c.txt"), nil, 0o644) }},
		{"deleted file", func() error { return os.Remove(filepath.Join(dir, "sub", "c.txt")) }},
		{"renamed file", func() error { return os.Rename(filepath.Join(dir, "sub", "b.txt"), filepath.Join(dir, "sub", "d.txt")) }},
		{"file mode", func() error { return os.Chmod(filepath.Join(dir, "a.txt"), 0o755) }},
		{"touched file", func() error {
			old := time.Now().Add(-time.Hour)
			return os.Chtimes(filepath.Join(dir, "a.txt"), old, old)
		}},
		{"symlink target", func() error {
			os.Remove(filepath.Join(dir, "link"))
			return os.Symlink("sub", filepath.Join(dir, "link"))
		}},
		{"new empty directory", func() error { return os.Mkdir(filepath.Join(dir, "empty"), 0o755) }},
		{"directory mode", func() error { return os.Chmod(filepath.Join(dir, "empty"), 0o700) }},
		{"deleted directory", func() error { return os.Remove(filepath.Join(dir, "empty")) }},
	}
	for _, c := range changes {
		if err := c.do(); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		now := of(t, dir)
		if now == last {
			t.Errorf("%s: the fingerprint did not change", c.name)
		}
		last = now
	}
}

// A file created and removed again leaves only a new directory timestamp.
func TestIgnoresDirectoryTimestamps(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "sub", "a.txt"), "one")
	before := of(t, dir)

	lock := filepath.Join(dir, "sub", "index.lock")
	write(t, lock, "")
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "sub"), old, old); err != nil {
		t.Fatal(err)
	}
	if of(t, dir) != before {
		t.Error("a directory's timestamp changed the fingerprint")
	}
}

func TestIgnoreRules(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.txt"), "one")
	write(t, filepath.Join(dir, ".venv", "lib.py"), "x")
	write(t, filepath.Join(dir, "deep", "node_modules", "m.js"), "x")
	write(t, filepath.Join(dir, "build-out", "o.bin"), "x")
	rules := []string{".venv", "node_modules", "build-*", "docs/generated"}
	before := of(t, dir, rules...)

	// Under a plain-name rule, at any depth: not looked at.
	write(t, filepath.Join(dir, ".venv", "lib.py"), "changed")
	write(t, filepath.Join(dir, "deep", "node_modules", "new.js"), "x")
	if of(t, dir, rules...) != before {
		t.Error("a change under an ignored directory changed the fingerprint")
	}
	// A file with an ignored name is ignored as well, as restic does.
	write(t, filepath.Join(dir, "deep", ".venv"), "a file")
	if of(t, dir, rules...) != before {
		t.Error("a file with an ignored name changed the fingerprint")
	}
	// A rule with a wildcard is not applied here, so the change counts.
	write(t, filepath.Join(dir, "build-out", "o.bin"), "changed")
	if of(t, dir, rules...) == before {
		t.Error("a change under a wildcard rule was not seen")
	}
	// Without the rules everything counts.
	if of(t, dir) == of(t, dir, rules...) {
		t.Error("the rules made no difference")
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestGitIndex(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	repo := filepath.Join(dir, "nested", "repo") // not at the folder's top
	write(t, filepath.Join(repo, "a.txt"), "one\n")
	write(t, filepath.Join(repo, "b.txt"), "two\n")
	git(t, repo, "init", "-q")
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-q", "-m", "first")
	index := filepath.Join(repo, ".git", "index")

	// An index holding the same entries without their stat cache, which is
	// what a client has right after a pull.
	git(t, repo, "read-tree", "HEAD")
	stale, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	before := of(t, dir)
	gitDir, _ := os.Stat(filepath.Join(repo, ".git"))

	// Taking the fingerprint must not touch the repository.
	if now, _ := os.Stat(filepath.Join(repo, ".git")); !now.ModTime().Equal(gitDir.ModTime()) {
		t.Error("taking the fingerprint changed .git")
	}

	// git status refreshes the cache: the file changes, the fingerprint does not.
	git(t, repo, "status")
	refreshed, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	if string(refreshed) == string(stale) {
		t.Fatal("git status did not rewrite the index; the test proves nothing")
	}
	if of(t, dir) != before {
		t.Error("a refresh of the index cache changed the fingerprint")
	}
	git(t, repo, "status")
	if of(t, dir) != before {
		t.Error("a second git status changed the fingerprint")
	}

	// Changes to what is staged do count, also when no other file changes.
	last := before
	for _, args := range [][]string{
		{"rm", "-q", "--cached", "b.txt"},
		{"add", "b.txt"}, // its object exists already, so only the index changes
		{"update-index", "--assume-unchanged", "a.txt"},
		{"update-index", "--no-assume-unchanged", "a.txt"},
	} {
		git(t, repo, args...)
		now := of(t, dir)
		if now == last {
			t.Errorf("git %v: the fingerprint did not change", args)
		}
		last = now
	}
	write(t, filepath.Join(repo, "c.txt"), "three\n")
	afterNewFile := of(t, dir)
	git(t, repo, "add", "-N", "c.txt")
	if of(t, dir) == afterNewFile {
		t.Error("git add -N: the fingerprint did not change")
	}
}

// Without a usable repository the index is a file like any other.
func TestIndexThatIsNotGit(t *testing.T) {
	dir := t.TempDir()
	index := filepath.Join(dir, ".git", "index")
	write(t, index, "not an index")
	before := of(t, dir)
	write(t, index, "still not one")
	if of(t, dir) == before {
		t.Error("a changed file named .git/index was not seen")
	}
}
