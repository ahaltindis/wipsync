// Package restic runs the restic binary. Nothing else in wipsync knows its
// flags or output format.
package restic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const binary = "restic"

// MinVersion is the oldest restic that has backup --skip-if-unchanged and
// restore --delete (0.17.0), and its own exit code for a wrong password
// (0.17.1).
var MinVersion = Version{0, 17, 1}

var (
	ErrNoRepository  = errors.New("there is no repository at that location")
	ErrWrongPassword = errors.New("the password does not open the repository")
)

// restic's exit codes for the two failures callers tell apart.
const (
	exitNoRepository  = 10
	exitWrongPassword = 12
)

type Version [3]int

func (v Version) String() string { return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2]) }

func (v Version) Less(o Version) bool {
	for i := range v {
		if v[i] != o[i] {
			return v[i] < o[i]
		}
	}
	return false
}

// ParseVersion reads the output of "restic version", which looks like
// "restic 0.19.1 compiled with go1.26.4 on linux/amd64".
func ParseVersion(out string) (Version, error) {
	var v Version
	fields := strings.Fields(out)
	if len(fields) < 2 || fields[0] != "restic" {
		return v, fmt.Errorf("unexpected restic version output %q", strings.TrimSpace(out))
	}
	if n, _ := fmt.Sscanf(fields[1], "%d.%d.%d", &v[0], &v[1], &v[2]); n != 3 {
		return v, fmt.Errorf("unexpected restic version %q", fields[1])
	}
	return v, nil
}

// InstalledVersion returns the version of the restic found in PATH.
func InstalledVersion(ctx context.Context) (Version, error) {
	out, err := exec.CommandContext(ctx, binary, "version").Output()
	if errors.Is(err, exec.ErrNotFound) {
		return Version{}, errors.New("restic is not installed")
	} else if err != nil {
		return Version{}, fmt.Errorf("restic version: %w", err)
	}
	return ParseVersion(string(out))
}

// CheckVersion fails when restic is missing or older than MinVersion.
func CheckVersion(ctx context.Context) error {
	v, err := InstalledVersion(ctx)
	if err != nil {
		return err
	}
	if v.Less(MinVersion) {
		return fmt.Errorf("restic %s is too old, %s or newer is needed", v, MinVersion)
	}
	return nil
}

// Client reaches one repository. Exactly one of PasswordFile and
// PasswordCommand is set; restic reads the password itself.
type Client struct {
	Repository      string
	PasswordFile    string
	PasswordCommand string
}

func (c *Client) run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "RESTIC_REPOSITORY="+c.Repository)
	if c.PasswordFile != "" {
		cmd.Env = append(cmd.Env, "RESTIC_PASSWORD_FILE="+c.PasswordFile)
	}
	if c.PasswordCommand != "" {
		cmd.Env = append(cmd.Env, "RESTIC_PASSWORD_COMMAND="+c.PasswordCommand)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err == nil {
		return out, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		switch exit.ExitCode() {
		case exitNoRepository:
			return nil, fmt.Errorf("restic %s: %w", args[0], ErrNoRepository)
		case exitWrongPassword:
			return nil, fmt.Errorf("restic %s: %w", args[0], ErrWrongPassword)
		}
	}
	return nil, fmt.Errorf("restic %s: %s", args[0], failure(err, out, stderr.Bytes()))
}

type message struct {
	Type    string `json:"message_type"`
	Message string `json:"message"`

	// summary of a backup
	SnapshotID      string `json:"snapshot_id"`
	FilesNew        int    `json:"files_new"`
	FilesChanged    int    `json:"files_changed"`
	DataAddedPacked int64  `json:"data_added_packed"`
}

// find returns the first JSON line of the given type.
func find(output []byte, msgType string) (message, bool) {
	sc := bufio.NewScanner(bytes.NewReader(output))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var m message
		if json.Unmarshal(sc.Bytes(), &m) == nil && m.Type == msgType {
			return m, true
		}
	}
	return message{}, false
}

// failure picks the most useful description of a failed run.
func failure(err error, stdout, stderr []byte) string {
	for _, output := range [][]byte{stderr, stdout} {
		if m, ok := find(output, "exit_error"); ok {
			return m.Message
		}
	}
	if text := strings.TrimSpace(string(stderr)); text != "" {
		return text
	}
	return err.Error()
}

// Init creates the repository.
func (c *Client) Init(ctx context.Context) error {
	_, err := c.run(ctx, "", "init")
	return err
}

// Ping checks that the repository can be reached and the password opens it.
// It fails with ErrNoRepository or ErrWrongPassword in those two cases.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.run(ctx, "", "cat", "config", "--no-lock")
	return err
}

type BackupOptions struct {
	Dir      string // the folder; its contents are stored with relative paths
	Host     string
	Tags     []string
	Excludes []string
	Parent   string // snapshot the folder is based on, if any
}

type BackupResult struct {
	SnapshotID   string // empty when nothing changed, so no snapshot was made
	FilesNew     int
	FilesChanged int
	BytesStored  int64
}

// Backup snapshots a folder, unless nothing changed since its parent.
func (c *Client) Backup(ctx context.Context, o BackupOptions) (BackupResult, error) {
	args := []string{"backup", ".", "--json", "--skip-if-unchanged", "--host", o.Host}
	for _, t := range o.Tags {
		args = append(args, "--tag", t)
	}
	for _, e := range o.Excludes {
		args = append(args, "--exclude", e)
	}
	if o.Parent != "" {
		args = append(args, "--parent", o.Parent)
	}
	out, err := c.run(ctx, o.Dir, args...)
	if err != nil {
		return BackupResult{}, err
	}
	m, ok := find(out, "summary")
	if !ok {
		return BackupResult{}, errors.New("restic backup: no summary in its output")
	}
	return BackupResult{
		SnapshotID:   m.SnapshotID,
		FilesNew:     m.FilesNew,
		FilesChanged: m.FilesChanged,
		BytesStored:  m.DataAddedPacked,
	}, nil
}
