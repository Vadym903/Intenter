#requires -Version 5.1
<#
.SYNOPSIS
    Installs Intenter, a semantic permission layer for AI coding agents.

.DESCRIPTION
    Downloads the release build for this machine, verifies its checksum, and
    puts the binary somewhere the shell can find it. Run it again to upgrade, or
    with -Uninstall to remove it.

    No administrator rights are needed: everything is written under the current
    user's profile.

.EXAMPLE
    irm https://raw.githubusercontent.com/Vadym903/Intenter/main/install.ps1 | iex

.EXAMPLE
    & ([scriptblock]::Create((irm https://raw.githubusercontent.com/Vadym903/Intenter/main/install.ps1))) -Setup claude
#>
[CmdletBinding()]
# Write-Host is the right call here and not a lapse: this script talks to a
# person watching a terminal, and it is usually run through `iex`, where
# anything written to the pipeline becomes the caller's return value.
[Diagnostics.CodeAnalysis.SuppressMessageAttribute('PSAvoidUsingWriteHost', '')]
# -Yes is accepted and deliberately ignored, so that a habitual --yes does not
# fail; there are no prompts to answer.
[Diagnostics.CodeAnalysis.SuppressMessageAttribute('PSReviewUnusedParameter', '')]
# The helpers that change state (Update-UserPath, Remove-UserPath, Restart-Daemon
# and the legacy clean-up) are internal to an installer that never prompts and
# has -DryRun as its one "what if"; -WhatIf/-Confirm on each of them would be
# switches nothing can pass.
[Diagnostics.CodeAnalysis.SuppressMessageAttribute('PSUseShouldProcessForStateChangingFunctions', '')]
param(
    # Install a specific version instead of the latest.
    [string]$Version,
    # Install into this directory instead of %LOCALAPPDATA%\Intenter\bin.
    [string]$InstallDir,
    # Do not change the user PATH; print the instruction instead.
    [switch]$NoModifyPath,
    # Run `intenter setup <agent>` after installing.
    [string]$Setup,
    # Remove Intenter instead of installing it.
    [switch]$Uninstall,
    # With -Uninstall, also delete approvals and history.
    [switch]$Purge,
    # Print what would happen and change nothing.
    [switch]$DryRun,
    # Accepted and ignored; this script never prompts.
    [switch]$Yes,
    # Show the help text.
    [switch]$Help
)

Set-StrictMode -Version 2
$ErrorActionPreference = 'Stop'
# Windows PowerShell 5.1 redraws its progress bar for every buffer a download
# fills, which makes Invoke-WebRequest many times slower than the network; the
# download is reported in one line instead.
$ProgressPreference = 'SilentlyContinue'

# Distribution constants. These strings are identical in install.sh and in the
# README install section, so a repository move is a single search and replace.
$Repo = if ($env:INTENTER_REPO) { $env:INTENTER_REPO } else { 'Vadym903/Intenter' }
$DownloadBase = if ($env:INTENTER_DOWNLOAD_BASE) { $env:INTENTER_DOWNLOAD_BASE } else { "https://github.com/$Repo/releases/download" }
$LatestUrl = if ($env:INTENTER_LATEST_URL) { $env:INTENTER_LATEST_URL } else { "https://github.com/$Repo/releases/latest" }

# Downloads are pinned to HTTPS end to end (Save-Url below): a redirect can
# never move a transfer to plaintext, the same protection install.sh's
# CURL_PROTO gives with `--proto =https`. Pointing the installer somewhere
# else -- which the tests and the pre-publish verification job do, at a local
# server -- lifts the restriction, because by then the user has chosen the
# source themselves. Mirrors install.sh's `case "${DOWNLOAD_BASE}${LATEST_URL}"
# in *http://*)` exactly: a literal, case-sensitive substring check, not a
# scheme parse.
$AllowInsecureDownload = $DownloadBase.Contains('http://') -or $LatestUrl.Contains('http://')

# Exit codes, so a script wrapping this one can tell the cases apart.
$ExitOK = 0
$ExitUsage = 1
$ExitDownload = 2
$ExitChecksum = 3
$ExitWrite = 4
$ExitUninstallWarnings = 5
$ExitPostInstall = 6
# 8: signature verification failed (same family as 3; see internal/updater's
# ExitSignature, contracts/release-and-signing.md section 3).
$ExitSignature = 8

# The pinned release signing key (research R-05,
# contracts/release-and-signing.md section 2): the same PEM committed at the
# repository root as cosign.pub, embedded so verification does not depend on
# finding that file on the machine it is protecting.
$EmbeddedPublicKeyPem = @'
-----BEGIN PUBLIC KEY-----
MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAE+9zI6vPn9ZfPtjb4MWC1z2NcL1oB
KeWZibnfrHoQzxZttzl1kcFzmroK9jlPfn4LdCQbZVN9rAec09WtMMo+tA==
-----END PUBLIC KEY-----
'@

# Name and folder the pre-rename installer wrote to. Recognized only so an
# upgrade or -Uninstall can remove them (contracts/identity-and-rename.md
# section 2.3) -- nothing legacy is ever used or trusted. # legacy
$LegacyBinaryName = 'agentguard.exe' # legacy: pre-rename binary name

# Windows PowerShell 5.1 still defaults to TLS 1.0 on older builds, and GitHub
# refuses that. Adding 1.2 is the difference between "it just works" and an
# error message about a closed connection.
try {
    if (([Net.ServicePointManager]::SecurityProtocol -band [Net.SecurityProtocolType]::Tls12) -ne [Net.SecurityProtocolType]::Tls12) {
        [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
    }
} catch {
    Write-Verbose "could not raise the TLS version: $_"
}

function Write-Info { param([string]$Message) Write-Host $Message }

function Write-Warn { param([string]$Message) Write-Host "install.ps1: $Message" -ForegroundColor Yellow }

function Stop-WithError {
    param([int]$Code, [string]$Message)
    Write-Host "install.ps1: $Message" -ForegroundColor Red
    exit $Code
}

function Show-Usage {
    Write-Info @'
Install Intenter, a semantic permission layer for AI coding agents.

Usage:
  irm <url>/install.ps1 | iex
  & ([scriptblock]::Create((irm <url>/install.ps1))) [options]

Options:
  -Version <v>      install a specific version instead of the latest
  -InstallDir <dir> install into <dir> (default: %LOCALAPPDATA%\Intenter\bin)
  -NoModifyPath     do not change the user PATH
  -Setup claude     run `intenter setup claude` after installing
  -Uninstall        remove Intenter (hooks, service, binary, PATH entry)
  -Purge            with -Uninstall, also delete approvals and history
  -DryRun           print what would happen and change nothing
  -Yes              accepted and ignored; this script never prompts
  -Help             show this message

Environment:
  INTENTER_VERSION, INTENTER_INSTALL_DIR, INTENTER_NO_MODIFY_PATH=1,
  INTENTER_REPO, INTENTER_DOWNLOAD_BASE, INTENTER_LATEST_URL
  (a parameter always wins over the matching variable)
'@
}

# Resolve-Configuration applies the precedence: parameter, then environment,
# then default.
function Resolve-Configuration {
    if (-not $script:Version -and $env:INTENTER_VERSION) {
        $script:Version = $env:INTENTER_VERSION
    }
    if (-not $script:Version) { $script:Version = 'latest' }

    if (-not $script:InstallDir -and $env:INTENTER_INSTALL_DIR) {
        $script:InstallDir = $env:INTENTER_INSTALL_DIR
    }
    if (-not $script:InstallDir) {
        $local = $env:LOCALAPPDATA
        if (-not $local) { $local = Join-Path $env:USERPROFILE 'AppData\Local' }
        $script:InstallDir = Join-Path $local 'Intenter\bin'
    }

    if (-not $script:NoModifyPath -and $env:INTENTER_NO_MODIFY_PATH -eq '1') {
        $script:NoModifyPath = $true
    }

    if ($script:Setup -and $script:Setup -ne 'claude') {
        Stop-WithError $ExitUsage "unknown agent: $($script:Setup) (only 'claude' is supported)"
    }
}

# Get-Architecture maps the machine to a release asset architecture.
function Get-Architecture {
    $arch = $null
    try {
        $arch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()
    } catch {
        # PowerShell 5.1 on older .NET does not expose RuntimeInformation.
        $arch = $env:PROCESSOR_ARCHITECTURE
    }

    switch -Regex ($arch) {
        '^(X64|AMD64)$' { return 'amd64' }
        '^(Arm64|ARM64)$' { return 'arm64' }
        default {
            Stop-WithError $ExitUsage @"
unsupported architecture: $arch
Intenter publishes amd64 and arm64 builds. To run on $arch, build from source:
  https://github.com/$Repo#building-from-source
"@
        }
    }
}

# Get-ProxyHint names the proxy in an error when one is configured, because a
# failure through a proxy looks identical to a failure without one.
function Get-ProxyHint {
    $proxy = $env:HTTPS_PROXY
    if (-not $proxy) { $proxy = $env:https_proxy }
    if ($proxy) { return " (via proxy $proxy)" }
    return ''
}

# Invoke-Hop makes one request with redirects switched off and returns the
# response, or $null when no response came back at all. A 3xx answer is
# returned like any other, so the caller can read its Location header and
# decide for itself whether to follow it.
#
# The two shells differ in how they hand over that answer. Windows PowerShell
# 5.1 returns the 3xx response and then writes a non-terminating "maximum
# redirection count" error, which -ErrorAction SilentlyContinue drops; its
# 4xx/5xx arrive as an exception that still carries the response. pwsh 7 would
# turn every non-2xx into an exception, so -SkipHttpErrorCheck (which 5.1
# does not have) keeps them as responses. With -OutFile, -PassThru makes the
# response come back as well as the file.
function Invoke-Hop {
    param([string]$Url, [string]$OutFile)

    $request = @{
        Uri                = $Url
        MaximumRedirection = 0
        UseBasicParsing    = $true
        ErrorAction        = 'SilentlyContinue'
    }
    if ($OutFile) {
        $request['OutFile'] = $OutFile
        $request['PassThru'] = $true
    }
    if ($PSVersionTable.PSVersion.Major -ge 7) {
        $request['SkipHttpErrorCheck'] = $true
    }

    try {
        return Invoke-WebRequest @request
    } catch {
        $response = $null
        try { $response = $_.Exception.Response } catch { Write-Verbose "no response on the error: $_" }
        if ($null -ne $response) { return $response }
        Write-Verbose "request failed: $_"
        return $null
    }
}

# Get-HeaderValue reads one header from whatever shape the headers have: the
# response objects of both shells index by name (a string in 5.1, a
# one-element collection in pwsh 7), as does the WebHeaderCollection on a 5.1
# exception; the .NET HttpResponseMessage on a pwsh 7 exception offers
# GetValues instead. Returns the first value as a string, or $null.
function Get-HeaderValue {
    param($Headers, [string]$Name)

    if ($null -eq $Headers) { return $null }
    $value = $null
    try { $value = $Headers[$Name] } catch { $value = $null }
    if ($null -eq $value) {
        try { $value = $Headers.GetValues($Name) } catch { $value = $null }
    }
    if ($null -eq $value) { return $null }
    $first = @($value)[0]
    if ($null -eq $first -or [string]$first -eq '') { return $null }
    return [string]$first
}

# Resolve-Version returns the version to install, without a leading v.
#
# "latest" is resolved by reading the redirect from the releases page rather
# than by calling the GitHub API, which is rate-limited per IP and would fail
# for exactly the users behind a large corporate NAT.
function Resolve-Version {
    if ($script:Version -ne 'latest') {
        return $script:Version.TrimStart('v')
    }

    $location = $null
    $response = Invoke-Hop -Url $LatestUrl
    if ($response) {
        $location = Get-HeaderValue -Headers $response.Headers -Name 'Location'
    }

    if (-not $location -or $location -notmatch '/tag/(?<tag>[^/]+)$') {
        Stop-WithError $ExitDownload @"
cannot determine the latest release from $LatestUrl$(Get-ProxyHint)
Install a specific version instead:
  & ([scriptblock]::Create((irm <url>/install.ps1))) -Version X.Y.Z
"@
    }
    return $Matches['tag'].TrimStart('v')
}

# Save-Url downloads $Url to $Destination, following any redirect by hand so a
# hop can never move the transfer to plaintext: install.sh's `curl --proto
# =https` refuses to follow a redirect to an http:// URL, and this does the
# same by checking each hop's scheme itself rather than trusting
# Invoke-WebRequest's default (which follows a redirect to any scheme). Relaxed
# only when $AllowInsecureDownload is set, i.e. the configured download
# source is itself http:// -- the same escape hatch install.sh's CURL_PROTO
# uses for a locally hosted test server.
function Save-Url {
    param([string]$Url, [string]$Destination)

    $current = $Url
    for ($hop = 0; $hop -lt 10; $hop++) {
        if (-not $AllowInsecureDownload -and $current -notmatch '^https://') {
            Stop-WithError $ExitDownload "refusing a plaintext download: $current$(Get-ProxyHint)"
        }

        # -OutFile writes the raw response bytes directly, which -- unlike
        # reading .Content and rewriting it -- cannot corrupt a binary
        # download through a text encoding round trip. A redirect's body lands
        # in the file too, and the next hop overwrites it.
        $response = Invoke-Hop -Url $current -OutFile $Destination
        if (-not $response) {
            Stop-WithError $ExitDownload "download failed: $Url$(Get-ProxyHint)"
        }

        $status = [int]$response.StatusCode
        if ($status -ge 200 -and $status -lt 300) { return }

        $location = $null
        if ($status -ge 300 -and $status -lt 400) {
            $location = Get-HeaderValue -Headers $response.Headers -Name 'Location'
        }
        if (-not $location) {
            Stop-WithError $ExitDownload "download failed: $Url$(Get-ProxyHint)"
        }
        try {
            $current = [Uri]::new([Uri]$current, $location).AbsoluteUri
        } catch {
            Stop-WithError $ExitDownload "download failed: $Url$(Get-ProxyHint)"
        }
    }

    Stop-WithError $ExitDownload "download failed: too many redirects for $Url$(Get-ProxyHint)"
}

# Test-Checksum verifies one archive against the release checksums file.
function Test-Checksum {
    param([string]$Directory, [string]$Archive)

    $checksums = Join-Path $Directory 'checksums.txt'
    # Anchored on the 1-2 spaces sha256sum's text-mode output puts between the
    # hash and the filename, same as install.sh's `grep " \{1,2\}${archive}\$"`
    # -- without it, a checksums.txt line for an unrelated file whose name
    # happens to end with this archive's name would also match.
    $line = Select-String -Path $checksums -Pattern (' {1,2}' + [regex]::Escape($Archive) + '$') |
        Select-Object -First 1
    if (-not $line) {
        Stop-WithError $ExitChecksum "checksum verification failed: $Archive is not listed in checksums.txt"
    }

    $expected = ($line.Line -split '\s+')[0]
    $actual = (Get-FileHash -Algorithm SHA256 -Path (Join-Path $Directory $Archive)).Hash

    # Get-FileHash returns upper case and checksums.txt is written lower case,
    # so the comparison is deliberately case-insensitive rather than accidentally
    # always false.
    if ($actual.ToLowerInvariant() -ne $expected.ToLowerInvariant()) {
        Stop-WithError $ExitChecksum @"
checksum verification failed for $Archive
The download does not match the checksum published with the release. Nothing was
installed. Try again; if it keeps failing, report it at
  https://github.com/$Repo/issues
"@
    }

    # The hash is printed so it can be compared with the release page by hand.
    Write-Info "verified sha256 $($expected.ToLowerInvariant())"
}

# Test-Mode reports whether the test-only overrides below apply. Same gate as
# internal/platform's EnvTestMode: a real installation must never be steerable
# by a variable left in a shell profile (audit AG-08).
function Test-Mode {
    return $env:INTENTER_TEST_MODE -eq '1'
}

# Get-SigningKeyPem returns the PEM this run verifies checksums.txt.sig
# against: the test override, only when INTENTER_TEST_MODE=1, else the
# embedded release key.
function Get-SigningKeyPem {
    if ((Test-Mode) -and $env:INTENTER_SIGNING_KEY_FILE) {
        return Get-Content -LiteralPath $env:INTENTER_SIGNING_KEY_FILE -Raw
    }
    return $EmbeddedPublicKeyPem
}

# Write-SignatureNotice is the one line printed when neither verifier is
# available: the download was still checksum-verified, but provenance was not
# confirmed. Written directly to the process stderr stream so its wording is
# exact, rather than through Write-Warning's "WARNING: " prefix. This is the
# common case for a Windows PowerShell 5.1 user without cosign installed, so
# it falls back to Write-Host rather than letting a host with no real console
# (PowerShell ISE, some restricted hosts) turn a successful install into a
# crash here.
function Write-SignatureNotice {
    $message = "intenter: signature not verified (no cosign or openssl on PATH); the download was checksum-verified. See https://github.com/$Repo/blob/main/docs/install.md#verifying-a-download-by-hand"
    try {
        [Console]::Error.WriteLine($message)
    } catch {
        Write-Host $message
    }
}

# Convert-PemToDer strips the PEM header/footer and decodes the base64 body.
function Convert-PemToDer {
    param([string]$Pem)
    $body = ($Pem -split "`r?`n" | Where-Object { $_ -and $_ -notmatch '-----(BEGIN|END) PUBLIC KEY-----' }) -join ''
    return [Convert]::FromBase64String($body)
}

# ConvertTo-P256PublicKey reads the P-256 public key out of a PEM
# SubjectPublicKeyInfo as the ECParameters .NET imports from. .NET's own
# ImportSubjectPublicKeyInfo takes a ReadOnlySpan, which PowerShell cannot
# pass, so the fixed 91-byte layout of a P-256 key is read directly: a
# constant 27-byte header (the SEQUENCE, the ecPublicKey and prime256v1
# object identifiers, the BIT STRING and the uncompressed-point marker 0x04),
# then X and Y, 32 bytes each.
function ConvertTo-P256PublicKey {
    param([string]$Pem)

    $der = Convert-PemToDer -Pem $Pem
    $header = [byte[]](
        0x30, 0x59, 0x30, 0x13, 0x06, 0x07, 0x2A, 0x86, 0x48, 0xCE, 0x3D, 0x02, 0x01,
        0x06, 0x08, 0x2A, 0x86, 0x48, 0xCE, 0x3D, 0x03, 0x01, 0x07, 0x03, 0x42, 0x00, 0x04)
    if ($der.Length -ne 91) { throw 'the signing key is not a P-256 public key' }
    for ($i = 0; $i -lt $header.Length; $i++) {
        if ($der[$i] -ne $header[$i]) { throw 'the signing key is not a P-256 public key' }
    }

    $point = [System.Security.Cryptography.ECPoint]::new()
    $point.X = [byte[]]$der[27..58]
    $point.Y = [byte[]]$der[59..90]
    $parameters = [System.Security.Cryptography.ECParameters]::new()
    $parameters.Curve = [System.Security.Cryptography.ECCurve+NamedCurves]::nistP256
    $parameters.Q = $point
    return $parameters
}

# ConvertFrom-DerSignature turns the ASN.1 DER signature cosign and openssl
# write -- a SEQUENCE of the two INTEGERs r and s -- into the fixed-width
# r||s form .NET's VerifyData takes, 32 bytes each for P-256.
function ConvertFrom-DerSignature {
    param([byte[]]$Der)

    $invalid = 'checksums.txt.sig is not a DER-encoded ECDSA signature'
    if ($Der.Length -lt 8 -or $Der[0] -ne 0x30) { throw $invalid }
    $pos = 2
    if (($Der[1] -band 0x80) -ne 0) { $pos = 2 + ($Der[1] -band 0x7F) }

    $out = [byte[]]::new(64)
    for ($i = 0; $i -lt 2; $i++) {
        if ($pos + 2 -gt $Der.Length -or $Der[$pos] -ne 0x02) { throw $invalid }
        $length = [int]$Der[$pos + 1]
        $start = $pos + 2
        if ($length -lt 1 -or $start + $length -gt $Der.Length) { throw $invalid }
        # An INTEGER is signed, so a value with its high bit set carries a
        # leading zero byte; the fixed-width form has none.
        $skip = 0
        while ($skip -lt $length - 1 -and $Der[$start + $skip] -eq 0) { $skip++ }
        $width = $length - $skip
        if ($width -gt 32) { throw $invalid }
        [Array]::Copy($Der, $start + $skip, $out, $i * 32 + (32 - $width), $width)
        $pos = $start + $length
    }
    return , $out
}

# Get-DotNetVerifier returns an ECDsa holding the pinned public key, or $null
# where this runtime cannot build one: ECParameters arrived with .NET
# Framework 4.7, and importing a named curve needs Windows 10 or later. On
# such a machine the script falls back to cosign or the notice rather than
# calling a release tampered with.
function Get-DotNetVerifier {
    param([string]$KeyPem)

    # pwsh resolves a type only out of an assembly that is already loaded, and
    # the algorithms assembly (a forwarder on .NET 7 and later) may not be yet.
    # Windows PowerShell keeps these types in System.Core, always loaded.
    if ($PSVersionTable.PSVersion.Major -ge 7) {
        try {
            Add-Type -AssemblyName System.Security.Cryptography.Algorithms -ErrorAction Stop
        } catch {
            Write-Verbose "cryptography assembly: $_"
        }
    }

    try {
        if ($null -eq ('System.Security.Cryptography.ECParameters' -as [type])) { return $null }
        return [System.Security.Cryptography.ECDsa]::Create((ConvertTo-P256PublicKey -Pem $KeyPem))
    } catch {
        Write-Verbose "dotnet verifier: $_"
        return $null
    }
}

# Test-SignatureDotNet verifies checksums.txt.sig with .NET's ECDsa, the
# equivalent of `openssl dgst -verify` (research R-05): an ECDSA P-256
# signature over SHA-256, the format cosign and openssl use. It runs on
# Windows PowerShell 5.1 as well as pwsh 7, so a machine without cosign still
# gets a real verification. Returns $false rather than throwing, so a
# malformed signature is refused rather than crashing the installer.
function Test-SignatureDotNet {
    param($Verifier, [string]$Checksums, [string]$SignaturePath)
    try {
        $data = [System.IO.File]::ReadAllBytes($Checksums)
        $der = [Convert]::FromBase64String((Get-Content -LiteralPath $SignaturePath -Raw).Trim())
        $signature = ConvertFrom-DerSignature -Der $der
        return $Verifier.VerifyData($data, $signature, [System.Security.Cryptography.HashAlgorithmName]::SHA256)
    } catch {
        Write-Verbose "signature verification: $_"
        return $false
    }
}

# Test-Signature checks checksums.txt.sig against the pinned release key:
# .NET's ECDsa first (it checks exactly what the updater checks and works
# offline), else cosign if it is on PATH, else the one-line notice above.
# cosign's transparency-log lookup is switched off because the pinned key is
# the trust anchor here and the lookup would need the network; the signature
# itself is still fully verified. A failed verification with a verifier
# present is fatal -- nothing is installed from a release whose checksums
# cannot be trusted (research R-05, contracts/release-and-signing.md section 3).
function Test-Signature {
    param([string]$Directory)

    $checksums = Join-Path $Directory 'checksums.txt'
    $sigPath = Join-Path $Directory 'checksums.txt.sig'
    $keyPem = Get-SigningKeyPem

    # Test-only: exercises the no-verifier notice without manipulating PATH.
    if ((Test-Mode) -and $env:INTENTER_TEST_NO_VERIFIER -eq '1') {
        Write-SignatureNotice
        return
    }

    $failureMessage = @"
signature verification failed for checksums.txt
Nothing was installed; the release may have been tampered with. Report it at
  https://github.com/$Repo/issues
"@

    $verifier = Get-DotNetVerifier -KeyPem $keyPem
    if ($null -ne $verifier) {
        if (Test-SignatureDotNet -Verifier $verifier -Checksums $checksums -SignaturePath $sigPath) {
            Write-Info 'verified signature (dotnet)'
            return
        }
        Stop-WithError $ExitSignature $failureMessage
    }

    $cosign = Get-Command cosign -ErrorAction SilentlyContinue
    if ($cosign) {
        $keyFile = Join-Path $Directory 'cosign.pub'
        Set-Content -LiteralPath $keyFile -Value $keyPem -NoNewline
        $verify = @('verify-blob', '--key', $keyFile, '--signature', $sigPath, '--insecure-ignore-tlog=true', $checksums)
        if ((Invoke-Native -Command $cosign.Source -Arguments $verify) -ne 0) {
            Stop-WithError $ExitSignature $failureMessage
        }
        Write-Info 'verified signature (cosign)'
        return
    }

    Write-SignatureNotice
}

# Invoke-Native runs a program with its output discarded and returns its exit
# code. Windows PowerShell 5.1 turns a native command's stderr into error
# records when the stream is redirected, and under $ErrorActionPreference =
# 'Stop' the first line of stderr then ends the script; the preference is
# lowered for the call, so the exit code, not the chatter, decides.
function Invoke-Native {
    param([string]$Command, [string[]]$Arguments)

    $ErrorActionPreference = 'Continue'
    & $Command @Arguments *> $null
    return $LASTEXITCODE
}

# Invoke-Binary runs the installed binary with its output shown to the user
# and returns its exit code; the error preference is lowered for the same
# reason as in Invoke-Native. The output goes straight to the host rather
# than through the pipeline, where it would become the return value.
function Invoke-Binary {
    param([string]$Command, [string[]]$Arguments)

    $ErrorActionPreference = 'Continue'
    & $Command @Arguments | Out-Host
    return $LASTEXITCODE
}

# Get-InstalledVersion reports the version of an existing install, or $null.
function Get-InstalledVersion {
    param([string]$Binary)
    if (-not (Test-Path -LiteralPath $Binary)) { return $null }
    # See Invoke-Native: a line on stderr must not turn into an error here.
    $ErrorActionPreference = 'Continue'
    try {
        $first = (& $Binary version 2>$null | Select-Object -First 1)
        if (-not $first) { return $null }
        $fields = $first -split '\s+'
        if ($fields.Count -lt 2) { return $null }
        return $fields[1]
    } catch {
        return $null
    }
}

# Update-UserPath adds the install directory to the user's PATH, once.
function Update-UserPath {
    param([string]$Directory)

    $current = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (-not $current) { $current = '' }

    $entries = $current -split ';' | Where-Object { $_ -ne '' }
    foreach ($entry in $entries) {
        if ($entry.TrimEnd('\') -ieq $Directory.TrimEnd('\')) { return $false }
    }

    $updated = if ($current.TrimEnd(';')) { $current.TrimEnd(';') + ';' + $Directory } else { $Directory }
    [Environment]::SetEnvironmentVariable('Path', $updated, 'User')

    # Already-open programs read PATH once at startup; the broadcast is what
    # makes a newly opened terminal see it without a sign-out.
    Publish-EnvironmentChange
    $env:Path = $env:Path + ';' + $Directory
    return $true
}

# Remove-UserPath removes exactly the entry the installer added.
function Remove-UserPath {
    param([string]$Directory)

    $current = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (-not $current) { return $false }

    $kept = $current -split ';' | Where-Object {
        $_ -ne '' -and $_.TrimEnd('\') -ine $Directory.TrimEnd('\')
    }
    $updated = $kept -join ';'
    if ($updated -eq $current) { return $false }

    [Environment]::SetEnvironmentVariable('Path', $updated, 'User')
    Publish-EnvironmentChange
    return $true
}

# Get-LegacyInstallDir is where the pre-rename installer put the binary,
# computed the same way $script:InstallDir's default is. # legacy: pre-rename install dir
function Get-LegacyInstallDir {
    $local = $env:LOCALAPPDATA
    if (-not $local) { $local = Join-Path $env:USERPROFILE 'AppData\Local' }
    return Join-Path $local 'AgentGuard\bin' # legacy: pre-rename install dir
}

# Remove-LegacyBinary deletes a pre-rename binary and its containing
# directory, if that leaves the directory empty. Safe to call whether or not
# either is present. # legacy: agentguard binary cleanup
function Remove-LegacyBinary {
    $legacyDir = Get-LegacyInstallDir
    $legacyBinary = Join-Path $legacyDir $LegacyBinaryName

    if (Test-Path -LiteralPath $legacyBinary) {
        Remove-Item -LiteralPath $legacyBinary -Force -ErrorAction SilentlyContinue
        Write-Info "  removed the legacy agentguard binary at $legacyBinary"
    }
    if ((Test-Path -LiteralPath $legacyDir) -and
        -not (Get-ChildItem -LiteralPath $legacyDir -Force -ErrorAction SilentlyContinue)) {
        Remove-Item -LiteralPath $legacyDir -Force -ErrorAction SilentlyContinue
    }
}

# Remove-LegacyUserPath drops the pre-rename install directory from the user
# Path, if present. install.ps1 manages no startup/profile entry, so there is
# none to remove here. # legacy: agentguard PATH entry cleanup
function Remove-LegacyUserPath {
    if (Remove-UserPath -Directory (Get-LegacyInstallDir)) {
        Write-Info "  removed the legacy agentguard entry from your PATH"
    }
}

# Publish-EnvironmentChange tells running programs that the environment changed.
function Publish-EnvironmentChange {
    if (-not ('Intenter.Native' -as [type])) {
        Add-Type -Namespace 'Intenter' -Name 'Native' -MemberDefinition @'
[DllImport("user32.dll", SetLastError = true, CharSet = CharSet.Auto)]
public static extern IntPtr SendMessageTimeout(
    IntPtr hWnd, uint Msg, UIntPtr wParam, string lParam,
    uint fuFlags, uint uTimeout, out UIntPtr lpdwResult);
'@
    }
    $HWND_BROADCAST = [IntPtr]0xffff
    $WM_SETTINGCHANGE = 0x1a
    $SMTO_ABORTIFHUNG = 0x2
    $result = [UIntPtr]::Zero
    [void][Intenter.Native]::SendMessageTimeout(
        $HWND_BROADCAST, $WM_SETTINGCHANGE, [UIntPtr]::Zero, 'Environment',
        $SMTO_ABORTIFHUNG, 5000, [ref]$result)
}

# Restart-Daemon picks up the new binary when a daemon is already registered.
function Restart-Daemon {
    param([string]$Binary)

    if ((Invoke-Native -Command $Binary -Arguments @('daemon', 'status')) -ne 0) { return $true }

    if ((Invoke-Native -Command $Binary -Arguments @('daemon', 'restart')) -eq 0) {
        Write-Info '  restarted the Intenter daemon'
        return $true
    }
    Write-Warn "could not restart the daemon; run this yourself:`n  $Binary daemon restart"
    return $false
}

# Assert-SafeArchive refuses to extract an archive whose entries could write
# outside the destination directory: the .NET extractor Expand-Archive uses
# is patched against this on a modern runtime, but the check is explicit here
# rather than assumed, matching install.sh's `tar -xzf ... intenter` (which
# only ever asks for one named member) and the traversal-safe extraction
# internal/updater/download.go already does for the same archive family
# (SECURITY_AUDIT.md section 3).
function Assert-SafeArchive {
    param([string]$ZipPath)

    if (-not ('System.IO.Compression.ZipArchive' -as [type])) {
        Add-Type -AssemblyName System.IO.Compression
    }

    # ZipArchive owns the stream it is given (leaveOpen defaults to $false), so
    # disposing it is enough to close the file too.
    $zip = [System.IO.Compression.ZipArchive]::new([System.IO.File]::OpenRead($ZipPath), [System.IO.Compression.ZipArchiveMode]::Read)
    try {
        foreach ($entry in $zip.Entries) {
            $name = $entry.FullName
            $segments = $name -split '[\\/]'
            $unsafe = $name.StartsWith('/') -or $name.StartsWith('\') -or
                ($name.Length -ge 2 -and $name[1] -eq ':') -or
                ($segments | Where-Object { $_ -eq '..' })
            if ($unsafe) {
                Stop-WithError $ExitDownload "the release archive contains an unsafe path: $name"
            }
        }
    } finally {
        $zip.Dispose()
    }
}

function Invoke-Install {
    $arch = Get-Architecture
    $version = Resolve-Version
    $archive = "intenter_${version}_windows_${arch}.zip"
    $base = "$DownloadBase/v$version"
    $target = Join-Path $script:InstallDir 'intenter.exe'
    $previous = Get-InstalledVersion -Binary $target

    if ($script:DryRun) {
        Write-Info "Would install Intenter $version (windows_$arch)"
        Write-Info "  from $base/$archive"
        Write-Info "  to   $target"
        if ($previous) { Write-Info "  replacing $previous" }
        return
    }

    if ($previous -and $previous -eq $version) {
        Write-Info "Intenter $version is already installed at $target"
        return
    }

    $temp = Join-Path ([IO.Path]::GetTempPath()) ("intenter-" + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $temp -Force | Out-Null
    try {
        Write-Info "Downloading Intenter $version (windows_$arch)"
        Save-Url -Url "$base/$archive" -Destination (Join-Path $temp $archive)
        Save-Url -Url "$base/checksums.txt" -Destination (Join-Path $temp 'checksums.txt')
        Save-Url -Url "$base/checksums.txt.sig" -Destination (Join-Path $temp 'checksums.txt.sig')
        Test-Signature -Directory $temp
        Test-Checksum -Directory $temp -Archive $archive

        $unpacked = Join-Path $temp 'unpacked'
        $archivePath = Join-Path $temp $archive
        Assert-SafeArchive -ZipPath $archivePath
        Expand-Archive -LiteralPath $archivePath -DestinationPath $unpacked -Force
        $source = Join-Path $unpacked 'intenter.exe'
        if (-not (Test-Path -LiteralPath $source)) {
            Stop-WithError $ExitDownload 'the release archive does not contain intenter.exe'
        }

        try {
            New-Item -ItemType Directory -Path $script:InstallDir -Force | Out-Null
        } catch {
            Stop-WithError $ExitWrite "cannot create $($script:InstallDir)"
        }

        # Staged and renamed rather than written in place, so a half-written
        # binary is never left where the shell would find it. A running daemon
        # holds the old file open, which is why the old one is moved aside
        # rather than overwritten.
        $staged = "$target.new"
        try {
            Copy-Item -LiteralPath $source -Destination $staged -Force
            if (Test-Path -LiteralPath $target) {
                Move-Item -LiteralPath $target -Destination "$target.old" -Force
            }
            Move-Item -LiteralPath $staged -Destination $target -Force
            Remove-Item -LiteralPath "$target.old" -Force -ErrorAction SilentlyContinue
        } catch {
            Stop-WithError $ExitWrite "cannot write to $($script:InstallDir): $_"
        }

        # Windows marks downloaded files, and an unblocked binary is the
        # difference between running and a SmartScreen dialog.
        try { Unblock-File -LiteralPath $target } catch { Write-Verbose "Unblock-File: $_" }
    } finally {
        Remove-Item -LiteralPath $temp -Recurse -Force -ErrorAction SilentlyContinue
    }

    # The pre-rename binary, if any, sits next to the one just installed and
    # is never used going forward. # legacy: agentguard binary cleanup on install
    Remove-LegacyBinary

    $pathChanged = $false
    if ($script:NoModifyPath) {
        Write-Info ''
        Write-Info "Add $($script:InstallDir) to your PATH:"
        Write-Info "  `$env:Path += ';$($script:InstallDir)'"
    } else {
        # legacy: agentguard PATH entry cleanup on install
        Remove-LegacyUserPath
        $pathChanged = Update-UserPath -Directory $script:InstallDir
        if ($pathChanged) {
            Write-Info "  added $($script:InstallDir) to your PATH"
        }
    }

    $failed = 0
    if (-not (Restart-Daemon -Binary $target)) { $failed = $ExitPostInstall }

    if ($script:Setup) {
        Write-Info ''
        # Somebody who declined an edit to their PATH did not ask for a
        # different edit to their PowerShell profile.
        $integration = @('setup', $script:Setup)
        if ($script:NoModifyPath) { $integration += '--no-startup-check' }
        if ((Invoke-Binary -Command $target -Arguments $integration) -ne 0) { $failed = $ExitPostInstall }
    }

    Write-Info ''
    if ($previous) {
        Write-Info "Intenter $version installed to $target (upgraded from $previous)"
    } else {
        Write-Info "Intenter $version installed to $target"
    }
    if (-not $script:Setup) {
        Write-Info 'Next step: intenter setup claude'
    } elseif ($script:NoModifyPath) {
        Write-Info 'To be told about new releases when you open a terminal:'
        Write-Info '  intenter update startup enable'
    }
    if ($pathChanged) {
        Write-Info 'Open a new terminal to pick up PATH changes.'
    }

    if ($failed -ne 0) { exit $failed }
}

function Invoke-Uninstall {
    $target = Join-Path $script:InstallDir 'intenter.exe'

    if (-not (Test-Path -LiteralPath $target)) {
        Write-Info "Intenter is not installed in $($script:InstallDir); nothing to remove."
        Remove-LegacyBinary
        [void](Remove-UserPath -Directory $script:InstallDir)
        Remove-LegacyUserPath
        return
    }

    if ($script:DryRun) {
        Write-Info "Would remove Intenter from $target"
        Write-Info '  and its Claude Code hooks and background service'
        if ($script:Purge) { Write-Info '  and delete approvals and history' }
        return
    }

    $warnings = 0
    Write-Info 'Removing Intenter'

    # The binary removes its own integration, so hooks and the service go
    # through the same code that installed them.
    $removal = @('uninstall', 'claude')
    if ($script:Purge) { $removal += '--purge' }
    if ((Invoke-Binary -Command $target -Arguments $removal) -ne 0) {
        $warnings = 1
        Write-Warn 'could not fully remove the Claude Code integration; continuing'
    }

    Remove-Item -LiteralPath $target -Force -ErrorAction SilentlyContinue
    if (-not (Test-Path -LiteralPath $target)) {
        Write-Info "  removed $target"
    }
    Remove-LegacyBinary
    if (Remove-UserPath -Directory $script:InstallDir) {
        Write-Info "  removed $($script:InstallDir) from your PATH"
    }
    Remove-LegacyUserPath

    Write-Info ''
    if ($script:Purge) {
        Write-Info 'Intenter and its data have been removed.'
    } else {
        Write-Info 'Intenter has been removed. Your approvals and history are kept;'
        Write-Info 're-run the installer with -Uninstall -Purge to delete them too.'
    }

    if ($warnings -ne 0) { exit $ExitUninstallWarnings }
}

if ($Help) {
    Show-Usage
    exit 0
}

Resolve-Configuration

if ($Uninstall) {
    Invoke-Uninstall
} else {
    Invoke-Install
}

# Success is stated, not implied. Every failure path above exits with its own
# documented code; falling off the end instead would leave $LASTEXITCODE at
# whatever the last native command set — and the install path deliberately runs
# `intenter daemon status`, which returns non-zero on a machine that has no
# daemon yet, which is the normal case on a first install. A caller checking
# $LASTEXITCODE, which docs/install.md invites, would then read a clean install
# as a failure.
exit $ExitOK
