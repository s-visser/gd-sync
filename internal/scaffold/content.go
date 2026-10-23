package scaffold

const gitignoreAstro = `node_modules/
dist/
.astro/
.env
.env.production
.DS_Store
.idea/
_db/

# connection details for this machine only
.gd-sync.local.toml
.gd-sync.local
`

// gitignoreScaffold is what every WordPress repo commits besides the paths
// you maintain: gd-sync's own files. Everything else in the folder is the
// local mirror of the live site.
var gitignoreScaffold = []string{
	".gitignore", ".gitattributes", ".gd-sync.toml", ".devcontainer/", ".github/", ".run/", "GD-SYNC.md", "README.md",
}

// gitignoreNever stays out of git even inside a path you maintain.
const gitignoreNever = `# Never committed, even inside a maintained path.
/wp-admin/
/wp-includes/
/wp-*.php
/index.php
/license.txt
/readme.html
/xmlrpc.php
wp-config.php
wp-content/uploads/
wp-content/cache/
wp-content/upgrade/
wp-content/backup*/
wp-content/*.log
error_log
*:Zone.Identifier
.idea/
.vscode/
.DS_Store
node_modules/
*.log
.env
_db/

# connection details for this machine only
.gd-sync.local.toml
.gd-sync.local
`

// cheatSheet is GD-SYNC.md. It is regenerated on every scaffold, so it stays
// about concepts and the handful of commands that won't churn — anything
// version-specific belongs behind "gd-sync help".
const cheatSheet = "# Working on this site\n\n" +
	"This project is managed by **gd-sync**. Run gd-sync from anywhere inside this project.\n\n" +
	"## The one rule\n\n" +
	"The live server is the source of truth. `main` mirrors the server's copy of the\n" +
	"paths you maintain (`tracked` in `.gd-sync.toml`). You work on `develop`. Never edit\n" +
	"files on the server by hand if you can help it.\n\n" +
	"The rest of the live site — WordPress core, other plugins and themes, uploads — is\n" +
	"copied into this folder too, so the site runs locally. Git ignores all of it, and\n" +
	"`gd-sync sync` keeps it current.\n\n" +
	"## A normal session\n\n" +
	"```\n" +
	"gd-sync sync        # pull the server's current state onto main and locally\n" +
	"gd-sync db          # pull the live database (WordPress sites)\n" +
	"gd-sync up          # start the local site in Docker\n" +
	"                    # ... work on develop ...\n" +
	"gd-sync merge       # sync again, merge develop, go live\n" +
	"```\n\n" +
	"Start with `gd-sync sync`. The site may have changed since you last looked —\n" +
	"a plugin update, or someone editing through the hosting panel.\n\n" +
	"## Commands\n\n" +
	"| | |\n" +
	"|---|---|\n" +
	"| `gd-sync sync` | update `main` and the local copy with what's on the server right now |\n" +
	"| `gd-sync status` | what differs between the server and `main` (changes nothing) |\n" +
	"| `gd-sync db` | download the live database into `_db/` |\n" +
	"| `gd-sync up` / `gd-sync down` | start / stop the local Docker site (from your computer) |\n" +
	"| `gd-sync merge` | the safe way to go live: sync, merge, deploy |\n" +
	"| `gd-sync deploy` | upload `main` by hand, skipping GitHub |\n" +
	"| `gd-sync shell` | SSH into the server |\n" +
	"| `gd-sync help` | everything else |\n\n" +
	"## Files in here\n\n" +
	"| | |\n" +
	"|---|---|\n" +
	"| `.gd-sync.toml` | this site's config — committed, also read by the GitHub workers |\n" +
	"| `.gitignore` | generated from `TRACKED` — run `gd-sync scaffold` after changing it |\n" +
	"| `.gd-sync.local.toml` | your SSH details — not committed |\n" +
	"| `_db/` | database dumps — not committed |\n" +
	"| `.devcontainer/` | the local Docker environment, with a local `wp-config.php` and `.htaccess` |\n" +
	"| `.github/workflows/` | the sync and deploy workers |\n\n" +
	"## What git leaves out\n\n" +
	"Before files from the server go into git, gd-sync asks about three kinds:\n\n" +
	"- **Dependency folders** (a plugin's `vendor/`): kept out of git. Every deploy\n" +
	"  rebuilds them from `main` first (`[[vendor]]` in `.gd-sync.toml`), and stops\n" +
	"  before uploading anything if the build fails.\n" +
	"- **Credential-looking files**: kept out of git (`ignore`). The server's copy is\n" +
	"  never synced or deployed, only downloaded so the site runs locally.\n" +
	"- **Large binaries**: kept in Git LFS (`lfs`), or left out like the above.\n\n" +
	"Files nobody was there to decide about — the nightly worker, or `-y` — wait\n" +
	"outside git until the next sync in a terminal.\n\n" +
	"## Inside the devcontainer\n\n" +
	"gd-sync, git and gh are installed in the container, so everything above works from\n" +
	"the IDE's terminal too, except `gd-sync up` / `down`: those run on your computer.\n" +
	"Your `~/.ssh` is mounted in, so the server connection is the same. For GitHub, run\n" +
	"once, in any gd-sync container: `gh auth login && gh auth setup-git`.\n\n" +
	"## If something looks wrong\n\n" +
	"`gd-sync status` shows the difference without touching anything.\n\n" +
	"Every deploy backs the server up first, to `~/gd-sync-backups/` on the server.\n" +
	"`gd-sync shell`, then unpack the tarball you want over the web root.\n\n" +
	"## Working on a different machine\n\n" +
	"```\n" +
	"git clone <this repo>\n" +
	"cd <folder> && gd-sync adopt\n" +
	"```\n\n" +
	"`gd-sync adopt` asks for the SSH details, since those aren't committed. Then\n" +
	"`gd-sync sync` brings in the rest of the site.\n"

// readme takes the domain, live URL, local URL and stack.
const readme = `# %s

| | |
|---|---|
| Live | %s |
| Local | %s |
| Stack | %s |
| Hosting | GoDaddy cPanel |

See [GD-SYNC.md](GD-SYNC.md) for how to pull, run and deploy this site.

## About this site

_What it's for, who owns it, anything worth knowing a year from now._

## Custom code

_What lives in the tracked paths and why._

## Gotchas

_Anything that bit you last time._
`

// runConfig takes the configuration's name and gd-sync's arguments. It runs
// gd-sync from PATH in the IDE's terminal, so it works on every platform.
const runConfig = `<component name="ProjectRunConfigurationManager">
  <configuration default="false" name="%s" type="ShConfigurationType">
    <option name="SCRIPT_TEXT" value="gd-sync %s" />
    <option name="INDEPENDENT_SCRIPT_PATH" value="true" />
    <option name="SCRIPT_PATH" value="" />
    <option name="SCRIPT_OPTIONS" value="" />
    <option name="INDEPENDENT_SCRIPT_WORKING_DIRECTORY" value="true" />
    <option name="SCRIPT_WORKING_DIRECTORY" value="$PROJECT_DIR$" />
    <option name="INDEPENDENT_INTERPRETER_PATH" value="true" />
    <option name="INTERPRETER_PATH" value="" />
    <option name="INTERPRETER_OPTIONS" value="" />
    <option name="EXECUTE_IN_TERMINAL" value="true" />
    <option name="EXECUTE_SCRIPT_FILE" value="false" />
    <envs />
    <method v="2" />
  </configuration>
</component>
`
