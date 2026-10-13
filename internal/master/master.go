// Package master keeps each folder's master pointer: the snapshot a push
// marked as its known good state. The pointers are small files beside the
// repository, in <repository>.wipsync, and are read and moved by short
// scripts that run where the repository is.
package master

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
)

// Pointers is how a client reads and moves master pointers, wherever the
// repository is.
type Pointers interface {
	// Master returns the folder's master, or "" if it has none.
	Master(ctx context.Context, root, path string) (string, error)
	// Move sets the folder's master to id, but only if it is still expect
	// ("" for not set). Otherwise it changes nothing and returns a
	// *MovedError. Of several callers moving from the same expect at once,
	// one succeeds.
	Move(ctx context.Context, root, path, expect, id string) error
	// All returns the master of every folder that has one, keyed by the
	// folder as "Root/path".
	All(ctx context.Context) (map[string]string, error)
	// Check reports whether the pointers can be reached and moved.
	Check(ctx context.Context) error
}

// MovedError says that master is not what the caller expected.
type MovedError struct {
	Current string // what master is instead; empty if it is not set
}

func (e *MovedError) Error() string {
	if e.Current == "" {
		return "master is not set"
	}
	return "master is " + e.Current
}

// Open returns the pointers that belong to a restic repository: on this
// machine for a local directory, through ssh for an sftp repository.
func Open(repository string) (Pointers, error) {
	if rest, ok := strings.CutPrefix(repository, "sftp:"); ok {
		t, err := parseSFTP(rest)
		if err != nil {
			return nil, fmt.Errorf("repository %s: %w", repository, err)
		}
		return &pointers{where: t.host, repo: t.path, run: t.run}, nil
	}
	if rest, ok := strings.CutPrefix(repository, "local:"); ok {
		repository = rest
	} else if scheme, _, found := strings.Cut(repository, ":"); found && !strings.Contains(scheme, "/") {
		return nil, fmt.Errorf("master needs a repository on local disk or reached over ssh, not %s:", scheme)
	}
	abs, err := filepath.Abs(repository)
	if err != nil {
		return nil, err
	}
	return &pointers{where: "this machine", repo: abs, run: runLocal}, nil
}

// runner builds the command that runs a script with arguments.
type runner func(ctx context.Context, script string, args ...string) *exec.Cmd

func runLocal(ctx context.Context, script string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, "sh", append([]string{"-c", script, "sh"}, args...)...)
}

// target is where an sftp repository lives.
type target struct {
	host string // as given to ssh, with the user if there is one
	port string
	path string // of the repository on the server
}

// sshCommand builds the ssh call that runs a command line on the server.
var sshCommand = func(ctx context.Context, t target, commandLine string) *exec.Cmd {
	var args []string
	if t.port != "" {
		args = append(args, "-p", t.port)
	}
	args = append(args, "--", t.host, commandLine)
	return exec.CommandContext(ctx, "ssh", args...)
}

// run sends the script through ssh. ssh hands one command line to the
// user's shell on the server, so every part is quoted into a single word.
func (t target) run(ctx context.Context, script string, args ...string) *exec.Cmd {
	words := []string{"sh", "-c", shellQuote(script), "sh"}
	for _, a := range args {
		words = append(words, shellQuote(a))
	}
	return sshCommand(ctx, t, strings.Join(words, " "))
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// parseSFTP reads what follows "sftp:" in a restic repository. restic has
// two forms: user@host:path, and //user@host:port/path, where a path that
// is absolute starts with a second slash.
func parseSFTP(s string) (target, error) {
	var t target
	if rest, ok := strings.CutPrefix(s, "//"); ok {
		hostport, path, _ := strings.Cut(rest, "/")
		t.path = path
		user, hp, hasUser := strings.Cut(hostport, "@")
		if !hasUser {
			user, hp = "", hostport
		}
		t.host = hp
		if h, p, err := net.SplitHostPort(hp); err == nil {
			t.host, t.port = h, p
		}
		t.host = strings.Trim(t.host, "[]")
		if hasUser {
			t.host = user + "@" + t.host
		}
	} else {
		// The host ends at the first colon that is not inside [brackets].
		split := strings.Index(s, ":")
		if end := strings.Index(s, "]"); end >= 0 {
			split = end + strings.Index(s[end:], ":")
			if split < end {
				split = -1
			}
		}
		if split < 0 {
			return t, errors.New("no path after the host")
		}
		t.host = strings.NewReplacer("[", "", "]", "").Replace(s[:split])
		t.path = s[split+1:]
	}
	if t.host == "" || strings.HasSuffix(t.host, "@") || t.path == "" {
		return t, errors.New("it needs a host and a path")
	}
	return t, nil
}

type pointers struct {
	where string // the machine the repository is on, for messages
	repo  string // the repository directory there
	run   runner
}

// script runs one of the scripts against the repository and returns what
// it printed and its exit code. Only exit codes that are answers come back
// without an error.
func (p *pointers) script(ctx context.Context, script string, args ...string) (string, int, error) {
	cmd := p.run(ctx, script, append([]string{strings.TrimRight(p.repo, "/")}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	reply := strings.TrimSpace(string(out))
	if err == nil {
		return reply, 0, nil
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return "", 0, fmt.Errorf("%s: %w", p.where, err)
	}
	switch code := exit.ExitCode(); code {
	case exitMoved:
		return reply, code, nil
	case exitNoRepository:
		return "", code, fmt.Errorf("there is no repository at %s on %s", p.repo, p.where)
	case exitNoFlock:
		return "", code, fmt.Errorf("flock is not installed on %s, and a push needs it", p.where)
	default:
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return "", code, fmt.Errorf("%s: %s", p.where, detail)
	}
}

// validFolder checks a folder's root and path before they are used to
// build a path beside the repository: they must not lead outside it.
func validFolder(root, path string) error {
	if root == "" || root == "." || root == ".." || strings.ContainsAny(root, `/\`) {
		return fmt.Errorf("invalid root %q", root)
	}
	if path == "" || !filepath.IsLocal(path) || filepath.ToSlash(filepath.Clean(path)) != path {
		return fmt.Errorf("invalid path %q", path)
	}
	return nil
}

func validID(id string) bool {
	if len(id) != 64 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// decodeID reads a snapshot ID from a script's reply.
func (p *pointers) decodeID(s string) (string, error) {
	if s == unset {
		return "", nil
	}
	if !validID(s) {
		return "", fmt.Errorf("%s: unexpected master %q", p.where, s)
	}
	return s, nil
}

func (p *pointers) Master(ctx context.Context, root, path string) (string, error) {
	if err := validFolder(root, path); err != nil {
		return "", err
	}
	reply, _, err := p.script(ctx, scriptMaster, root, path)
	if err != nil {
		return "", err
	}
	return p.decodeID(reply)
}

func (p *pointers) Move(ctx context.Context, root, path, expect, id string) error {
	if err := validFolder(root, path); err != nil {
		return err
	}
	if !validID(id) {
		return fmt.Errorf("invalid snapshot ID %q", id)
	}
	if expect == "" {
		expect = unset
	} else if !validID(expect) {
		return fmt.Errorf("invalid snapshot ID %q", expect)
	}
	reply, code, err := p.script(ctx, scriptMove, root, path, expect, id)
	if err != nil {
		return err
	}
	if code == exitMoved {
		current, ok := strings.CutPrefix(reply, "moved ")
		if !ok {
			return fmt.Errorf("%s: unexpected reply %q", p.where, reply)
		}
		current, err := p.decodeID(current)
		if err != nil {
			return err
		}
		return &MovedError{Current: current}
	}
	if reply != "ok" {
		return fmt.Errorf("%s: unexpected reply %q", p.where, reply)
	}
	return nil
}

func (p *pointers) All(ctx context.Context) (map[string]string, error) {
	reply, _, err := p.script(ctx, scriptAll)
	if err != nil {
		return nil, err
	}
	all := map[string]string{}
	for _, line := range strings.Split(reply, "\n") {
		if line == "" {
			continue
		}
		// "<id> ./<root>/<path>/master"
		id, file, ok := strings.Cut(line, " ")
		folder, isPointer := strings.CutSuffix(strings.TrimPrefix(file, "./"), "/master")
		if !ok || !isPointer || !validID(id) {
			return nil, fmt.Errorf("%s: unexpected reply %q", p.where, line)
		}
		all[folder] = id
	}
	return all, nil
}

func (p *pointers) Check(ctx context.Context) error {
	reply, _, err := p.script(ctx, scriptCheck)
	if err != nil {
		return err
	}
	if reply != "ok" {
		return fmt.Errorf("%s: unexpected reply %q", p.where, reply)
	}
	return nil
}
