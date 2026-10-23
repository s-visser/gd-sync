# gd-sync

Git-backed maintenance for GoDaddy cPanel sites you touch once a year.

**The server is the source of truth.** `main` mirrors it. You work on `develop`.
Nothing merges into `main` until `main` has been refreshed from the live server,
and anything that lands on `main` gets deployed.

`gd-sync` works like `git`: it's a global command, and it acts on whichever site
folder you're standing in. Outside one, it says so.

---

## Install

gd-sync is a single program for Windows, macOS and Linux. Download the archive
for your platform from the [releases page](https://github.com/s-visser/gd-sync/releases),
unpack it and put `gd-sync` (`gd-sync.exe` on Windows) somewhere on your PATH.
Or, with Go installed:

```bash
go install github.com/s-visser/gd-sync/cmd/gd-sync@latest
```

Then run the short first-time setup:

```bash
gd-sync config --setup
```

Required: `git` ([Git for Windows](https://git-scm.com/download/win) on Windows).
Optional: `docker` for devcontainers, `gh` for GitHub setup, and an `ssh` client
for `gd-sync shell` (built into macOS, Linux and Windows 10+).

SSH is built in: gd-sync uses your SSH agent or the keys in `~/.ssh`
(`%USERPROFILE%\.ssh` on Windows), and asks for the cPanel password otherwise.
No rsync, tar or Perl needed on your machine — see [How files move](#how-files-move).

### Windows

Site folders can live anywhere, `C:\Users\you\Sites` included, and every command
works from PowerShell, cmd or Windows Terminal. gd-sync sets each site repo to
keep the server's line endings exactly, so files never get converted to CRLF
on the way through.

Docker Desktop reaches folders on `C:\` more slowly than its own disk. The
WordPress devcontainer only mounts your tracked paths, so this stays bearable;
WordPress core lives in a Docker volume.

## Global settings

```bash
gd-sync config                        # show
gd-sync config PROJECTS_DIR ~/Sites   # change one
gd-sync config --setup                # walk through them again
```

| | |
|---|---|
| `PROJECTS_DIR` | where `gd-sync new` creates site folders |
| `GIT_OWNER` | GitHub user or org for new repos |
| `GIT_PRIVATE` | `true` / `false` for new repos |
| `DEFAULT_TYPE` | `wordpress` or `astro` |

Stored in `~/.config/gd-sync/config` (`%AppData%\gd-sync\config` on Windows). With these set, `gd-sync new` already knows the
folder and the GitHub repo name, so you just press enter.

---

## A new domain

```bash
gd-sync new
```

Asks for the domain, short name, local folder, GitHub repo, site type, and the
SSH details. Then it creates the folder and repo, writes the config, installs
the devcontainer and the three workflows, tests SSH, offers to install your SSH
key, pulls the live files onto `main`, offers to pull the database, branches
`develop`, creates the GitHub repo and sets the deploy secrets.

Already have a folder with a `.gd-sync` in it — a clone on another computer, say?

```bash
gd-sync adopt
```

It asks only for the connection details, which are deliberately not committed.

## Day to day

Run these from anywhere inside a site folder.

```bash
gd-sync sync       # main := the live server
gd-sync merge      # sync, merge develop, go live   ← the normal one
gd-sync status     # what's drifted (read-only)
gd-sync db         # live database -> _db/, imported into the container
gd-sync up / down  # start / stop the devcontainer
gd-sync deploy     # ship main by hand, if you'd rather not involve GitHub
gd-sync shell      # SSH in
```

`gd-sync sync --all`, `gd-sync status --all` and `gd-sync db --all` walk every
registered site and can be run from anywhere.

---

## Where configuration lives

There is no central site list. Each repo carries its own:

| file | committed? | holds |
|---|---|---|
| `.gd-sync` | yes | what the site is and where it deploys — also read by the GitHub workers |
| `.gd-sync.local` | no | SSH host, user and port for this machine |

`~/.config/gd-sync/repos` is only a list of folders, so `gd-sync list` and `--all` have
something to walk. Delete it and nothing breaks; `gd-sync adopt` rebuilds entries.

---

## The loop

```
         live server  ──(1) sync ──▶  main  ──(2) deploy worker ──▶  live server
                                       ▲
                                       │ (3) merge, gated
                                       │
                                    develop  ◀── you
```

1. **`gd-sync sync`** copies the live server onto `main` and commits it as
   `[server-sync] …`. It also runs nightly in GitHub Actions, so `main` stays
   honest even if nobody touches the repo for a year.

2. **Push to `main` → deploy worker.** `deploy.yml` runs `gd-sync deploy`: it
   backs up the server, then ships what `main` changed. Commits tagged
   `[server-sync]` are skipped, so a sync can't trigger a pointless redeploy of
   what just came down.

3. **`gd-sync merge`** is the only sanctioned route into `main`: server first, then
   your branch, then push. If the server changed under you, you get a normal Git
   conflict in your IDE instead of silently flattening someone's live edit.

The same rule is enforced on GitHub for pull requests. `verify-main.yml` runs
the server sync, then fails the check if your branch doesn't contain the
server's current state — so you can't merge a PR in the browser and bypass it.

## How files move

No rsync. The server lists its files with a short Perl script (every cPanel
server has Perl), and files travel as one compressed `tar` stream over a single
SSH connection.

- **sync / status** fetch only files whose size or date changed since the last
  sync, hash them, and compare with `main`, so a file that was touched but not
  changed never shows up. What the server held is remembered per machine in
  `.git/gd-sync/server`.
- **deploy** looks at what changed in `main` since the last commit known to be
  live — the last deploy, which is recorded on the server in `~/.gd-sync/`, or
  the last `[server-sync]` snapshot. For each of those files it checks what the
  server holds right now:
  - still the old version: upload the new one, or delete it if `main` did;
  - already the new version: nothing to do;
  - anything else: someone changed it on the server. The deploy stops before
    touching anything and tells you to run `gd-sync merge`.

  Files go up straight from git, so the working tree can't leak into a deploy.
  A file gd-sync never put on the server is never deleted, and there is no
  `rsync --delete` anywhere. Because the record lives on the server, a deploy
  works the same from any computer and from a fresh GitHub runner.
- **Astro** deploys upload the whole build, and remove only files that an
  earlier deploy uploaded and the new build no longer has.

The GitHub workers run the same gd-sync commands, so they follow the same rules.

---

## Devcontainers

### WordPress

`wordpress:php8.3-apache` + MariaDB 11 + Mailpit, plus a PHP workspace container
with WP-CLI, Composer and the mysql client.

- Your tracked plugins and themes are bind-mounted into place — edit in the IDE,
  refresh the browser.
- On first start it imports the newest dump from `_db/`, rewrites the live URL
  to `localhost:8080`, creates a `dev`/`dev` admin, and deactivates caching and
  security plugins that fight local dev.
- All outgoing mail is captured at localhost:8025. This matters more than it
  sounds: a restored production database will happily email real customers the
  moment a plugin fires.
- WordPress core lives in a Docker volume, not the repo.

| | |
|---|---|
| Site | http://localhost:8080 |
| Admin | http://localhost:8080/wp-admin — `dev` / `dev` |
| Mail | http://localhost:8025 |
| MySQL | 127.0.0.1:13306 — `wordpress` / `wordpress` |

### Astro

Node 22 with corepack, so npm/pnpm/yarn all work off whatever lockfile the repo
has. Dev server on 4321, production preview on 4322. `node_modules` lives in a
volume rather than a bind mount — faster, and no native-module mismatches
between macOS and the Linux container.

Astro deploys differently: the worker runs `npm ci && npm run build` and ships
`dist/` into the web root. There's no sync-back, because for a static site the
repo genuinely is the source — `gd-sync sync` says so and does nothing.

---

## GitHub secrets

`gd-sync secrets` sets `GD_SYNC_SSH_KEY`, `GD_SYNC_SSH_HOST`, `GD_SYNC_SSH_USER`
and `GD_SYNC_SSH_PORT` on the repo. Run `gd-sync key` first so the key is
authorised on the server.

Without these the workers can't reach GoDaddy, and you fall back to `gd-sync deploy`
from your computer, which works fine.

The workers install gd-sync with `go install`. While the gd-sync repository is
private, also add a `GD_SYNC_INSTALL_TOKEN` secret: a fine-grained token with
read access to the gd-sync repository's contents.

---

## Deliberate omissions

**No database push.** Overwriting a live WordPress database from a local copy
destroys every order, comment and form entry submitted since you pulled.
Content changes belong on the live site. `gd-sync db` is one-directional on purpose.

**Uploads aren't tracked.** `wp-content/uploads` is excluded everywhere. It's
large, it changes through the admin, and it isn't code.

**`wp-config.php` is never touched.** It holds the live DB credentials and salts.

## Backups

Every deploy — worker or manual — tars the current server copy to
`~/gd-sync-backups/` on the server first, keeping the last 20 per site.

```bash
gd-sync shell
cd ~/public_html && tar xzf ~/gd-sync-backups/<site>-20261022-143000.tar.gz
```
