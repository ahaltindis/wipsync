package state

import (
	"path/filepath"
	"testing"
)

func TestBase(t *testing.T) {
	s := &Store{Dir: filepath.Join(t.TempDir(), "wipsync")}

	if id, err := s.Base("Projects", "Freelance/app"); err != nil || id != "" {
		t.Errorf("unset base = %q, %v", id, err)
	}
	if err := s.SetBase("Projects", "Freelance/app", "abc123"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetBase("Projects", "Freelance/app", "def456"); err != nil {
		t.Fatal(err)
	}
	if id, err := s.Base("Projects", "Freelance/app"); err != nil || id != "def456" {
		t.Errorf("base = %q, %v; want def456", id, err)
	}
	if id, _ := s.Base("Projects", "Freelance/other"); id != "" {
		t.Errorf("another folder has base %q", id)
	}

	// Master is kept apart from base.
	if id, err := s.Master("Projects", "Freelance/app"); err != nil || id != "" {
		t.Errorf("unset master = %q, %v", id, err)
	}
	if err := s.SetMaster("Projects", "Freelance/app", "aaa111"); err != nil {
		t.Fatal(err)
	}
	if id, _ := s.Master("Projects", "Freelance/app"); id != "aaa111" {
		t.Errorf("master = %q", id)
	}
	if id, _ := s.Base("Projects", "Freelance/app"); id != "def456" {
		t.Errorf("setting master changed base to %q", id)
	}
}

func TestAfterPull(t *testing.T) {
	s := &Store{Dir: filepath.Join(t.TempDir(), "wipsync")}
	if got, err := s.AfterPull("Projects", "app"); err != nil || got {
		t.Errorf("unset mark = %v, %v", got, err)
	}
	if err := s.SetAfterPull("Projects", "app", false); err != nil {
		t.Errorf("clearing an unset mark: %v", err)
	}
	if err := s.SetAfterPull("Projects", "app", true); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.AfterPull("Projects", "app"); !got {
		t.Error("mark was not set")
	}
	if err := s.SetAfterPull("Projects", "app", false); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.AfterPull("Projects", "app"); got {
		t.Error("mark was not cleared")
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
