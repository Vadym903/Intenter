# Installing, upgrading and removing Intenter

Intenter is a single binary. The installers download the build for your
machine, check it against the published checksums, put it on your `PATH`, and
tell you what to do next. Nothing needs administrator rights, and nothing is
written outside your user account.

If you just want the one-liner, it is in the [README](../README.md#install).
This page is for everything around it: pinning a version, upgrading, corporate
proxies, air-gapped machines, verifying a download by hand, and removing the
whole thing again.

- [macOS and Linux](#macos-and-linux)
- [Windows](#windows)
- [Package managers](#package-managers)
- [Upgrade, pin, uninstall](#upgrade-pin-uninstall)
- [Verifying a download by hand](#verifying-a-download-by-hand)
- [Proxies, custom CAs and air-gapped machines](#proxies-custom-cas-and-air-gapped-machines)
- [Unsupported platforms and building from source](#unsupported-platforms-and-building-from-source)

## macOS and Linux

```sh
curl -fsSL https://raw.githubusercontent.com/Vadym903/Intenter/main/install.sh | sh
```

The script is POSIX `sh`, so it runs under dash on Debian, BusyBox ash on Alpine
and zsh-as-sh on macOS. `wget` works too if you have no `curl`:

```sh
wget -qO- https://raw.githubusercontent.com/Vadym903/Intenter/main/install.sh | sh
```

By default this installs the latest release into `~/.local/bin`, adds that
directory to your `PATH` if it is not there already, and prints the next step
(`intenter setup claude`).

### Options

Options go after `sh -s --`, which is how a piped script receives arguments:

```sh
curl -fsSL https://raw.githubusercontent.com/Vadym903/Intenter/main/install.sh | sh -s -- --version 0.1.0 --prefix /usr/local/bin
```

| Option | Effect |
|---|---|
| `--version <v>` | Install a specific version instead of the latest. With or without a leading `v`. |
| `--prefix <dir>` | Install into `<dir>` instead of `~/.local/bin`. |
| `--no-modify-path` | Do not touch shell startup files; the script prints the `export` line for you to add yourself. |
| `--setup claude` | Run `intenter setup claude` once the binary is in place. |
| `--uninstall` | Remove Intenter. See [Upgrade, pin, uninstall](#upgrade-pin-uninstall). |
| `--purge` | With `--uninstall`, also delete approvals and history. |
| `--dry-run` | Print what would happen and change nothing. |
| `--help` | Show the built-in help. |

Every option has an environment-variable equivalent, which is easier to set in a
Dockerfile or a provisioning script: `INTENTER_VERSION`,
`INTENTER_INSTALL_DIR`, `INTENTER_NO_MODIFY_PATH=1`. A flag always beats the
matching variable.

```sh
INTENTER_VERSION=0.1.0 INTENTER_NO_MODIFY_PATH=1 sh install.sh
```

`INTENTER_REPO`, `INTENTER_DOWNLOAD_BASE` and `INTENTER_LATEST_URL` point
the installer at a different source — a mirror, or an internal artifact server.
See [air-gapped machines](#proxies-custom-cas-and-air-gapped-machines).

### PATH, and why a new terminal is needed

A running shell cannot be changed from the outside, so the `PATH` line the
installer writes only affects shells started afterwards. Open a new terminal, or
source the file it names.

The installer writes an idempotent block, marked so it can find and remove it
later, to the startup files that actually apply to you:

| Shell | File |
|---|---|
| zsh | `~/.zshrc`, plus `~/.zprofile` on macOS (a macOS login shell reads `.zprofile`) |
| bash | `~/.bashrc`, and `~/.bash_profile` or `~/.profile` when they exist |
| fish | `~/.config/fish/conf.d/intenter.fish` |

Running the installer twice does not duplicate the block. If `~/.local/bin` is
already on your `PATH`, nothing is written at all.

With `--no-modify-path` you get the line to add yourself:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

### Exit codes

Useful when the installer runs inside another script:

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | Bad usage, or an unsupported OS/architecture |
| 2 | Download or version resolution failed |
| 3 | Checksum verification failed |
| 4 | Could not write to the install directory |
| 5 | Uninstall finished, but with warnings |
| 6 | A post-install step failed (daemon restart, `setup`) |

Exit code 3 means the archive did not match the published checksum. The
installer stops before anything is written. Do not work around it — re-run, and
if it persists, report it.

### WSL

Windows Subsystem for Linux is Linux: use the `install.sh` one-liner inside the
WSL distribution, and it installs a Linux binary that guards commands run
*inside* WSL. If you also run Claude Code natively on Windows, install there
separately with `install.ps1` — the two installations keep separate approvals,
because they are separate machines as far as paths are concerned.

## Windows

PowerShell 5.1 (the one in every Windows install) or PowerShell 7. No
administrator rights needed.

```powershell
irm https://raw.githubusercontent.com/Vadym903/Intenter/main/install.ps1 | iex
```

`irm | iex` cannot pass arguments — the script is executed as a bare
expression. To use options, turn the downloaded text into a scriptblock and call
it with parameters:

```powershell
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/Vadym903/Intenter/main/install.ps1))) -Version 0.1.0
```

That form is worth a bookmark; every option below uses it.

### Options

| Option | Effect |
|---|---|
| `-Version <v>` | Install a specific version instead of the latest. |
| `-InstallDir <dir>` | Install into `<dir>` instead of `%LOCALAPPDATA%\Intenter\bin`. |
| `-NoModifyPath` | Do not change the user `PATH`; the script prints the instruction instead. |
| `-Setup claude` | Run `intenter setup claude` once the binary is in place. |
| `-Uninstall` | Remove Intenter. |
| `-Purge` | With `-Uninstall`, also delete approvals and history. |
| `-DryRun` | Print what would happen and change nothing. |
| `-Help` | Show the built-in help. |

The same `INTENTER_*` environment variables work here, and a parameter always
beats the matching variable.

### PowerShell 5.1 versus 7

Both are supported and tested. Two differences worth knowing:

- **TLS.** Older Windows PowerShell 5.1 builds still default to TLS 1.0, which
  GitHub refuses; the script enables TLS 1.2 itself before downloading, so this
  is handled. If you see an error about a closed connection on a very old build,
  run `[Net.ServicePointManager]::SecurityProtocol = 'Tls12'` first.
- **Execution policy.** `irm | iex` runs text already in memory, so the
  execution policy for script *files* does not apply. If you download
  `install.ps1` to disk and run it, an `AllSigned` or `Restricted` policy will
  block it. Use `powershell -ExecutionPolicy Bypass -File install.ps1`, or the
  `irm | iex` form.

### PATH and new terminals

The installer adds `%LOCALAPPDATA%\Intenter\bin` to the **user** `Path`
environment variable (never the machine-wide one, which needs administrator
rights). Windows only hands the updated environment to processes started after
the change, so **open a new terminal** before running `intenter`. That
includes Windows Terminal tabs, VS Code's integrated terminal, and any Claude
Code session already running.

### SmartScreen and mark-of-the-web

The release binaries are not code-signed yet. Downloading a file from the
internet tags it with the "mark of the web", and Windows may warn about running
it. Neither installer hits this in the normal path — the binary is extracted
from an archive by the script rather than launched from Explorer — but if you
download an asset manually and Windows blocks it:

```powershell
Unblock-File -Path .\intenter.exe
```

Only do this after verifying the checksum, as described in
[verifying a download by hand](#verifying-a-download-by-hand).

### arm64

Windows on arm64 (Surface Pro X and similar) gets a native arm64 build; the
installer detects it from the OS architecture rather than the process
architecture, so it does the right thing even when PowerShell itself is running
emulated under x64.

## If your Claude Code is the VS Code extension

Nothing extra is needed, but one detail surprises people. The extension bundles
its own copy of Claude Code's CLI and **does not put `claude` on your `PATH`**,
so a machine can have Claude Code and no `claude` command at all.

That is fine. Intenter's hooks go into `~/.claude/settings.json`, which the
extension and the CLI both read, so the gate works the same in the editor panel
as in a terminal. Install Intenter as above, then:

```sh
intenter setup claude
```

Setup recognizes the extension when there is no binary to find. It reports the
Claude Code version as unknown — it had nothing to ask — and says which
extension it found. Everything else is identical.

Two consequences worth knowing:

- **Restart Claude Code afterwards.** It reads its hooks when a session starts.
  If setup had to create `~/.claude/skills/`, a restart is also what lets it
  notice the new `/intenter` command.
- **The update prompt cannot appear in the panel.** It runs when a shell starts,
  and the panel is not a shell. Setup says so; use `intenter update --check`.

If you also want to run `claude` in VS Code's integrated terminal, that needs
Claude Code's own [standalone CLI install](https://code.claude.com/docs/en/vs-code),
which is separate from the extension.

## Package managers

Use these if you would rather your usual tool tracked the upgrade.

**Homebrew** (macOS and Linux):

```sh
brew install Vadym903/tap/intenter
brew upgrade intenter
```

After a Homebrew upgrade, run `intenter doctor` once. Homebrew installs into a
versioned Cellar directory and relinks the symlink in its `bin`; Intenter
writes the stable symlink path into your Claude hooks precisely so an upgrade
does not break them, and `doctor` confirms it.

**winget** (Windows) — available once the manifest is accepted upstream:

```powershell
winget install Intenter.Intenter
```

Until then, the release ships the generated manifests as
`winget-manifest.zip`, which you can install from directly:

```powershell
winget install --manifest .\manifests\a\Intenter\Intenter\<version>
```

Package-manager installs do not run `intenter setup claude` for you. Run it
once afterwards.

## Upgrade, pin, uninstall

### Upgrade

Intenter asks you about new releases itself, once, when you open a terminal —
and `intenter update` does it on demand. That is the usual way to upgrade;
[updates.md](updates.md) covers the prompt, the channels and how to turn it off.

To upgrade without it, run the same one-liner again. It replaces the binary in
place, and restarts the background daemon so the running service matches the new
CLI:

```sh
curl -fsSL https://raw.githubusercontent.com/Vadym903/Intenter/main/install.sh | sh
```

```powershell
irm https://raw.githubusercontent.com/Vadym903/Intenter/main/install.ps1 | iex
```

Your approvals and history are untouched by an upgrade. If the daemon could not
be restarted the installer says so and exits 6; `intenter doctor` will offer
the fix (`intenter daemon restart`).

Installing with `--no-modify-path` / `-NoModifyPath` also skips the terminal
update check, on the grounds that somebody who declined one edit to their shell
files did not ask for another. Add it later with `intenter update startup
enable`.

### Pin a version

Pass the version explicitly. This is the form to use in a Dockerfile, a
provisioning script, or anywhere a surprise upgrade would be unwelcome — and the
only way to install a release candidate, because `latest` resolves to final
releases only. The [releases page](https://github.com/Vadym903/Intenter/releases)
lists what is published:

```sh
curl -fsSL https://raw.githubusercontent.com/Vadym903/Intenter/main/install.sh | sh -s -- --version 0.2.0
```

```powershell
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/Vadym903/Intenter/main/install.ps1))) -Version 0.2.0
```

Downgrading works the same way — pass an older version. Approvals are forward
compatible within a minor series; going back across a schema change may make
older approvals unreadable, in which case Intenter asks again rather than
guessing.

### Uninstall

```sh
curl -fsSL https://raw.githubusercontent.com/Vadym903/Intenter/main/install.sh | sh -s -- --uninstall
```

```powershell
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/Vadym903/Intenter/main/install.ps1))) -Uninstall
```

This removes, in order: the Claude Code hooks (leaving the rest of your
`settings.json` as it was), the background service, the binary, and the `PATH`
block the installer wrote — identified by its markers, so hand-edits around it
survive.

**Your approvals and history are kept.** Reinstalling picks up where you left
off. To delete those too, add `--purge` / `-Purge`:

```sh
curl -fsSL https://raw.githubusercontent.com/Vadym903/Intenter/main/install.sh | sh -s -- --uninstall --purge
```

```powershell
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/Vadym903/Intenter/main/install.ps1))) -Uninstall -Purge
```

If you installed with Homebrew or winget, uninstall with the same tool
(`brew uninstall intenter`), but run `intenter uninstall claude` **first**,
while the binary still exists, so the hooks are removed cleanly.

An uninstall that could not finish every step exits 5 and prints what it left
behind, rather than failing silently.

## Verifying a download by hand

Every release ships `checksums.txt` (SHA-256 of every archive) and
`checksums.txt.sig`, a signature of that file made with the release key. Both
installers verify the archive against the checksums before extracting anything
and refuse to continue on a mismatch; they also verify the signature when a
verifier is available (`openssl` on macOS/Linux or .NET on PowerShell 7, else
`cosign` if installed) and otherwise print one line saying the download was
checksum-verified only. `intenter update` always verifies the signature. If you
install manually, do both.

The release public key is [`cosign.pub`](../cosign.pub) at the repository root
(ECDSA P-256, PEM). Compare the copy you download with the repository's before
trusting it.

Release assets are named by platform:

```text
intenter_<version>_darwin_amd64.tar.gz
intenter_<version>_darwin_arm64.tar.gz
intenter_<version>_linux_amd64.tar.gz
intenter_<version>_linux_arm64.tar.gz
intenter_<version>_windows_amd64.zip
intenter_<version>_windows_arm64.zip
checksums.txt
checksums.txt.sig
```

macOS and Linux:

```sh
base=https://github.com/Vadym903/Intenter/releases/download/v0.2.0
curl -fsSLO "$base/intenter_0.2.0_linux_amd64.tar.gz"
curl -fsSLO "$base/checksums.txt"
curl -fsSLO "$base/checksums.txt.sig"
curl -fsSLO https://raw.githubusercontent.com/Vadym903/Intenter/main/cosign.pub

# Signature — with openssl (the signature file is base64 of a DER ECDSA signature):
base64 -d checksums.txt.sig > checksums.txt.sig.der
openssl dgst -sha256 -verify cosign.pub -signature checksums.txt.sig.der checksums.txt
# …or with cosign (the signing events are logged to Rekor when the release is
# built; the flag skips that online lookup and checks the signature against the
# pinned key, which is what the updater does):
cosign verify-blob --key cosign.pub --signature checksums.txt.sig --insecure-ignore-tlog=true checksums.txt

# Checksum, then install:
sha256sum --ignore-missing -c checksums.txt
tar -xzf intenter_0.2.0_linux_amd64.tar.gz
install -m 0755 intenter ~/.local/bin/intenter
```

On macOS, `shasum -a 256 --ignore-missing -c checksums.txt` does the checksum
step.

Windows:

```powershell
$base = 'https://github.com/Vadym903/Intenter/releases/download/v0.2.0'
Invoke-WebRequest -Uri "$base/intenter_0.2.0_windows_amd64.zip" -OutFile intenter.zip
Invoke-WebRequest -Uri "$base/checksums.txt" -OutFile checksums.txt
Invoke-WebRequest -Uri "$base/checksums.txt.sig" -OutFile checksums.txt.sig
Invoke-WebRequest -Uri https://raw.githubusercontent.com/Vadym903/Intenter/main/cosign.pub -OutFile cosign.pub
cosign verify-blob --key cosign.pub --signature checksums.txt.sig --insecure-ignore-tlog=true checksums.txt   # if cosign is installed
(Get-FileHash .\intenter.zip -Algorithm SHA256).Hash.ToLower()
Select-String -Path .\checksums.txt -Pattern 'windows_amd64'
```

Compare the two strings, then expand the archive and place `intenter.exe`
wherever you like on your `PATH`.

## Proxies, custom CAs and air-gapped machines

### Behind a proxy

Both installers use the platform's normal HTTP client, so the standard
environment variables apply:

```sh
export HTTPS_PROXY=http://proxy.corp.example:3128
export NO_PROXY=localhost,127.0.0.1
curl -fsSL https://raw.githubusercontent.com/Vadym903/Intenter/main/install.sh | sh
```

```powershell
$env:HTTPS_PROXY = 'http://proxy.corp.example:3128'
irm https://raw.githubusercontent.com/Vadym903/Intenter/main/install.ps1 | iex
```

A blocked or misconfigured proxy shows up as a download failure (exit code 2),
typically `curl: (35)`, `Could not resolve host`, or a PowerShell message about
the underlying connection being closed. It is not a checksum error — if you see
exit code 3 instead, the proxy is rewriting content, and you should not proceed.

If your proxy terminates TLS with a company CA, the CA must be in the system
trust store; both installers use it. On Linux that usually means placing the
certificate in `/usr/local/share/ca-certificates/` and running
`update-ca-certificates`.

After installation, Intenter's only network use is the optional update check
and `intenter update` ([updates](updates.md)), which honor the same proxy
variables; decisions never touch the network. The proxy configuration matters
only while downloading.

### Air-gapped or internal mirror

Point the installer at your own artifact server. It expects the same layout as a
GitHub release — the archives, `checksums.txt` and `checksums.txt.sig` under a
per-version path:

```sh
INTENTER_DOWNLOAD_BASE=https://artifacts.corp.example/intenter \
INTENTER_VERSION=0.1.0 \
sh install.sh
```

Because you have chosen the source yourself, the installer relaxes its
HTTPS-only pin for non-GitHub bases, which is what makes a plain internal
`http://` mirror usable. Checksum and signature verification are **not**
relaxed: mirror `checksums.txt` and `checksums.txt.sig` alongside the archives.

With no server at all, download the assets on a connected machine, verify them
as above, copy them across, and place the binary on the `PATH` yourself. Then
run `intenter setup claude`, which needs no network.

## Unsupported platforms and building from source

Prebuilt binaries cover macOS and Linux on amd64 and arm64, and Windows on amd64
and arm64. Anything else — FreeBSD, 32-bit x86, riscv64, older ARM — exits with
code 1 and a pointer here.

Intenter is ordinary Go with no cgo, so it builds anywhere Go does:

```sh
git clone https://github.com/Vadym903/Intenter.git
cd intenter
make build
```

That produces `./bin/intenter`. Copy it onto your `PATH` and run
`intenter setup claude` as usual. Platform integration — the service manager
used to keep the daemon running — is implemented for launchd, systemd and
Windows services; on other systems the daemon still runs, but in unmanaged mode,
which `intenter doctor` will tell you about.

Build prerequisites and the full development workflow are in
[CONTRIBUTING.md](../CONTRIBUTING.md).

---

Next: [set up Claude Code and run the walkthrough](getting-started.md) ·
[troubleshooting](troubleshooting.md) · [back to the README](../README.md)
