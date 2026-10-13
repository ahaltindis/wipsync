package app

import (
	"testing"
	"time"
)

func TestDescends(t *testing.T) {
	// a <- b <- c, and a <- x; y stands alone.
	parents := map[string]string{"a": "", "b": "a", "c": "b", "x": "a", "y": "gone"}
	for _, tc := range []struct {
		id, ancestor string
		want         bool
	}{
		{"c", "a", true}, {"c", "b", true}, {"b", "c", false}, {"c", "c", false},
		{"x", "b", false}, {"y", "a", false}, {"a", "", false},
	} {
		if got := descends(parents, tc.id, tc.ancestor); got != tc.want {
			t.Errorf("descends(%s, %s) = %v", tc.id, tc.ancestor, got)
		}
	}
	for _, tc := range []struct{ a, b, want string }{
		{"c", "x", "a"}, {"c", "b", "b"}, {"x", "x", "x"}, {"c", "y", ""},
	} {
		if got := commonAncestor(parents, tc.a, tc.b); got != tc.want {
			t.Errorf("commonAncestor(%s, %s) = %q, want %q", tc.a, tc.b, got, tc.want)
		}
	}
	// A loop in the links must not hang.
	loop := map[string]string{"p": "q", "q": "p"}
	if descends(loop, "p", "z") || commonAncestor(loop, "p", "z") != "" {
		t.Error("a loop was not handled")
	}
}

func TestAge(t *testing.T) {
	cases := map[string]string{
		"20s":   "just now",
		"1m30s": "1 minute ago",
		"59m":   "59 minutes ago",
		"1h":    "1 hour ago",
		"47h":   "47 hours ago",
		"48h":   "2 days ago",
		"240h":  "10 days ago",
	}
	for in, want := range cases {
		d, err := time.ParseDuration(in)
		if err != nil {
			t.Fatal(err)
		}
		if got := Age(d); got != want {
			t.Errorf("age(%s) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	cases := map[int64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KiB", 1536: "1.5 KiB", 3 << 20: "3.0 MiB", 5 << 30: "5.0 GiB"}
	for in, want := range cases {
		if got := FormatBytes(in); got != want {
			t.Errorf("FormatBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestSplitName(t *testing.T) {
	for in, want := range map[string][2]string{
		"Projects/app":           {"Projects", "app"},
		"Projects/Freelance/app": {"Projects", "Freelance/app"},
		"Projects/app/":          {"Projects", "app"},
	} {
		if root, path, ok := SplitName(in); !ok || root != want[0] || path != want[1] {
			t.Errorf("SplitName(%q) = %q, %q, %v", in, root, path, ok)
		}
	}
	for _, in := range []string{"", "app", "/app", "Projects/"} {
		if _, _, ok := SplitName(in); ok {
			t.Errorf("SplitName(%q) was accepted", in)
		}
	}
}
