package watch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ahaltindis/wipsync/internal/app"
)

// serve runs Serve at a new socket, answering each request with reply.
func serve(t *testing.T, reply func(Action) Reply) (string, chan<- Request) {
	t.Helper()
	// Short: a socket's path is limited to about 100 bytes.
	dir, err := os.MkdirTemp("", "ws")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "s", "watch.sock")
	ctx, cancel := context.WithCancel(context.Background())
	asks := make(chan Request, 1)
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, path, asks) }()
	go func() {
		for r := range asks {
			r.Reply <- reply(r.Action)
		}
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})
	waitFor(t, path)
	return path, asks
}

func waitFor(t *testing.T, path string) {
	t.Helper()
	for range 200 {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s did not appear", path)
}

func TestAsk(t *testing.T) {
	path, _ := serve(t, func(a Action) Reply {
		if a == PushAll {
			return Reply{Result: app.AllResult{Verb: "push", Done: 1, Lines: []string{"Projects/app: master is now 55555555"}}}
		}
		return Reply{Err: errors.New("cannot read the masters: connection refused")}
	})
	res, err := Ask(context.Background(), path, PushAll)
	if err != nil || res.Summary() != "pushed 1 folder" || len(res.Lines) != 1 {
		t.Errorf("push all: %+v, %v", res, err)
	}
	if _, err := Ask(context.Background(), path, PullAll); err == nil || err.Error() != "cannot read the masters: connection refused" {
		t.Errorf("pull all: err = %v", err)
	}
}

func TestAskWithoutService(t *testing.T) {
	path := filepath.Join(t.TempDir(), "none.sock")
	if _, err := Ask(context.Background(), path, PullAll); !errors.Is(err, ErrNoService) {
		t.Errorf("err = %v", err)
	}
	// A file left by a service that is gone is no service either.
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Ask(context.Background(), path, PullAll); !errors.Is(err, ErrNoService) {
		t.Errorf("with a stale file: err = %v", err)
	}
}

// One service answers at a time; a second is told so, and the first goes on.
func TestServeOnce(t *testing.T) {
	path, _ := serve(t, func(Action) Reply { return Reply{Result: app.AllResult{Verb: "pull"}} })
	if err := Serve(context.Background(), path, nil); err == nil || !strings.Contains(err.Error(), "another wipsync watch answers") {
		t.Errorf("second Serve: %v", err)
	}
	if res, err := Ask(context.Background(), path, PullAll); err != nil || res.Summary() != "nothing to pull" {
		t.Errorf("after the second: %+v, %v", res, err)
	}
}

// A stale socket file is replaced.
func TestServeReplacesStale(t *testing.T) {
	dir, err := os.MkdirTemp("", "ws")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "watch.sock")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	asks := make(chan Request)
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, path, asks) }()
	go func() {
		r := <-asks
		r.Reply <- Reply{Result: app.AllResult{Verb: "push"}}
	}()
	for range 200 {
		if info, err := os.Stat(path); err == nil && info.Mode()&os.ModeSocket != 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if res, err := Ask(context.Background(), path, PushAll); err != nil || res.Summary() != "nothing to push" {
		t.Errorf("%+v, %v", res, err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Error(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the socket stayed after the service stopped: %v", err)
	}
}

func TestSocketPath(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	a, _ := SocketPath("/home/u/.local/share/wipsync/wipsync.db")
	b, _ := SocketPath("/tmp/other/wipsync.db")
	if a == b || !strings.HasPrefix(a, "/run/user/1000/wipsync/watch-") || !strings.HasSuffix(a, ".sock") {
		t.Errorf("paths %q and %q", a, b)
	}
	t.Setenv("XDG_RUNTIME_DIR", "")
	if _, ok := SocketPath("/x/wipsync.db"); ok {
		t.Error("a socket path without XDG_RUNTIME_DIR")
	}
}
