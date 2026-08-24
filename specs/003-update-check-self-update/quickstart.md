# Quickstart & Validation Guide: Update Check & Guided Self-Update

Purpose: prove the start-up prompt, the choices, the self-update, non-interactive silence, and hook install/removal. Contracts: `contracts/update-cli.md`, `contracts/startup-hook-and-checker.md`.

## Prerequisites

- Feature 002 implemented (installer, release artifacts, `tools/releaseserve`, stable executable path, daemon self-refresh).
- For hermetic runs: `go run ./tools/releaseserve <dist-with-fake-newer-version> --tag v9.9.9` and env `AGENTGUARD_LATEST_URL=http://127.0.0.1:<port>/releases/latest AGENTGUARD_DOWNLOAD_BASE=http://127.0.0.1:<port>/releases/download`.

## 1. Hook installed by setup

```bash
agentguard setup claude            # step "✓ Start-up update check (zsh: ~/.zshrc)"
agentguard update startup status   # lists files, blocked_by_policy on Windows
grep -c 'agentguard:update-check' ~/.zshrc   # 1
```

## 2. Prompt at terminal start (fake newer release)

```bash
agentguard update --check          # latest 9.9.9 known, prompt_due=true
zsh -i                             # new interactive shell → prompt appears
# Update now? [y]es / [N]ot now / [s]kip this version (auto "not now" in 30s):
```
Expected: exactly one prompt even if you open five terminals at once; `bash -c 'echo hi'`, `zsh -c`, `pwsh -NonInteractive`, and `agentguard … --json` print nothing about updates.

## 3. Choices

- Enter / wait 30 s → "not now": no prompt in new terminals for 24 h (`agentguard update --check` shows `deferred_until`).
- `s` → skip 9.9.9: no prompt for 9.9.9; publish 9.9.10 (fake) → prompt returns.
- `y` → update: `verified sha256 …`, binary replaced at the same path, `agentguard version` = 9.9.9 in the same and in new terminals, `agentguard daemon status --json` version = 9.9.9, `agentguard doctor` clean, approvals/history intact.

## 4. Explicit command

```bash
agentguard update --plan           # shows installed → target, channel, path, actions; changes nothing
agentguard update --yes            # updates without questions (script/manual channel)
agentguard update --version 0.1.0  # explicit target (downgrade asks for confirmation)
```

## 5. Package-manager installs

On a Homebrew install (`brew install agentguard/tap/agentguard`): `agentguard update` prints and runs `brew upgrade agentguard/tap/agentguard` instead of touching the Cellar file, then restarts the daemon. On winget: `winget upgrade --id AgentGuard.AgentGuard --exact`.

## 6. Trust checks

- Corrupt `checksums.txt` on the fake server → `agentguard update --yes` exits 3, binary untouched.
- Only a pre-release published → stable users see no prompt; `updates.channel = "prerelease"` → prompt.
- `AGENTGUARD_NO_UPDATE_CHECK=1 zsh -i` → nothing; `updates.check=false` → nothing and zero requests (fake server log empty).

## 7. Removal

```bash
cp ~/.zshrc /tmp/zshrc.before      # do this BEFORE `agentguard setup claude` in step 1
agentguard uninstall claude        # step "✓ Start-up update check removed (~/.zshrc)"
diff /tmp/zshrc.before ~/.zshrc    # no differences: the file is byte-identical outside the managed block
```

## 8. Automated

```bash
go test ./internal/updater/... -count=1
go test ./test/e2e -run 'TestUpdate' -count=1     # start-up prompt in bash/zsh/fish/pwsh, silence, latency < 50 ms, self-update, locks
```
