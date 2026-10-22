# Working on gd-sync

`gd-sync` is a single bash script plus a folder of templates. There is no build step.

```
gd-sync                 the whole program
install.sh              symlinks gd-sync onto PATH, checks tools, runs first setup
templates/wordpress/    devcontainer copied into new WordPress sites
templates/astro/        devcontainer copied into new Astro sites
templates/github/       the three workflows copied into every new site
```

## Trying a change

`install.sh` installs a **symlink**, so edits here take effect immediately.
No reinstall needed.

Point gd-sync at a throwaway config and a throwaway registry while testing:

```bash
export GD_HOME=/tmp/gd-sync-test
gd-sync config --setup
gd-sync new
```

`GD_TEMPLATES` overrides the template folder, `GD_STATE` the SSH control-socket
directory.

## Constraints worth keeping

- **bash 3.2.** macOS still ships it. No `${var^^}`, no `mapfile`, no
  `declare -A`, no `local -n`.
- **BSD userland.** No `xargs -r`, no `readlink -f`, no `sed -i` without a
  suffix, no `grep -P`.
- **Declare loop and read variables `local`.** A leaked `t` or `p` silently
  clobbers a caller's variable — this has already caused one real bug.
- **The server is the source of truth.** Any new command should preserve that:
  nothing reaches `main` without a server sync first.

## Changing what new projects get

`write_gitignore`, `write_manifest`, `write_docs`, `write_devcontainer`,
`write_workflows` and `write_runconfigs` each own one part of a site's
scaffolding. `gd-sync scaffold` re-runs all of them in an existing site, so changes
roll out to sites that already exist.

`write_docs` regenerates `GD.md` but never overwrites a site's own `README.md`.

## Checks before committing

```bash
bash -n gd-sync && bash -n install.sh
```

There are no automated tests. The flows worth exercising by hand against a real
site: `gd-sync new`, `gd-sync sync` with server-side drift, `gd-sync merge` with a conflict,
and `gd-sync adopt` on a fresh clone.
