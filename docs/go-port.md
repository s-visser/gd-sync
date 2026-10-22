# Porting gd-sync to Go

The bash script is the reference implementation. The Go version keeps its
commands, its `.gd-sync` / `.gd-sync.local` files, its templates and its
`.git/gd-sync/server` state file, so a site works with either version during
the port. When Go reaches parity, the bash script and `install.sh` go away.

## Goals

- One binary per platform: Windows, macOS, Linux. Nothing else to install
  except `git` (and `docker` / `gh` for the commands that use them).
- Sites in ordinary folders on every platform, including `C:\Users\...`.
- No local dependency on rsync, tar, perl or a POSIX shell.

Not a goal: changing behaviour. Improvements wait until after parity.

## What stays on the server

The server side doesn't change: the Perl lister, GNU `tar` and `xargs -0`, all
standard on cPanel. The lister script is embedded in the binary as a string and
sent over SSH exactly as bash does now.

## Layout

```
cmd/gd-sync/main.go        entry point: parse args, dispatch, exit codes
internal/ui                colours, step/info/warn/die, confirm/ask prompts
internal/config            ~/.config/gd-sync/config, registry
internal/site              .gd-sync parsing, find the site from the cwd
internal/gitx              thin wrapper that runs git and returns output/errors
internal/remote            SSH client: connect once, run commands, stream stdin/stdout
internal/transfer          manifest, excludes, compare, state file, tar streams
internal/scaffold          templates (embed.FS), gitignore, docs, workflows
internal/commands          one file per command: sync.go, deploy.go, ...
```

`templates/` moves to `internal/scaffold/templates/`, because `go:embed` can
only see files at or below the package that embeds them.

## How the bash pieces map over

| bash | Go |
|---|---|
| `say` `step` `info` `warn` `die` `confirm` `ask` | `internal/ui`; `die` becomes returning an error up to `main` |
| `load_config` `write_config` `registry_*` | `internal/config` |
| `find_repo` `load_site` `need_server` | `internal/site`, returning a `Site` struct, not globals |
| `in_repo` `current_branch` `require_clean_tree` | `internal/gitx` |
| `ssh_opts` `rsh` (ControlMaster) | `internal/remote`: one `*ssh.Client` per run, a new session per command |
| `lister_pl` (local `--filter` mode) | Go glob matching in `transfer`; Perl stays for the server side only |
| `remote_manifest` `main_tree` `compare_with_server` `save_server_state` | `internal/transfer`, with maps in place of awk |
| local `tar` / `git hash-object` | `archive/tar` + `compress/gzip`; blob hashes via `git hash-object --stdin-paths` |
| `deploy_main` `deploy_build` `ship_files` `remove_remote` | `transfer` + `commands/deploy.go` |
| `write_*` scaffold functions | `internal/scaffold` with `text/template` |
| `cmd_shell` | runs the system `ssh` (built into Windows 10+) for a real interactive terminal |

## Windows specifics

- **Line endings.** Every repo gd-sync creates or adopts gets
  `core.autocrlf=false`, and the generated `.gitattributes` says `* -text`.
  Server bytes are the truth; Git must not rewrite them.
- **Permissions.** Set `core.fileMode=false`. Uploads keep the server's
  permissions (tar extracts with the server's umask, as now).
- **Long paths.** Set `core.longpaths=true`.
- **Case.** Windows and macOS treat `File.php` and `file.php` as one file.
  Warn when a server manifest has two paths that differ only in case.
- **Paths.** Manifests and state always use `/`. Convert with
  `filepath.ToSlash` / `filepath.FromSlash` at the edge, never inside the
  compare logic.
- **SSH keys and agent.** Keys from `%USERPROFILE%\.ssh`; the Windows OpenSSH
  agent is a named pipe (`\\.\pipe\openssh-ssh-agent`, via `go-winio`).
  Fall back to a password prompt (`golang.org/x/term`) for a first connection,
  as GoDaddy hands out cPanel passwords before keys are installed.
- **Known hosts.** `x/crypto/ssh/knownhosts`, with the same accept-new
  behaviour as now.
- **Docker.** Bind mounts from `C:\` are slow under Docker Desktop. Document it,
  and mount only `wp-content` in the WordPress template.

## Testing

- **Unit tests** (all platforms): exclude matching, the compare/drift logic
  (table-driven: given state, manifest and tree, expect fetch/gone/up/del/drift),
  `.gd-sync` parsing, path conversion.
- **Integration tests** (Linux CI): an `openssh-server` container with Perl and
  GNU tar as the "server", running the same scenarios the bash prototype was
  tested with: first sync, no-op sync, touched-but-identical file, server
  edits, deploy, drift refusal, merge with and without conflict, names with
  spaces and brackets, Astro build deploy.
- **CI matrix:** `ubuntu-latest`, `macos-latest`, `windows-latest` run the unit
  tests and a build; integration tests run on Ubuntu.

## Release

GoReleaser from a GitHub Actions workflow on tags: binaries for
windows/amd64, windows/arm64, darwin/arm64, darwin/amd64, linux/amd64,
linux/arm64. Install through a Homebrew tap and a Scoop bucket first; winget
once it's stable. `gd-sync --version` reports the tag.

## Milestones

Ordered so each step teaches one part of Go and ends with something that runs.

1. **Skeleton.** `go mod init`, `main.go`, `help`, `config`, `list`.
   Modules, packages, error returns, the standard `flag` package.
2. **Sites.** `.gd-sync` parsing, find the site from the cwd, registry.
   Structs, files, `filepath`.
3. **Git wrapper.** `os/exec`, capturing output, turning exit codes into errors.
4. **SSH.** Connect, run a command, `shell`, `key`. Interfaces, `io.Reader` /
   `io.Writer`, streaming.
5. **Transfer.** `status` first (read-only), then `sync`, then `deploy`. Maps,
   `archive/tar`, table-driven tests. This is the core, so take the time here.
6. **Scaffolding.** `new`, `adopt`, `scaffold` with `embed` and `text/template`.
7. **The rest.** `merge`, `db`, `up` / `down`, `secrets`.
8. **Windows hardening, CI matrix, release.** Then remove the bash script.

## Before starting

- Run the bash prototype against a real GoDaddy site: first sync, a status, a
  deploy, and a drift refusal. The fake server used in testing has BSD tar, so
  the GNU-only `--no-overwrite-dir` and Perl on the server are not yet proven.
- Decide on the module path, e.g. `github.com/<owner>/gd-sync`.
- Start with the standard `flag` package; it's enough for these commands and
  there's less to learn. Switch to `cobra` only if it starts to hurt.
