package db

import (
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func open(t *testing.T) *DB {
	d, err := Open(filepath.Join(t.TempDir(), "wipsync", "wipsync.db"))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestLoadEmpty(t *testing.T) {
	d := open(t)
	data, err := d.Load()
	if err != nil {
		t.Fatal(err)
	}
	if data.Client == "" {
		t.Error("client should default to the hostname")
	}
	if len(data.Folders) != 0 || len(data.Roots) != 0 || data.Repository != "" {
		t.Errorf("expected nothing, got %+v", data)
	}
	if empty, err := d.Empty(); err != nil || !empty {
		t.Errorf("empty = %v, %v", empty, err)
	}
}

func TestSaveLoad(t *testing.T) {
	d := open(t)
	settings := Settings{Client: "laptop", Repository: "sftp:user@host:/repo", PasswordFile: "/home/u/.local/share/wipsync/password"}
	if err := d.SetSettings(settings); err != nil {
		t.Fatal(err)
	}
	app := Folder{Root: "Projects", Path: "Freelance/kadir/boxing-machine", Ignore: []string{".venv"}}
	notes := Folder{Root: "Hangarea", Path: "notes"}
	web := Folder{Root: "Projects", Path: "web", Ignore: []string{}}
	for _, f := range []Folder{app, notes, web} {
		if err := d.AddFolder(f, map[string]string{"Projects": "/home/u/Projects", "Hangarea": "/home/u/Hangarea"}[f.Root]); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.AddFolder(app, "/home/u/Projects"); err == nil {
		t.Error("the same folder was added twice")
	}
	if err := d.SetIgnore(web, []string{"dist"}); err != nil {
		t.Fatal(err)
	}

	// Opened again, as another process would.
	got, err := d.Load()
	if err != nil {
		t.Fatal(err)
	}
	notes.Ignore = []string{} // nil is saved as none
	web.Ignore = []string{"dist"}
	want := &Data{
		Settings: settings,
		Roots:    map[string]string{"Projects": "/home/u/Projects", "Hangarea": "/home/u/Hangarea"},
		Folders:  []Folder{app, notes, web}, // in the order they were added
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
	if empty, _ := d.Empty(); empty {
		t.Error("empty after saving")
	}
}

func TestState(t *testing.T) {
	d := open(t)
	if id, err := d.Base("Projects", "Freelance/app"); err != nil || id != "" {
		t.Errorf("unset base = %q, %v", id, err)
	}
	if err := d.SetBase("Projects", "Freelance/app", "abc123"); err != nil {
		t.Fatal(err)
	}
	if err := d.SetBase("Projects", "Freelance/app", "def456"); err != nil {
		t.Fatal(err)
	}
	if id, err := d.Base("Projects", "Freelance/app"); err != nil || id != "def456" {
		t.Errorf("base = %q, %v; want def456", id, err)
	}
	if id, _ := d.Base("Projects", "Freelance/other"); id != "" {
		t.Errorf("another folder has base %q", id)
	}

	// Each is kept apart from the others.
	if err := d.SetMaster("Projects", "Freelance/app", "aaa111"); err != nil {
		t.Fatal(err)
	}
	if err := d.SetFingerprint("Projects", "Freelance/app", "print"); err != nil {
		t.Fatal(err)
	}
	if err := d.SetAfterPull("Projects", "Freelance/app", true); err != nil {
		t.Fatal(err)
	}
	want := State{Base: "def456", Master: "aaa111", Fingerprint: "print", AfterPull: true}
	if got, err := d.State("Projects", "Freelance/app"); err != nil || got != want {
		t.Errorf("state = %+v, %v; want %+v", got, err, want)
	}
	if err := d.SetAfterPull("Projects", "Freelance/app", false); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.AfterPull("Projects", "Freelance/app"); got {
		t.Error("mark was not cleared")
	}
}

func TestUpdateIsAllOrNothing(t *testing.T) {
	d := open(t)
	f := Folder{Root: "Projects", Path: "app"}
	failed := errors.New("failed")
	err := d.Update(func(w Writer) error {
		if err := w.AddFolder(f, "/home/u/Projects"); err != nil {
			return err
		}
		if err := w.SetBase(f.Root, f.Path, "abc123"); err != nil {
			return err
		}
		return failed
	})
	if !errors.Is(err, failed) {
		t.Fatalf("got %v", err)
	}
	if empty, _ := d.Empty(); !empty {
		t.Error("the folder was saved")
	}
	if id, _ := d.Base(f.Root, f.Path); id != "" {
		t.Errorf("base %q was saved", id)
	}
}

func TestOpenTwice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wipsync.db")
	a, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Error("opened twice")
	}
}

func TestWritersTakeTurns(t *testing.T) {
	d := open(t)
	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i := range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- d.Update(func(w Writer) error {
				return w.AddFolder(Folder{Root: "Projects", Path: string(rune('a'+i%26)) + string(rune('a'+i/26))}, "/p")
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	data, err := d.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Folders) != 40 {
		t.Errorf("%d folders saved, want 40", len(data.Folders))
	}
}
