package master

// The server side is these scripts, run by sh. They are sent over ssh, so
// the server needs no wipsync: only sh, and flock for a push. They are
// written without single quotes, which keeps quoting them simple.
//
// Arguments: the repository directory, then the folder's root and path.
// A dash stands for "no snapshot", because an empty argument does not
// survive the trip through ssh.

// Exit codes the scripts use beyond 0 and 1.
const (
	exitMoved        = 3
	exitNoRepository = 4
	exitNoFlock      = 5
)

const unset = "-"

// scriptHead checks the repository is there, so a mistyped path does not
// leave pointer directories around, and sets dir to the folder's directory.
const scriptHead = `
umask 077
repo=$1 root=$2 path=$3
if [ ! -d "$repo" ]; then exit 4; fi
dir="${repo}.wipsync/folders/$root/$path"
`

// scriptCheck reports whether a push could work at all.
const scriptCheck = `
if [ ! -d "$1" ]; then exit 4; fi
command -v flock >/dev/null 2>&1 || exit 5
echo ok
`

// scriptMaster prints the folder's master, or a dash.
const scriptMaster = scriptHead + `
if [ -f "$dir/master" ]; then cat "$dir/master"; else echo -; fi
`

// scriptMove sets master to $5 if it is still $4. Otherwise it prints
// "moved" and the current master, and exits with code 3. The lock makes
// the comparison and the replacement one step, so of several pushes from
// the same starting point one succeeds.
const scriptMove = scriptHead + `
expect=$4 new=$5
command -v flock >/dev/null 2>&1 || exit 5
mkdir -p "$dir" || exit 1
exec 9>"$dir/lock" || exit 1
flock 9 || exit 1
current=-
if [ -f "$dir/master" ]; then current=$(cat "$dir/master"); fi
if [ "$current" != "$expect" ]; then
	echo "moved $current"
	exit 3
fi
if [ "$current" != "$new" ]; then
	printf "%s\n" "$new" > "$dir/master.tmp" || exit 1
	mv "$dir/master.tmp" "$dir/master" || exit 1
fi
echo ok
`
