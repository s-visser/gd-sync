# gd

Git-backed maintenance for GoDaddy cPanel sites you touch once a year.

**The server is the source of truth.** `main` mirrors it. You work on `develop`.
Nothing merges into `main` until `main` has been refreshed from the live server,
and anything that lands on `main` gets deployed.

`gd` works like `git`: it's a global command, and it acts on whichever site
folder you're standing in. Outside one, it says so.

---

## Install (macOS)

```bash
git clone <this-repo> ~/gd
cd ~/gd && ./install.sh
```

The installer checks your tools, symlinks `gd` onto your PATH, adds the PATH
line to `.zshrc` if needed, and runs first-time setup. It installs a symlink
rather than a copy, so `git pull` in `~/gd` updates the command everywhere.

Required: `git`, `ssh`, `rsync` (Xcode command line tools cover the first two).
Optional: `docker` for devcontainers, `gh` for GitHub setup, `mysql-client`.

macOS ships bash 3.2 and BSD userland; `gd` is written for both, so Homebrew
bash is not required.

## Global settings

```bash
gd config                        # show
gd config PROJECTS_DIR ~/Sites   # change one
gd config --setup                # walk through them again
```

| | |
|---|---|
| `PROJECTS_DIR` | where `gd new` creates site folders |
| `GIT_OWNER` | GitHub user or org for new repos |
| `GIT_PRIVATE` | `true` / `false` for new repos |
| `DEFAULT_TYPE` | `wordpress` or `astro` |

Stored in `~/.config/gd/config`. With these set, `gd new` already knows the
folder and the GitHub repo name, so you just press enter.

---

## A new domain

```bash
gd new
```

Asks for the domain, short name, local folder, GitHub repo, site type, and the
SSH details. Then it creates the folder and repo, writes the config, installs
the devcontainer and the three workflows, tests SSH, offers to install your SSH
key, pulls the live files onto `main`, offers to pull the database, branches
`develop`, creates the GitHub repo and sets the deploy secrets.

Already have a folder with a `.gdsync` in it — a clone on another Mac, say?

```bash
gd adopt
```

It asks only for the connection details, which are deliberately not committed.

## Day to day

Run these from anywhere inside a site folder. No site name, ever.

```bash
gd sync       # main := the live server
gd merge      # sync, merge develop, go live   ← the normal one
gd status     # what's drifted (read-only)
gd db         # live database -> _db/, imported into the container
gd up / down  # start / stop the devcontainer
gd deploy     # manual rsync, if you'd rather not involve GitHub
gd shell      # SSH in
```

`gd sync --all`, `gd status --all` and `gd db --all` walk every registered site
and can be run from anywhere.

---

## Where configuration lives

There is no central site list. Each repo carries its own:

| file | committed? | holds |
|---|---|---|
| `.gdsync` | yes | what the site is and where it deploys — also read by the GitHub workers |
| `.gdsync.local` | no | SSH host, user and port for this machine |

`~/.config/gd/repos` is only a list of folders, so `gd list` and `--all` have
something to walk. Delete it and nothing breaks; `gd adopt` rebuilds entries.

---

## The loop

```
         live server  ──(1) sync ──▶  main  ──(2) deploy worker ──▶  live server
                                       ▲
                                       │ (3) merge, gated
                                       │
                                    develop  ◀── you
```

1. **`gd sync`** rsyncs the live server onto `main` and commits it as
   `[server-sync] …`. It also runs nightly in GitHub Actions, so `main` stays
   honest even if nobody touches the repo for a year.

2. **Push to `main` → deploy worker.** `deploy.yml` backs up the server, then
   rsyncs `main` up. Commits tagged `[server-sync]` are skipped, so a sync can't
   trigger a pointless redeploy of what just came down.

3. **`gd merge`** is the only sanctioned route into `main`: server first, then
   your branch, then push. If the server changed under you, you get a normal Git
   conflict in your IDE instead of silently flattening someone's live edit.

The same rule is enforced on GitHub for pull requests. `verify-main.yml` runs
the server sync, then fails the check if your branch doesn't contain the
server's current state — so you can't merge a PR in the browser and bypass it.

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
repo genuinely is the source — `gd sync` says so and does nothing.

---

## GitHub secrets

`gd secrets` sets `GD_SSH_KEY`, `GD_SSH_HOST`, `GD_SSH_USER`, `GD_SSH_PORT` on
the repo. Run `gd key` first so the key is authorised on the server.

Without these the workers can't reach GoDaddy, and you fall back to `gd deploy`
from your Mac, which works fine.

---

## Deliberate omissions

**No database push.** Overwriting a live WordPress database from a local copy
destroys every order, comment and form entry submitted since you pulled.
Content changes belong on the live site. `gd db` is one-directional on purpose.

**Uploads aren't tracked.** `wp-content/uploads` is excluded everywhere. It's
large, it changes through the admin, and it isn't code.

**`wp-config.php` is never touched.** It holds the live DB credentials and salts.

## Backups

Every deploy — worker or manual — tars the current server copy to
`~/gd-backups/` on the server first. The worker keeps the last 20.

```bash
gd shell
cd ~/public_html && tar xzf ~/gd-backups/deploy-20261022-143000.tar.gz
```
