package master

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func id(c string) string { return strings.Repeat(c, 64) }

// newRepo returns a directory standing in for a repository.
func newRepo(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.Mkdir(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	return repo
}

func open(t *testing.T, repository string) Pointers {
	t.Helper()
	p, err := Open(repository)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMove(t *testing.T) {
	repo := newRepo(t)
	p := open(t, repo+"/")
	ctx := context.Background()
	const root, path = "Projects", "Freelance/app"

	if err := p.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if got, err := p.Master(ctx, root, path); err != nil || got != "" {
		t.Errorf("unset master = %q, %v", got, err)
	}
	// Reading leaves nothing behind.
	if _, err := os.Stat(repo + ".wipsync"); err == nil {
		t.Error("reading master created the pointer directory")
	}
	// First push: nothing expected, nothing there.
	if err := p.Move(ctx, root, path, "", id("a")); err != nil {
		t.Fatal(err)
	}
	// A stale expectation is refused and changes nothing.
	var moved *MovedError
	if err := p.Move(ctx, root, path, "", id("b")); !errors.As(err, &moved) || moved.Current != id("a") {
		t.Errorf("stale move: err = %v", err)
	}
	if err := p.Move(ctx, root, path, id("c"), id("b")); !errors.As(err, &moved) || moved.Current != id("a") {
		t.Errorf("stale move: err = %v", err)
	}
	if got, _ := p.Master(ctx, root, path); got != id("a") {
		t.Errorf("master = %q after refused moves", got)
	}
	// The right expectation moves it; moving to where it already is works too.
	if err := p.Move(ctx, root, path, id("a"), id("b")); err != nil {
		t.Fatal(err)
	}
	if err := p.Move(ctx, root, path, id("b"), id("b")); err != nil {
		t.Errorf("no-op move: %v", err)
	}
	if got, _ := p.Master(ctx, root, path); got != id("b") {
		t.Errorf("master = %q, want b", got)
	}
	// Another folder is separate.
	if got, _ := p.Master(ctx, root, "Freelance/other"); got != "" {
		t.Errorf("other folder has master %q", got)
	}
	// Expecting a master where there is none.
	if err := p.Move(ctx, root, "Freelance/other", id("a"), id("b")); !errors.As(err, &moved) || moved.Current != "" {
		t.Errorf("err = %v", err)
	}

	// The pointer is a private file beside the repository.
	file := filepath.Join(repo+".wipsync", "folders", root, path, "master")
	data, err := os.ReadFile(file)
	if err != nil || string(data) != id("b")+"\n" {
		t.Errorf("pointer file = %q, %v", data, err)
	}
	if info, _ := os.Stat(file); info.Mode().Perm() != 0o600 {
		t.Errorf("pointer file mode = %o, want 600", info.Mode().Perm())
	}
}

func TestAll(t *testing.T) {
	repo := newRepo(t)
	p := open(t, repo)
	ctx := context.Background()
	if all, err := p.All(ctx); err != nil || len(all) != 0 {
		t.Errorf("empty store: %v, %v", all, err)
	}
	want := map[string]string{
		"Projects/Freelance/app": id("a"),
		"Projects/master":        id("b"), // a folder named like the pointer file
		"My Docs/a b/c":          id("c"),
	}
	for folder, master := range want {
		root, path, _ := strings.Cut(folder, "/")
		if err := p.Move(ctx, root, path, "", master); err != nil {
			t.Fatal(err)
		}
	}
	all, err := p.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != len(want) {
		t.Errorf("all = %v", all)
	}
	for folder, master := range want {
		if all[folder] != master {
			t.Errorf("%s: master = %q, want %q", folder, all[folder], master)
		}
	}
	if _, err := open(t, repo+"-missing").All(ctx); err == nil {
		t.Error("All succeeded without a repository")
	}
}

func TestRejectsBadInput(t *testing.T) {
	repo := newRepo(t)
	p := open(t, repo)
	ctx := context.Background()
	bad := []struct{ root, path, expect, id string }{
		{"..", "app", "", id("a")},
		{"a/b", "app", "", id("a")},
		{"", "app", "", id("a")},
		{"Projects", "../escape", "", id("a")},
		{"Projects", "/abs", "", id("a")},
		{"Projects", "a/../../b", "", id("a")},
		{"Projects", "a//b", "", id("a")},
		{"Projects", "", "", id("a")},
		{"Projects", "app", "", "abc"},
		{"Projects", "app", "", strings.Repeat("A", 64)},
		{"Projects", "app", "nope", id("a")},
		{"Projects", "app", "-", id("a")},
	}
	for _, c := range bad {
		if err := p.Move(ctx, c.root, c.path, c.expect, c.id); err == nil {
			t.Errorf("Move(%q, %q, %q, %q) was accepted", c.root, c.path, c.expect, c.id)
		}
	}
	if _, err := p.Master(ctx, "Projects", "../../etc"); err == nil {
		t.Error("Master accepted a path outside the store")
	}
	if entries, _ := os.ReadDir(filepath.Dir(repo)); len(entries) != 1 {
		t.Errorf("bad input left files behind: %v", entries)
	}
}

func TestNoRepository(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "repo")
	p := open(t, missing)
	ctx := context.Background()
	for name, err := range map[string]error{
		"Check": p.Check(ctx),
		"Move":  p.Move(ctx, "Projects", "app", "", id("a")),
	} {
		if err == nil || !strings.Contains(err.Error(), "there is no repository at "+missing+" on this machine") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := p.Master(ctx, "Projects", "app"); err == nil {
		t.Error("Master succeeded without a repository")
	}
	if entries, _ := os.ReadDir(filepath.Dir(missing)); len(entries) != 0 {
		t.Errorf("left files behind: %v", entries)
	}
}

// Many clients push from the same base at once: exactly one may win. Each
// push is its own sh process, as it is for real clients.
func TestMoveRace(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	const root, path = "Projects", "app"
	for round := 0; round < 20; round++ {
		base, err := open(t, repo).Master(ctx, root, path)
		if err != nil {
			t.Fatal(err)
		}
		const pushers = 16
		var wg sync.WaitGroup
		results := make([]error, pushers)
		for i := 0; i < pushers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				// An ID no other pusher or round uses.
				results[i] = open(t, repo).Move(ctx, root, path, base, fmt.Sprintf("%064x", round*pushers+i+1))
			}()
		}
		wg.Wait()
		won := 0
		for _, err := range results {
			var moved *MovedError
			switch {
			case err == nil:
				won++
			case !errors.As(err, &moved):
				t.Fatalf("round %d: %v", round, err)
			}
		}
		if won != 1 {
			t.Fatalf("round %d: %d pushers won, want 1", round, won)
		}
		got, err := open(t, repo).Master(ctx, root, path)
		if err != nil || got == base {
			t.Fatalf("round %d: master = %q, %v", round, got, err)
		}
	}
}

func TestParseSFTP(t *testing.T) {
	cases := map[string]target{
		"ahmet@s2.ahmt.me:/opt/p2/repo":        {host: "ahmet@s2.ahmt.me", path: "/opt/p2/repo"},
		"server:backups/repo":                  {host: "server", path: "backups/repo"},
		"user@[::1]:/srv/repo":                 {host: "user@::1", path: "/srv/repo"},
		"//ahmet@s2.ahmt.me:2222//opt/p2/repo": {host: "ahmet@s2.ahmt.me", port: "2222", path: "/opt/p2/repo"},
		"//server/backups/repo":                {host: "server", path: "backups/repo"},
		"//user@[::1]:2222//srv/repo":          {host: "user@::1", port: "2222", path: "/srv/repo"},
	}
	for in, want := range cases {
		if got, err := parseSFTP(in); err != nil || got != want {
			t.Errorf("parseSFTP(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "server", "server:", ":/path", "user@:/path", "//server", "//server/"} {
		if got, err := parseSFTP(in); err == nil {
			t.Errorf("parseSFTP(%q) = %+v, want an error", in, got)
		}
	}
}

func TestOpen(t *testing.T) {
	for _, repo := range []string{"sftp:ahmet@host:/repo", "/srv/repo", "local:/srv/repo", "relative/repo", "/srv/odd:name"} {
		if _, err := Open(repo); err != nil {
			t.Errorf("Open(%q): %v", repo, err)
		}
	}
	for _, repo := range []string{"rest:https://host/repo", "s3:s3.amazonaws.com/bucket", "sftp:host"} {
		if _, err := Open(repo); err == nil {
			t.Errorf("Open(%q) was accepted", repo)
		}
	}
}

// fakeSSH replaces ssh with a local shell. The command line still goes
// through a real shell, the way ssh hands it to the one on the server.
func fakeSSH(t *testing.T, shell string) {
	t.Helper()
	old := sshCommand
	t.Cleanup(func() { sshCommand = old })
	sshCommand = func(ctx context.Context, _ target, commandLine string) *exec.Cmd {
		return exec.CommandContext(ctx, shell, "-c", commandLine)
	}
}

func TestOverSSH(t *testing.T) {
	// Whatever the user's shell on the server is, it must pass the script
	// and its arguments on unchanged.
	for _, shell := range []string{"sh", "bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			if _, err := exec.LookPath(shell); err != nil {
				t.Skipf("%s is not installed", shell)
			}
			fakeSSH(t, shell)
			ctx := context.Background()
			// Names a shell would mangle if they were not quoted.
			repo := filepath.Join(t.TempDir(), "my repo's $HOME")
			if err := os.Mkdir(repo, 0o700); err != nil {
				t.Fatal(err)
			}
			p := open(t, "sftp:user@host:"+repo)
			const root, path = "My Projects", `it's; rm -rf * "x" \n`

			if err := p.Check(ctx); err != nil {
				t.Fatal(err)
			}
			if got, err := p.Master(ctx, root, path); err != nil || got != "" {
				t.Errorf("unset master = %q, %v", got, err)
			}
			if err := p.Move(ctx, root, path, "", id("a")); err != nil {
				t.Fatal(err)
			}
			var moved *MovedError
			if err := p.Move(ctx, root, path, "", id("b")); !errors.As(err, &moved) || moved.Current != id("a") {
				t.Errorf("stale move: err = %v", err)
			}
			if err := p.Move(ctx, root, path, id("a"), id("b")); err != nil {
				t.Fatal(err)
			}
			if got, err := p.Master(ctx, root, path); err != nil || got != id("b") {
				t.Errorf("master = %q, %v", got, err)
			}
			if all, err := p.All(ctx); err != nil || len(all) != 1 || all[root+"/"+path] != id("b") {
				t.Errorf("all = %v, %v", all, err)
			}
			// The same pointer, read without ssh.
			if got, _ := open(t, repo).Master(ctx, root, path); got != id("b") {
				t.Errorf("read locally: %q", got)
			}

			err := open(t, "sftp:user@host:"+repo+"-missing").Check(ctx)
			if err == nil || !strings.Contains(err.Error(), "there is no repository at "+repo+"-missing on user@host") {
				t.Errorf("missing repository: err = %v", err)
			}
		})
	}
}

func TestSSHFailure(t *testing.T) {
	old := sshCommand
	t.Cleanup(func() { sshCommand = old })
	sshCommand = func(ctx context.Context, _ target, _ string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "echo 'ssh: connect to host: Connection refused' >&2; exit 255")
	}
	err := open(t, "sftp:user@host:/repo").Check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "user@host: ssh: connect to host: Connection refused") {
		t.Errorf("err = %v", err)
	}
}
