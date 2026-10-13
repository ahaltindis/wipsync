# wipsync
Sync work-in-progress between different machines. See [DESIGN.md](DESIGN.md).

## Development

Needs Go, and restic 0.17.1 or newer. Tests that use restic are skipped without it.

```sh
go build -o wipsync ./cmd/wipsync   # build
go test ./...                       # test
go vet ./... && gofmt -l .          # check
```

To try it without touching your real config and state:

```sh
export XDG_STATE_HOME=/tmp/try-state
./wipsync --config /tmp/try.toml init
./wipsync --config /tmp/try.toml add <folder>
./wipsync --config /tmp/try.toml snapshot <folder>
./wipsync --config /tmp/try.toml doctor
```
