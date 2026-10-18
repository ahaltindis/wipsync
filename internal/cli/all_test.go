package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPushAndPullAll(t *testing.T) {
	a := newEnv(t)
	a.repo()
	a.write("Projects/app/main.py", "print(1)\n")
	a.write("Projects/web/index.html", "<p>1</p>\n")
	for _, dir := range []string{"Projects/app", "Projects/web"} {
		a.must("", "add", filepath.Join(a.home, dir))
		a.must("", "snapshot", filepath.Join(a.home, dir))
	}
	if out := a.must("", "push", "--all"); !strings.Contains(out, "Projects/app: master is now ") ||
		!strings.Contains(out, "Projects/web: master is now ") || !strings.HasSuffix(out, "pushed 2 folders\n") {
		t.Errorf("first push all:\n%s", out)
	}
	if out := a.must("", "push", "--all"); out != "nothing to push\n" {
		t.Errorf("push all again:\n%s", out)
	}

	// A client that does not know the root yet leaves its folders alone.
	b := a.secondClient("client-b")
	out := b.must("", "pull", "--all")
	if !strings.Contains(out, "Projects/app: not on this client, and Projects has no directory here") ||
		!strings.HasSuffix(out, "nothing to pull; 2 folders left alone\n") {
		t.Errorf("pull all without the root:\n%s", out)
	}
	// Once one is pulled, the rest come with pull all.
	b.must("\n", "pull", "Projects/app")
	out = b.must("", "pull", "--all")
	if !strings.Contains(out, "pulled Projects/web into "+filepath.Join(b.home, "Projects/web")+": ") ||
		!strings.HasSuffix(out, "pulled 1 folder\n") {
		t.Errorf("pull all with the root:\n%s", out)
	}

	// A folder changed since its latest snapshot fails; the others go on.
	b.write("Projects/app/main.py", "print(2)\n")
	b.must("", "snapshot", filepath.Join(b.home, "Projects/app"))
	b.write("Projects/web/index.html", "<p>2</p>\n")
	out, err := b.run("", "push", "--all")
	if err == nil || err.Error() != "1 of 2 folders failed" ||
		!strings.Contains(out, "Projects/app: master is now ") ||
		!strings.Contains(out, "Projects/web: changed since its latest snapshot, so not pushed") ||
		!strings.HasSuffix(out, "pushed 1 of 2 folders, 1 failed\n") {
		t.Errorf("push all with a changed folder: err = %v\n%s", err, out)
	}

	// The first client takes what the second pushed.
	out = a.must("", "pull", "--all")
	if !strings.Contains(out, "pulled Projects/app: ") || !strings.HasSuffix(out, "pulled 1 folder\n") {
		t.Errorf("pull all on the first client:\n%s", out)
	}
	if got := a.read("Projects/app/main.py"); got != "print(2)\n" {
		t.Errorf("main.py = %q after pull all", got)
	}

	for _, args := range [][]string{{"pull", "--all", "Projects/app"}, {"pull", "--all", "--from", "client-b"}, {"push", "--all", "Projects/app"}} {
		if _, err := a.run("", args...); err == nil || !strings.Contains(err.Error(), "--all") {
			t.Errorf("%v: err = %v", args, err)
		}
	}
}
