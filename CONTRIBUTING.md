# Working on gd-sync

gd-sync is a Go program with its templates built in. One binary per platform,
nothing else to install.

```
cmd/gd-sync/            main: hands the arguments to internal/app
internal/app/           the commands: sync, merge, deploy, new, ...
internal/transfer/      moving files without rsync: lister, compare, tar streams
internal/remote/        the SSH connection, auth and known_hosts
internal/remote/sshtest an in-process SSH server for tests
internal/gitx/          runs the real git binary
internal/migrate/       PHP revisions, dependency validation, planning and runner
internal/site/          a site's .gd-sync.toml / .gd-sync.local.toml
internal/config/        machine-wide settings and the site registry
internal/scaffold/      everything written into a new site, templates included
internal/ui/            output and prompts
```

## Reading the code

If you're learning Go through this project, this order builds up nicely:

1. `internal/ui` and `internal/config` — packages, structs, errors as values.
2. `internal/site` and `internal/gitx` — files, `os/exec`, a small error type.
3. `internal/transfer/match.go` and `compare.go` — maps, slices, pure logic
   with table-driven tests next to it.
4. `internal/remote` — interfaces (`io.Reader`, `io.Writer`), SSH, build tags
   for Windows (`agent_windows.go`).
5. `internal/transfer/tar.go` — goroutines and `io.Pipe` to stream a tar
   while it's being built.
6. `internal/app` — how it all comes together, and `app_test.go` for the flows.

## Building and trying it

```bash
go build -o gd-sync ./cmd/gd-sync
./gd-sync help
```

Point it at a throwaway config and registry while trying things:

```bash
export GD_SYNC_HOME=/tmp/gd-sync-test
./gd-sync config --setup
```

`GD_SYNC_TEMPLATES=internal/scaffold/templates` makes it read the templates from
disk instead of the built-in copy, so template edits show up without a rebuild.

## Tests

```bash
go vet ./...
go test ./...
```

`internal/app/app_test.go` runs the real commands — sync, status, deploy, merge
with and without a conflict, an Astro deploy — over real SSH against
`sshtest`, a server that runs each command with bash in a temp folder. It needs
`git`, `bash`, `perl` and `tar`, and skips itself on Windows. On macOS the
server side uses BSD tar; CI runs the same tests on Ubuntu with GNU tar, which
is what cPanel servers have.

CI runs everything on Ubuntu, macOS and Windows. Migration runner tests execute
with a locally installed `php`; if it is unavailable they skip. To run them
with an existing PHP Docker image instead:

```bash
GD_SYNC_TEST_PHP_DOCKER=php:8.3-cli go test ./internal/migrate -count=1
```

The database command integration tests use a fake WP-CLI executable over the
local SSH test server and require `python3`. They verify orchestration and
recovery without touching a live database.

## Releasing

Push a tag like `v0.1.0`. The release workflow runs the tests and GoReleaser,
which publishes archives for Windows, macOS and Linux on amd64 and arm64.

## Constraints worth keeping

- **The server is the source of truth.** Any new command should preserve that:
  nothing reaches `main` without a server sync first, and deploy refuses to
  overwrite a file the server changed since the last sync.
- **The server side stays minimal.** Only what every cPanel host has: a POSIX
  shell, Perl, GNU tar, `xargs -0`.
- **Paths are slash-separated** in manifests, state and tar streams. Convert with
  `filepath.FromSlash` only where a local file is opened.
- **The local copy only writes what git ignores.** It's a convenience for running
  the site; it must never be able to change a commit, a deploy or your work.
- **Server bytes are never rewritten.** Repos get `core.autocrlf=false` and a
  `* -text` `.gitattributes`; deploys read from git objects, not the working tree.

## Changing what new sites get

`internal/scaffold` owns every file written into a site. `gd-sync scaffold`
rewrites them in an existing site, so changes roll out to sites that already
exist. `GD-SYNC.md` is regenerated each time; a site's own `README.md` never is.
