package tray

import (
	"bytes"
	"image/png"
	"os/exec"
	"testing"
	"time"

	"github.com/ahaltindis/wipsync/internal/watch"
)

func TestTooltip(t *testing.T) {
	at := time.Date(2026, 10, 18, 9, 5, 0, 0, time.Local)
	got := Tooltip(watch.Status{Detail: "behind: Projects/app", Events: []watch.Event{
		{At: at, Line: "Projects/app: master moved to a1a1a1a1 (desktop), pull to take it"},
		{At: at.Add(-time.Hour), Line: "watching 2 folders"},
	}})
	want := "wipsync: behind: Projects/app\n\n" +
		"09:05  Projects/app: master moved to a1a1a1a1 (desktop), pull to take it\n" +
		"08:05  watching 2 folders"
	if got != want {
		t.Errorf("tooltip =\n%s\nwant\n%s", got, want)
	}
	if got := Tooltip(watch.Status{Detail: "starting"}); got != "wipsync: starting" {
		t.Errorf("without events: %q", got)
	}
}

func TestIcons(t *testing.T) {
	for st, c := range colours {
		img, err := png.Decode(bytes.NewReader(icon(c)))
		if err != nil {
			t.Fatalf("%s: %v", st, err)
		}
		// The circle (radius r, about 24 pixels) is drawn in the colour; between it and the W is empty.
		if _, _, _, a := img.At(size/2, size/2+16).RGBA(); a != 0 {
			t.Errorf("%s: below the W is drawn", st)
		}
		r, g, b, a := img.At(size/2, size/2+24).RGBA()
		if a != 0xffff || r>>8 != uint32(c.R) || g>>8 != uint32(c.G) || b>>8 != uint32(c.B) {
			t.Errorf("%s: the bottom of the circle is %d %d %d %d", st, r>>8, g>>8, b>>8, a>>8)
		}
	}
}

func TestDefaultOpen(t *testing.T) {
	got := DefaultOpen("/usr/bin/wipsync", "/home/me/it's/config.toml")
	want := `systemd-run --user --collect --quiet xdg-terminal-exec '/usr/bin/wipsync' --config '/home/me/it'\''s/config.toml' tui`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	// sh reads the quoted path back as one word.
	out, err := exec.Command("sh", "-c", "printf %s "+quote("/home/me/it's/a b")).Output()
	if err != nil || string(out) != "/home/me/it's/a b" {
		t.Errorf("sh read %q, %v", out, err)
	}
}
