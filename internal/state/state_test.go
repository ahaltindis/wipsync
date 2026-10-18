package state

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestOld(t *testing.T) {
	s := &Store{Dir: filepath.Join(t.TempDir(), "wipsync")}
	dir := filepath.Join(s.Dir, "folders", "Projects", "Freelance", "app")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]string{"base": "abc123\n", "master": "aaa111\n", "pulled": "yes\n", "base.tmp": "x"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(v), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Old("Projects", "Freelance/app")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"base": "abc123", "master": "aaa111", "pulled": "yes"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if got, err := s.Old("Projects", "other"); err != nil || len(got) != 0 {
		t.Errorf("a folder without files: %v, %v", got, err)
	}

	// A held lock and its directory stay; everything else goes.
	unlock, err := s.Lock("Projects", "Freelance/app")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	gone := filepath.Join(s.Dir, "folders", "Hangarea", "dotfiles")
	if err := os.MkdirAll(gone, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gone, "base"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveOld(); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "lock" {
		t.Errorf("left %v, want only the lock", entries)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "folders", "Hangarea")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("an emptied directory stayed: %v", err)
	}
	if err := (&Store{Dir: filepath.Join(t.TempDir(), "none")}).RemoveOld(); err != nil {
		t.Errorf("without a store: %v", err)
	}
}

func TestDefault(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/x/state")
	if s, err := Default(); err != nil || s.Dir != "/x/state/wipsync" {
		t.Errorf("got %+v, %v", s, err)
	}
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/home/u")
	if s, err := Default(); err != nil || s.Dir != "/home/u/.local/state/wipsync" {
		t.Errorf("got %+v, %v", s, err)
	}
}

func TestLock(t *testing.T) {
	s := &Store{Dir: filepath.Join(t.TempDir(), "wipsync")}
	unlock, err := s.Lock("Projects", "Freelance/app")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lock("Projects", "Freelance/app"); !errors.Is(err, ErrBusy) {
		t.Errorf("a second lock = %v, want ErrBusy", err)
	}
	other, err := s.Lock("Projects", "Freelance/other")
	if err != nil {
		t.Errorf("another folder is locked too: %v", err)
	} else {
		other()
	}

	// Another process sees it held, and free once let go.
	file := s.file("Projects", "Freelance/app", "lock")
	if _, err := exec.LookPath("flock"); err != nil {
		t.Skip("no flock command")
	}
	if err := exec.Command("flock", "-n", file, "true").Run(); err == nil {
		t.Error("another process could take the lock")
	}
	unlock()
	if err := exec.Command("flock", "-n", file, "true").Run(); err != nil {
		t.Errorf("the lock is still held after unlock: %v", err)
	}
	again, err := s.Lock("Projects", "Freelance/app")
	if err != nil {
		t.Fatalf("lock after unlock: %v", err)
	}
	again()
}
