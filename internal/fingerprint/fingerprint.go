// Package fingerprint tells whether a folder changed since it was last
// looked at, without reading file contents and without restic.
//
// restic alone is not enough for that: it saves a new snapshot when only a
// directory's timestamp moved, which every "git status" causes.
package fingerprint

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// Of returns the fingerprint of the folder at dir. Two fingerprints are
// equal when nothing that a snapshot should record changed in between.
//
// It covers every file's path, size, mode and timestamps, symlink targets,
// and which directories exist with which mode. It leaves out:
//
//   - directory timestamps, which change when a file is created and removed
//     again, as git's lock file is;
//   - the stat cache inside .git/index, which git rewrites after a pull.
//     The index counts by what is staged in it.
//
// ignore is the folder's ignore rules. Only rules that are a plain name
// are applied; files under any other rule are fingerprinted anyway, which
// can only make a folder look changed when it is not.
func Of(ctx context.Context, dir string, ignore []string) (string, error) {
	skip := map[string]bool{}
	for _, rule := range ignore {
		if rule != "" && !strings.ContainsAny(rule, `/*?[\`) {
			skip[rule] = true
		}
	}
	h := sha256.New()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == dir {
			return nil
		}
		if skip[d.Name()] {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if errors.Is(err, fs.ErrNotExist) {
			// Gone between listing and looking: the next run sees that.
			return nil
		} else if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			fmt.Fprintf(h, "d %q %o\n", rel, info.Mode())
		case d.Name() == "index" && filepath.Base(filepath.Dir(path)) == ".git" && writeStaged(ctx, h, rel, filepath.Dir(filepath.Dir(path))):
		default:
			target := ""
			if info.Mode()&fs.ModeSymlink != 0 {
				if target, err = os.Readlink(path); err != nil {
					return err
				}
			}
			var ctime syscall.Timespec
			if st, ok := info.Sys().(*syscall.Stat_t); ok {
				ctime = st.Ctim
			}
			fmt.Fprintf(h, "f %q %o %d %d %d.%d %q\n", rel, info.Mode(), info.Size(),
				info.ModTime().UnixNano(), ctime.Sec, ctime.Nsec, target)
		}
		return ctx.Err()
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// writeStaged adds what is staged in the index of the git repository at
// worktree. It reports false when that cannot be read, for example without
// git, and the index is then fingerprinted like any other file.
func writeStaged(ctx context.Context, h hash.Hash, rel, worktree string) bool {
	// --debug adds each entry's flags, such as intent-to-add, along with
	// the stat cache that must be left out.
	cmd := exec.CommandContext(ctx, "git", "-C", worktree, "ls-files", "--stage", "-v", "--debug")
	// Never take the index lock: that would itself touch .git.
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	fmt.Fprintf(h, "g %q\n", rel)
	for _, line := range bytes.Split(out, []byte("\n")) {
		if !bytes.HasPrefix(line, []byte("  ")) {
			h.Write(line) // an entry: tag, mode, object, stage, path
			h.Write([]byte("\n"))
		} else if _, flags, ok := bytes.Cut(line, []byte("flags: ")); ok {
			h.Write(flags)
			h.Write([]byte("\n"))
		}
	}
	return true
}
