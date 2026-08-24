# Configuration

Intenter works without any configuration. This page is for the cases where the
defaults are not what you want.

- [Where the file lives](#where-the-file-lives)
- [Every setting](#every-setting)
- [Environment variables](#environment-variables)
- [Where everything else lives](#where-everything-else-lives)
- [Logs](#logs)

## Where the file lives

| Platform | Path |
|---|---|
| macOS | `~/Library/Application Support/Intenter/config.toml` |
| Linux | `~/.config/intenter/config.toml` (or `$XDG_CONFIG_HOME/intenter/`) |
| Windows | `%APPDATA%\Intenter\config.toml` |

The file does not exist until you create it. `intenter doctor` prints the path
it is using, and whether it parsed.

A key Intenter does not recognise is a warning, not an error — a config file
written for a newer version still works. A value of the wrong *type* is an
error, because guessing what you meant is worse than stopping.

Restart the daemon after editing: `intenter daemon restart`.

## Every setting

```toml
[log]
level = "info"                    # debug | info | warn | error

[daemon]
lazy_start = true                 # let a hook start the daemon if it is not running
request_timeout_ms = 5000         # how long one evaluation may take

[policy]
allow_readonly_workspace = true   # the read-only baseline
protected_branches = ["main", "master"]
sensitive_paths_extra = []        # additional paths to treat as credentials

[scope]
generated_dirs_extra = []         # additional build-output directories

[claude]
settings_path = ""                # override Claude's settings.json location
hook_timeout_seconds = 10
hook_config_change = false        # also hook Claude's ConfigChange event

[audit]
store_response_summary = true     # keep a 1 KiB summary of command output

[updates]
check = true                      # look for new releases at all
check_interval = "24h"            # minimum time between background checks
remind_interval = "24h"           # quiet period after "not now"
prompt_timeout = "30s"            # unanswered prompt counts as "not now"
channel = "stable"                # stable | prerelease
startup_hook = true               # let setup add the terminal start-up check
```

### `[log]`

**`level`** — `info` by default. `debug` records every evaluation in detail,
which is what to turn on before reporting a problem. Nothing at any level logs
command output beyond the audit summary, or the contents of files.

### `[updates]`

Covered in full in [updates.md](updates.md); in short:

**`check`** — the master switch. `false` means no network request is ever made
by the update feature, no prompt is ever shown, and the terminal start-up check
returns immediately. `intenter update` still works when you ask for it.

**`check_interval`** — how stale the known-latest version may get before a
background check runs. Checks never happen on the path of a Claude hook or a
policy decision, and nothing waits for one.

**`remind_interval`** — how long "not now" (or an unanswered prompt) keeps the
terminal quiet. It is also the minimum gap between prompts, so opening twenty
tabs produces one prompt.

**`prompt_timeout`** — how long the start-up prompt waits for an answer before
treating it as "not now" and handing the terminal back.

**`channel`** — `stable` or `prerelease`. Stable installations are never offered
a pre-release.

**`startup_hook`** — whether `intenter setup claude` installs the managed
block that shows the prompt when a terminal starts. Set it to `false` to keep
your shell start-up files untouched; you can still run `intenter update`.

### `[daemon]`

**`lazy_start`** — when the hook cannot reach the daemon, it starts one and
retries. This is what makes Intenter work on a machine with no user service
manager. Turning it off means a stopped daemon stays stopped, and commands are
deferred to Claude's own flow until you start it.

**`request_timeout_ms`** — the budget for one evaluation. Resolution has its own
5-second limit, so raising this only matters on a very slow filesystem. An
evaluation that runs out of time is asked, not allowed.

### `[policy]`

**`allow_readonly_workspace`** — the baseline that lets reads inside your project
through without asking, so `git status`, `grep -r`, `ls` and `cat README.md`
never interrupt. Set it to `false` to be asked about everything; the prompts
multiply quickly, which is why it is on.

It never covers anything sensitive, anything outside the project, or anything
that writes — see [how it works](how-it-works.md#the-decision).

**`protected_branches`** — branches where a force-push or a delete forces a
prompt. Add your release branches:

```toml
[policy]
protected_branches = ["main", "master", "release/*", "production"]
```

**`sensitive_paths_extra`** — paths to treat like credentials: reading one asks,
changing one is blocked. Intenter already knows about `.env`, `~/.ssh`, cloud
credential directories, keychains and its own configuration. Add anything
specific to you:

```toml
[policy]
sensitive_paths_extra = [
  "~/work/secrets/**",
  "~/.config/company-vpn/**",
]
```

Patterns ending in `/**` match recursively.

### `[scope]`

**`generated_dirs_extra`** — directories to classify as build output rather than
source. Intenter recognises `dist/`, `build/`, `target/`, `out/`,
`node_modules/`, `.next/`, `.gradle/` and similar when a project marker is
present. Deleting generated output is a smaller thing than deleting source, and
this is how you say what counts:

```toml
[scope]
generated_dirs_extra = ["artifacts", "public/compiled"]
```

Relative to the project root.

### `[claude]`

**`settings_path`** — where Claude's `settings.json` is, when it is not
`~/.claude/settings.json`.

**`hook_timeout_seconds`** — how long Claude waits for the hook before giving up
and proceeding. Ten seconds is generous; an evaluation takes milliseconds.

**`hook_config_change`** — subscribe to Claude's `ConfigChange` event, so
Intenter notices immediately when you edit Claude's permission rules. Off by
default because the event is not present in every Claude build; turning it on
where it exists means one less stale-cache case.

### `[audit]`

**`store_response_summary`** — keep up to 1 KiB of what a command printed, so
`intenter history show` can tell you what happened when it ran. Command output
can contain anything; set this to `false` on a machine where that matters. The
decision record is unaffected either way.

## Environment variables

Mostly for tests, containers and unusual layouts. A variable always beats the
config file.

| Variable | Effect |
|---|---|
| `INTENTER_DATA_DIR` | Database, logs and backups |
| `INTENTER_CONFIG_DIR` | Where `config.toml` is looked for |
| `INTENTER_RUNTIME_DIR` | Socket and pid file |
| `INTENTER_ENDPOINT` | Connect to a daemon at a specific socket or pipe |
| `INTENTER_NO_UPDATE_CHECK` | `1` switches update checking and prompts off |
| `INTENTER_UPDATE_CHANNEL` | `stable` or `prerelease` for this shell |
| `CI` | Any value: no update prompt is shown |

`--config <path>` and `--data-dir <path>` do the same for one command.

The installer reads `INTENTER_VERSION`, `INTENTER_INSTALL_DIR`,
`INTENTER_NO_MODIFY_PATH`, `INTENTER_REPO`, `INTENTER_DOWNLOAD_BASE` and
`INTENTER_LATEST_URL` — see [install.md](install.md).

## Where everything else lives

| What | macOS | Linux | Windows |
|---|---|---|---|
| Database | `~/Library/Application Support/Intenter/intenter.db` | `~/.local/share/intenter/intenter.db` | `%LOCALAPPDATA%\Intenter\intenter.db` |
| Logs | `<data>/logs/` | `<data>/logs/` | `<data>\logs\` |
| Settings backups | `<data>/backups/` | `<data>/backups/` | `<data>\backups\` |
| Socket / pipe | `<runtime>/intenter.sock` | `<runtime>/intenter.sock` | `\\.\pipe\intenter-<hash of user and runtime dir>` |

`intenter status` prints the paths in use on your machine.

## Logs

`<data>/logs/daemon.log` and `<data>/logs/hook.log`, as JSON lines, rotated at
10 MB with a few kept.

The hook log is the one to look at when Claude behaves as though Intenter is
not there: it records every invocation, including the ones where the daemon
could not be reached.

```sh
tail -f "$(intenter status --json | grep -o '"db_path":"[^"]*"' | cut -d'"' -f4 | xargs dirname)/logs/daemon.log"
```

Or more simply, look at the directory `intenter doctor` prints.

---

Next: [troubleshooting](troubleshooting.md) · [security model](security-model.md) ·
[README](../README.md)
