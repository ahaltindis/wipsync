# wipsync
Sync work-in-progress between different machines. See [DESIGN.md](DESIGN.md).

## Development

Needs Go.

```sh
go build -o wipsync ./cmd/wipsync   # build
go test ./...                       # test
go vet ./... && gofmt -l .          # check
```

To try it without touching your real config:

```sh
./wipsync --config /tmp/try.toml add <folder>
```
