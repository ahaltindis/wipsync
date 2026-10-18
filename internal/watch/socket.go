package watch

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"

	"github.com/ahaltindis/wipsync/internal/app"
)

// The service answers on a Unix socket, so that pulls and pushes of many
// folders from the CLI and the TUI run in it, as the tray's menu does: the
// icon shows them, and the desktop is told how they went. A request is one
// JSON line, and so is the answer, sent once the action is done.

type socketRequest struct {
	Action string    `json:"action"`
	Scope  app.Scope `json:"scope"`
}

type socketReply struct {
	Result app.AllResult `json:"result"`
	Error  string        `json:"error,omitempty"`
}

// SocketPath returns where the service that uses the database at db
// answers: $XDG_RUNTIME_DIR/wipsync/watch-<hash of db>.sock. A client with
// another database never reaches it. It is false without XDG_RUNTIME_DIR.
func SocketPath(db string) (string, bool) {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		return "", false
	}
	sum := sha256.Sum256([]byte(db))
	return filepath.Join(dir, "wipsync", fmt.Sprintf("watch-%x.sock", sum[:6])), true
}

// Serve answers requests on the socket at path until ctx ends, handing
// each to asks and waiting for its reply.
func Serve(ctx context.Context, path string, asks chan<- Request) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if c, err := net.Dial("unix", path); err == nil {
		c.Close()
		return fmt.Errorf("another wipsync watch answers on %s", path)
	}
	// Left by a service that did not stop cleanly.
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		l.Close() // removes the file too
	}()
	for {
		c, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go answer(ctx, c, asks)
	}
}

func answer(ctx context.Context, c net.Conn, asks chan<- Request) {
	defer c.Close()
	var req socketRequest
	if err := json.NewDecoder(c).Decode(&req); err != nil {
		return
	}
	send := func(r socketReply) { json.NewEncoder(c).Encode(r) }
	a, ok := ParseAction(req.Action)
	if !ok {
		send(socketReply{Error: fmt.Sprintf("the service does not know %q", req.Action)})
		return
	}
	// Buffered: the watcher does not wait for a client that went away.
	reply := make(chan Reply, 1)
	select {
	case asks <- Request{a, req.Scope, reply}:
	case <-ctx.Done():
		return
	}
	select {
	case r := <-reply:
		out := socketReply{Result: r.Result}
		if r.Err != nil {
			out.Error = r.Err.Error()
		}
		send(out)
	case <-ctx.Done():
	}
}

// ErrNoService is returned by Ask when no service answers on the socket.
var ErrNoService = errors.New("no wipsync watch answers")

// Ask has the service at path run an action on the folders in scope, and
// returns how it went. It returns ErrNoService when none answers; the
// action was not run then.
func Ask(ctx context.Context, path string, a Action, scope app.Scope) (app.AllResult, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return app.AllResult{}, ErrNoService
	}
	defer c.Close()
	go func() {
		<-ctx.Done()
		c.Close()
	}()
	if err := json.NewEncoder(c).Encode(socketRequest{a.String(), scope}); err != nil {
		return app.AllResult{}, fmt.Errorf("asking the wipsync service: %w", err)
	}
	var r socketReply
	if err := json.NewDecoder(c).Decode(&r); err != nil {
		if ctx.Err() != nil {
			return app.AllResult{}, ctx.Err()
		}
		return app.AllResult{}, fmt.Errorf("the wipsync service stopped before it told how %s went: %w", a, err)
	}
	if r.Error != "" {
		return r.Result, errors.New(r.Error)
	}
	return r.Result, nil
}
