# gd-sync

Git-backed maintenance for GoDaddy cPanel sites you touch once a year.

**The server is the source of truth.** `main` mirrors the paths you maintain.
You work on `develop`. The rest of the live site is copied into the same folder
so it runs locally, but git ignores it.
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

Docker Desktop reaches folders on `C:\` more slowly than its own disk, and the
WordPress devcontainer serves the whole site from the folder, so the local
site is slower there than on macOS or Linux.

## Global settings

```bash
gd-sync config                        # show
gd-sync config projects_dir ~/Sites   # change one
gd-sync config --setup                # walk through them again
```

| | |
|---|---|
| `projects_dir` | where `gd-sync new` creates site folders |
| `git_owner` | GitHub user or org for new repos |
| `git_private` | `true` / `false` for new repos |
| `default_type` | `wordpress` or `astro` |
| `ssh_host`, `ssh_user`, `ssh_port` | the server new sites are on, when your sites share one |

Stored in `~/.config/gd-sync/config.toml` (`%AppData%\gd-sync\config.toml` on Windows). With these set, `gd-sync new` only needs the
domain: the folder, the GitHub repo, the server and the web root all default
from it, so you press enter for the rest.

---

## A new domain

```bash
gd-sync new
```

Asks for the domain, the site's name, local folder, GitHub repo, site type and
the SSH details. The name defaults to the full domain, and so do the folder and
the repo: `wolfpackvision.com` → `~/Sites/wolfpackvision.com` and
`you/wolfpackvision.com`. Like `git clone`, it refuses a folder that already
has something in it. Then it creates the folder and a git repo, writes the config, installs
the devcontainer and the three workflows, tests SSH, matches the live PHP version,
offers to install your SSH key, downloads the whole live site into the folder,
asks about vendor folders, credentials and large binaries in the paths you
maintain, offers to pull the database, creates the GitHub repo — empty — and sets
the deploy secrets.

It commits and pushes nothing. Check what git will take in, then make the first
commit yourself:

```bash
git status
git add -A && git commit -m "[server-sync] Live server state"
git branch develop && git push -u origin main develop
```

`[server-sync]` marks `main` as what the server has, so the deploy worker skips
it and the next deploy knows where it stands. A GitHub repo that already exists
is left alone — it may hold history; add it as `origin` yourself if you mean to.

The PHP version comes from the server's settings — on GoDaddy, the CloudLinux PHP
Selector's server-wide version. When the site itself answers with a different one
(its `X-Powered-By` header), gd-sync still goes by the server and warns: the
domain probably has its own version set in cPanel's MultiPHP Manager or an
`.htaccess` handler, which is worth fixing on the server.

It asks which paths you maintain: the plugins and themes you write, e.g.
`wp-content/plugins/acme-custom`. Leave it empty to start with none. You can
add them later to `tracked` in `.gd-sync.toml`; run `gd-sync scaffold` afterwards so
`.gitignore` follows, and commit the files it now shows on `develop`. The
server's copy lands on `main` at the next sync, and the two merge cleanly.

Already have a folder with a `.gd-sync.toml` in it — a clone on another computer, say?

```bash
gd-sync adopt
```

It asks only for the connection details, which are deliberately not committed.

## Day to day

Run these from anywhere inside a site folder.

```bash
gd-sync sync       # main := the live server; refresh the local copy
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
| `.gd-sync.toml` | yes | what the site is and where it deploys — also read by the GitHub workers |
| `.gd-sync.local.toml` | no | SSH host, user and port for this machine |

```toml
# .gd-sync.toml
name = "wolfpackvision.com"
type = "wordpress"

live_url = "https://wolfpackvision.com"
local_url = "http://localhost:8080"

remote_root = "/home/you/public_html/wolfpackvision.com"

tracked = [
  "wp-content/themes/wolfpack",
]
exclude = []

php_version = "8.5"
```

`~/.config/gd-sync/repos` is only a list of folders, so `gd-sync list` and `--all` have
something to walk. Delete it and nothing breaks; `gd-sync adopt` rebuilds entries.

---

## What's in a site folder

The folder is the live web root, plus gd-sync's own files:

| | in git? |
|---|---|
| the paths you maintain (`tracked`) | yes — on `main` as the server has them, on `develop` as you change them |
| `.gd-sync.toml`, `.gitignore`, `.devcontainer/`, `.github/`, `.run/`, `README.md`, `GD-SYNC.md` | yes |
| everything else: WordPress core, other plugins and themes, uploads, `wp-config.php` | no — the local copy |

`.gitignore` is generated from `tracked`: it ignores everything, then lets back
in gd-sync's files and each maintained path. With nothing maintained, git holds
only gd-sync's files.

### Inside a maintained path: vendor folders, credentials, large binaries

Before server files go into git, sync checks them — and `gd-sync scaffold` checks
newly maintained paths — and asks about:

| found | offered | recorded as |
|---|---|---|
| a `vendor/` folder | keep it out of git; with a `composer.json` next to it, every deploy rebuilds it | `[[vendor]]` |
| a credential-looking file (asked one by one) | keep it out of git | `ignore` |
| binaries of 1 MB or more | Git LFS, or keep them out of git | `lfs`, `ignore` |

```toml
ignore = ["wp-content/plugins/shop/auth/gtranslate.json"]
lfs = ["wp-content/themes/acme/assets/intro.mp4"]

[[vendor]]
path = "wp-content/plugins/shop/vendor"
build = "composer install --no-dev --no-interaction --no-progress --prefer-dist"
```

What git leaves out is never synced or deployed — the server's copy stays as it
is — but it is downloaded with the rest of the site, so the site runs locally.
The exception is a vendor folder with a `build`: deploy exports main's copy of
the folder above it, runs the build there, and only then uploads anything. The
server's vendor folder ends up exactly what the build made, and if the build
fails, nothing goes live. On GitHub the deploy worker sets up PHP (`php_version`)
and Composer for it.

Without a terminal, or with `-y`, nobody can answer, so those files stay out of
git until the next sync in a terminal asks.

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
- **sync** then refreshes the local copy of the rest of the site. It downloads
  a file when its size or date differs from the local one, and stamps it with
  the server's date. It only ever writes files git ignores, so it can't touch
  your work. A local file is only deleted when the server no longer has it,
  gd-sync put it there, and it's unchanged since — that list lives in
  `.git/gd-sync/mirror`. Server logs (`error_log`, `*.log`) and
  `wp-content/cache` are skipped. Big downloads show a progress bar with
  size, speed and time left. `--no-mirror` updates `main` only; the
  GitHub workers use it, and `gd-sync merge` skips the copy too.
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
with WP-CLI, Composer, the mysql client, git, gh and gd-sync.

- It serves the site folder itself — the paths you maintain plus the local copy
  of everything else, so core, plugins and themes are the live versions. Edit
  in the IDE, refresh the browser.
- `wp-config.php` and `.htaccess` are replaced, inside the container only, by
  local versions in `.devcontainer/`: the live ones hold the live database
  password, and may force HTTPS or load server-only firewall files. The local
  `wp-config.php` reads the live one for its table prefix.
- On first start it imports the newest dump from `_db/`, rewrites the live URL
  to `localhost:8080`, creates a `dev`/`dev` admin, and deactivates caching and
  security plugins that fight local dev.
- All outgoing mail is captured at localhost:8025. This matters more than it
  sounds: a restored production database will happily email real customers the
  moment a plugin fires.
- Before the first `gd-sync sync` there's no copy yet, and the wordpress image
  unpacks a stock core into the folder instead.

| | |
|---|---|
| Site | http://localhost:8080 |
| Admin | http://localhost:8080/wp-admin — `dev` / `dev` |
| Mail | http://localhost:8025 |
| MySQL | 127.0.0.1:13306 — `wordpress` / `wordpress` |

### gd-sync inside the container

Both devcontainers work on their own, so an IDE that runs inside the container
(JetBrains does) has everything it needs; VS Code, or a terminal on your
computer, keeps working from the host as before.

- gd-sync is built into the image with `go install`, at the version that wrote
  the folder (`GD_SYNC_VERSION` in `.devcontainer/.env`, like the workflows).
  Run `gd-sync scaffold` after upgrading and rebuild the container to follow.
- Your `~/.ssh` is mounted at the container user's `~/.ssh`, read-write, so the
  keys, `known_hosts` and `gd-sync key` are shared with your computer.
- `gh` keeps its login in the `gd-sync-gh` Docker volume, shared by every
  gd-sync site. It is external to each site's Compose project, so no site owns
  it; `initializeCommand` in `devcontainer.json` and `gd-sync up` create it. Log in once, in any of them: `gh auth login && gh auth setup-git`.
  After that `git push`, `gd-sync merge` and `gd-sync secrets` work in here.
- `gd-sync db` imports the dump straight into the container's database.
  `gd-sync up` and `down` drive Docker, so they only run on your computer.

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

**Uploads aren't tracked.** `wp-content/uploads` never goes into git or up in a
deploy, even inside a maintained path. It's large, it changes through the
admin, and it isn't code. The local copy has it, so images work locally.

**`wp-config.php` is never touched.** It holds the live DB credentials and
salts. The local copy has it, git doesn't, and no deploy sends it.

## Backups

Every deploy — worker or manual — tars the current server copy to
`~/gd-sync-backups/` on the server first, keeping the last 20 per site.

```bash
gd-sync shell
cd ~/public_html/<domain> && tar xzf ~/gd-sync-backups/<site>-20261022-143000.tar.gz
```
