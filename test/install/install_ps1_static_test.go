package install

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// These checks read install.ps1's source text directly rather than running
// it, so they run on every platform (including the macOS/Linux machines that
// build this repository, where no PowerShell interpreter is available) and
// still catch a regression in properties that are otherwise only exercised by
// the Windows-only tests in install_ps1_test.go.

func readInstallPs1(t *testing.T) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(repoRoot(), "install.ps1"))
	if err != nil {
		t.Fatalf("read install.ps1: %v", err)
	}
	return string(content)
}

// AG-08 / docs/security-model.md#the-installer: "No remote code beyond the
// documented entry point" -- the only thing this script ever executes is the
// binary it just verified, and only with -Setup. "iex" / "Invoke-Expression"
// may appear in the help text that documents how a *user* invokes the script
// from outside (the leading <# ... #> block, and Show-Usage's here-string),
// never as code the script itself runs.
func TestInstallPs1NeverEvaluatesDownloadedContentItself(t *testing.T) {
	content := readInstallPs1(t)
	docRanges := documentationRanges(t, content)

	re := regexp.MustCompile(`(?i)\biex\b|Invoke-Expression`)
	for _, loc := range re.FindAllStringIndex(content, -1) {
		start := loc[0]
		if withinAny(docRanges, start) {
			continue
		}
		lineStart := strings.LastIndexByte(content[:start], '\n') + 1
		if strings.Contains(content[lineStart:start], "#") {
			continue // after a # comment marker on this line
		}
		lineEnd := strings.IndexByte(content[start:], '\n')
		if lineEnd < 0 {
			lineEnd = len(content) - start
		}
		t.Errorf("possible code execution outside the documented entry point: %q", content[lineStart:start+lineEnd])
	}
}

type byteRange struct{ start, end int }

func withinAny(ranges []byteRange, pos int) bool {
	for _, r := range ranges {
		if pos >= r.start && pos <= r.end {
			return true
		}
	}
	return false
}

// documentationRanges finds the byte ranges that are prose rather than
// executable PowerShell: the leading <# ... #> comment-based help block, and
// every @'...'@ / @"..."@ here-string (Show-Usage's usage text is one of
// these, not a # comment).
func documentationRanges(t *testing.T, content string) []byteRange {
	t.Helper()
	var ranges []byteRange

	if start := strings.Index(content, "<#"); start >= 0 {
		if end := strings.Index(content[start:], "#>"); end >= 0 {
			ranges = append(ranges, byteRange{start, start + end + len("#>")})
		} else {
			t.Fatal("could not find the closing #> of the leading help block")
		}
	}

	for _, quote := range []struct{ open, close string }{{"@'", "'@"}, {`@"`, `"@`}} {
		pos := 0
		for {
			openIdx := strings.Index(content[pos:], quote.open)
			if openIdx < 0 {
				break
			}
			start := pos + openIdx
			searchFrom := start + len(quote.open)
			closeIdx := strings.Index(content[searchFrom:], quote.close)
			if closeIdx < 0 {
				t.Fatalf("unterminated here-string starting with %q", quote.open)
			}
			end := searchFrom + closeIdx + len(quote.close)
			ranges = append(ranges, byteRange{start, end})
			pos = end
		}
	}

	if len(ranges) == 0 {
		t.Fatal("found no documentation ranges to exclude")
	}
	return ranges
}

// "No sudo, ever" (docs/security-model.md#the-installer): install.ps1's
// equivalent is never elevating itself, since everything it writes is under
// the current user's profile.
func TestInstallPs1NeverElevates(t *testing.T) {
	content := readInstallPs1(t)
	for _, unwanted := range []string{"-Verb RunAs", "-Verb 'RunAs'", "-Verb \"RunAs\"", "Start-Process -Verb"} {
		if strings.Contains(content, unwanted) {
			t.Errorf("install.ps1 must never elevate itself, found %q", unwanted)
		}
	}
}

// AG-180: downloads are pinned to HTTPS end to end (install.sh's `curl
// --proto =https`), relaxed only through the same http:// escape hatch
// install.sh's CURL_PROTO uses.
func TestInstallPs1PinsDownloadsToHttps(t *testing.T) {
	content := readInstallPs1(t)
	requireContains(t, content, "AllowInsecureDownload")
	requireContains(t, content, "refusing a plaintext download")
	requireContains(t, content, "-notmatch '^https://'")
}

// AG-181: the checksums.txt lookup is anchored on the hash/filename separator
// the same way install.sh's `grep " \{1,2\}${archive}\$"` is, so a filename
// suffix collision in an unrelated line cannot be picked up instead.
func TestInstallPs1AnchorsTheChecksumLookup(t *testing.T) {
	content := readInstallPs1(t)
	requireContains(t, content, "' {1,2}'")
}

// AG-182: the release archive's entries are validated before Expand-Archive
// ever runs, so a traversal entry cannot write outside the temp sandbox --
// matching install.sh's single-named-member `tar -xzf ... intenter` and the
// traversal-safe extraction internal/updater/download.go already does.
func TestInstallPs1ValidatesZipEntriesBeforeExtracting(t *testing.T) {
	content := readInstallPs1(t)
	requireContains(t, content, "function Assert-SafeArchive")

	assertIdx := strings.Index(content, "Assert-SafeArchive -ZipPath")
	expandIdx := strings.Index(content, "Expand-Archive -LiteralPath")
	if assertIdx < 0 || expandIdx < 0 {
		t.Fatalf("could not find both the entry check and Expand-Archive call")
	}
	if assertIdx >= expandIdx {
		t.Error("Assert-SafeArchive must run before Expand-Archive, not after")
	}
}

// AG-183: the one-line signature notice does not depend on a real console
// being attached (PowerShell ISE and some restricted hosts have none), which
// matters because it is the common path for a Windows PowerShell 5.1 user
// without cosign installed.
func TestInstallPs1SignatureNoticeFallsBackWithoutAConsole(t *testing.T) {
	content := readInstallPs1(t)
	idx := strings.Index(content, "function Write-SignatureNotice")
	if idx < 0 {
		t.Fatal("could not find Write-SignatureNotice")
	}
	end := idx + 600
	if end > len(content) {
		end = len(content)
	}
	body := content[idx:end]
	requireContains(t, body, "[Console]::Error.WriteLine")
	requireContains(t, body, "catch")
	requireContains(t, body, "Write-Host $message")
}

func requireContains(t *testing.T, haystack, want string) {
	t.Helper()
	if !strings.Contains(haystack, want) {
		t.Errorf("install.ps1 is missing %q", want)
	}
}
