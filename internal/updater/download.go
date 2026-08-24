package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Exit codes the update family reports (003 contracts/update-cli.md). They are
// defined here rather than in the CLI because the step that fails is the one
// that knows which code it is.
const (
	// ExitUsage: the command was asked for something it cannot do.
	ExitUsage = 1
	// ExitDownload: the check or the download failed.
	ExitDownload = 2
	// ExitChecksum: the archive did not match the published checksum. Nothing
	// was replaced.
	ExitChecksum = 3
	// ExitNotWritable: the install location cannot be written.
	ExitNotWritable = 4
	// ExitDelegation: the package manager's upgrade command failed.
	ExitDelegation = 5
	// ExitPostUpdate: the binary is already replaced, but a step after it
	// failed — restarting the daemon, or verifying it afterwards.
	ExitPostUpdate = 6
	// ExitInProgress: another update holds the lock.
	ExitInProgress = 7
	// ExitSignature: checksums.txt.sig was missing, undecodable, or did not
	// verify against the pinned release key — same family as ExitChecksum:
	// nothing was replaced.
	ExitSignature = 8
)

// downloadLimit bounds an archive. Releases are a few megabytes; anything
// approaching this is not one of ours, and the limit keeps a hostile or broken
// server from filling a disk.
const downloadLimit = 256 << 20

// checksumsLimit bounds the checksums file, which lists a handful of lines.
const checksumsLimit = 1 << 20

// downloadTimeout bounds fetching one file. It is longer than a check because
// this one transfers real bytes, possibly over a slow link.
const downloadTimeout = 5 * time.Minute

// Failure carries the exit code a failed step reports.
type Failure struct {
	Code int
	Err  error
}

func (f *Failure) Error() string { return f.Err.Error() }
func (f *Failure) Unwrap() error { return f.Err }

// failf builds a Failure with a formatted message.
func failf(code int, format string, args ...any) error {
	return &Failure{Code: code, Err: fmt.Errorf(format, args...)}
}

// ExitCodeFor returns the exit code an error asks for, defaulting to 1.
func ExitCodeFor(err error) int {
	if err == nil {
		return 0
	}
	var failure *Failure
	if errors.As(err, &failure) {
		return failure.Code
	}
	return ExitUsage
}

// AssetName is the release archive for a version on this machine. It matches
// what GoReleaser publishes and what install.sh downloads; a mismatch here
// would be an update that can never find its own build.
func AssetName(version string) string {
	extension := ".tar.gz"
	if runtime.GOOS == "windows" {
		extension = ".zip"
	}
	return fmt.Sprintf("intenter_%s_%s_%s%s", Normalize(version), runtime.GOOS, runtime.GOARCH, extension)
}

// Fetcher downloads and verifies one release build.
type Fetcher struct {
	Sources Sources
	// Timeout bounds each file transfer; zero means downloadTimeout.
	Timeout time.Duration
	// SanityCheck confirms the extracted binary is the version it claims to
	// be. It is injectable so the failure can be tested; the default runs the
	// binary, which is the only thing that actually proves it.
	SanityCheck func(path, want string) error
	// PublicKey overrides the key checksums.txt.sig is verified against; nil
	// uses the pinned embedded key (or, under INTENTER_TEST_MODE=1, the
	// INTENTER_SIGNING_KEY_FILE override — see signature.go).
	PublicKey *ecdsa.PublicKey
}

func (f *Fetcher) timeout() time.Duration {
	if f.Timeout > 0 {
		return f.Timeout
	}
	return downloadTimeout
}

// Fetch downloads the release archive for version into dir, verifies it against
// the published checksums, extracts the binary and confirms it runs. It returns
// the path of the verified binary.
//
// Nothing outside dir is touched: on any failure the installed copy is exactly
// as it was, which is the property the whole procedure is built around.
func (f *Fetcher) Fetch(ctx context.Context, version, dir string) (string, error) {
	version = Normalize(version)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", failf(ExitDownload, "updater: create %s: %w", dir, err)
	}

	asset := AssetName(version)
	base := f.Sources.DownloadBase + "/v" + version

	archivePath := filepath.Join(dir, asset)
	if err := f.download(ctx, base+"/"+asset, archivePath, downloadLimit); err != nil {
		return "", err
	}
	checksumsPath := filepath.Join(dir, "checksums.txt")
	if err := f.download(ctx, base+"/checksums.txt", checksumsPath, checksumsLimit); err != nil {
		return "", err
	}
	sigPath := filepath.Join(dir, "checksums.txt.sig")
	if err := f.downloadSignature(ctx, base+"/checksums.txt.sig", sigPath); err != nil {
		return "", err
	}

	// The signature is verified before the checksum is even read: a checksums
	// file is only as trustworthy as its provenance, and it travels the same
	// channel as the archive it vouches for (research R-05, audit AG-08).
	pub, err := verifyingKey(f.PublicKey)
	if err != nil {
		return "", err
	}
	if err := verifySignature(checksumsPath, sigPath, pub); err != nil {
		return "", err
	}

	if err := verifyChecksum(archivePath, checksumsPath, asset); err != nil {
		return "", err
	}

	binary := filepath.Join(dir, ExecutableName())
	if err := extractBinary(archivePath, binary); err != nil {
		return "", err
	}

	check := f.SanityCheck
	if check == nil {
		check = runVersionCheck
	}
	if err := check(binary, version); err != nil {
		return "", err
	}
	return binary, nil
}

// download fetches one URL to a file, refusing plain HTTP unless the sources
// were deliberately pointed elsewhere.
func (f *Fetcher) download(ctx context.Context, rawURL, target string, limit int64) error {
	ctx, cancel := context.WithTimeout(ctx, f.timeout())
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return failf(ExitDownload, "updater: %w", err)
	}
	if err := f.Sources.allowScheme(request.URL); err != nil {
		return &Failure{Code: ExitDownload, Err: err}
	}
	request.Header.Set("User-Agent", UserAgent())

	response, err := f.Sources.client(f.timeout(), true).Do(request)
	if err != nil {
		return failf(ExitDownload, "updater: download %s: %s", rawURL, shortReason(err))
	}
	defer drainAndClose(response)

	if response.StatusCode != http.StatusOK {
		return failf(ExitDownload, "updater: download %s: %s", rawURL, response.Status)
	}

	file, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return failf(ExitDownload, "updater: create %s: %w", target, err)
	}
	written, err := io.Copy(file, io.LimitReader(response.Body, limit+1))
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return failf(ExitDownload, "updater: download %s: %w", rawURL, err)
	}
	if written > limit {
		return failf(ExitDownload, "updater: %s is larger than %d bytes", rawURL, limit)
	}
	return nil
}

// downloadSignature fetches checksums.txt.sig, reporting any failure —
// missing, unreachable, truncated — as ExitSignature rather than the plain
// ExitDownload a network error normally gets: from here on, not having a
// trustworthy signature is exactly as fatal as having a bad one.
func (f *Fetcher) downloadSignature(ctx context.Context, rawURL, target string) error {
	err := f.download(ctx, rawURL, target, signatureLimit)
	if err == nil {
		return nil
	}
	var failure *Failure
	if errors.As(err, &failure) {
		return &Failure{Code: ExitSignature, Err: failure.Err}
	}
	return &Failure{Code: ExitSignature, Err: err}
}

// verifyChecksum is the gate everything after it depends on: an archive that
// does not match the published checksum is never opened, let alone installed.
func verifyChecksum(archivePath, checksumsPath, asset string) error {
	published, err := checksumFor(checksumsPath, asset)
	if err != nil {
		return err
	}

	file, err := os.Open(archivePath)
	if err != nil {
		return failf(ExitDownload, "updater: read %s: %w", archivePath, err)
	}
	defer file.Close()

	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return failf(ExitDownload, "updater: read %s: %w", archivePath, err)
	}

	actual := hex.EncodeToString(digest.Sum(nil))
	if !strings.EqualFold(actual, published) {
		return failf(ExitChecksum,
			"updater: checksum verification failed for %s\n  published %s\n  downloaded %s\n"+
				"nothing was installed; the download may have been corrupted or tampered with",
			asset, published, actual)
	}
	return nil
}

// checksumFor finds one asset's line in a checksums file. The format is what
// `sha256sum` writes and GoReleaser publishes: the digest, whitespace, the name.
func checksumFor(path, asset string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", failf(ExitDownload, "updater: read %s: %w", path, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) != 2 {
			continue
		}
		if strings.TrimPrefix(fields[1], "*") == asset {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", failf(ExitChecksum, "updater: %s is not listed in checksums.txt", asset)
}

// extractBinary writes just the executable out of a release archive.
//
// Only the entry whose base name is the executable is read, and it is written
// to a path this code chose — an archive cannot name where its contents land.
func extractBinary(archivePath, target string) error {
	if strings.HasSuffix(archivePath, ".zip") {
		return extractFromZip(archivePath, target)
	}
	return extractFromTarGz(archivePath, target)
}

func extractFromTarGz(archivePath, target string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return failf(ExitDownload, "updater: open %s: %w", archivePath, err)
	}
	defer file.Close()

	gz, err := gzip.NewReader(file)
	if err != nil {
		return failf(ExitDownload, "updater: %s is not a gzip archive: %w", filepath.Base(archivePath), err)
	}
	defer gz.Close()

	archive := tar.NewReader(gz)
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return failf(ExitDownload, "updater: read %s: %w", filepath.Base(archivePath), err)
		}
		if header.Typeflag != tar.TypeReg || filepath.Base(header.Name) != ExecutableName() {
			continue
		}
		return writeExtracted(target, io.LimitReader(archive, downloadLimit))
	}
	return failf(ExitDownload, "updater: %s contains no %s", filepath.Base(archivePath), ExecutableName())
}

func extractFromZip(archivePath, target string) error {
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		return failf(ExitDownload, "updater: open %s: %w", filepath.Base(archivePath), err)
	}
	defer archive.Close()

	for _, entry := range archive.File {
		if entry.FileInfo().IsDir() || filepath.Base(entry.Name) != ExecutableName() {
			continue
		}
		reader, err := entry.Open()
		if err != nil {
			return failf(ExitDownload, "updater: read %s: %w", entry.Name, err)
		}
		defer reader.Close()
		return writeExtracted(target, io.LimitReader(reader, downloadLimit))
	}
	return failf(ExitDownload, "updater: %s contains no %s", filepath.Base(archivePath), ExecutableName())
}

// writeExtracted writes the binary with the mode an executable needs.
func writeExtracted(target string, source io.Reader) error {
	file, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return failf(ExitDownload, "updater: create %s: %w", target, err)
	}
	_, err = io.Copy(file, source)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return failf(ExitDownload, "updater: extract to %s: %w", target, err)
	}
	if err := os.Chmod(target, 0o755); err != nil {
		return failf(ExitDownload, "updater: make %s executable: %w", target, err)
	}
	return nil
}

// runVersionCheck runs the downloaded binary and requires it to report the
// version the release claims.
//
// This is the last check before anything is replaced, and it is the only one
// that proves the archive holds a working build for this machine rather than a
// correctly-checksummed file for another one.
func runVersionCheck(path, want string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	command := exec.CommandContext(ctx, path, "version")
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		return failf(ExitDownload, "updater: the downloaded build did not run: %w\n%s",
			err, strings.TrimSpace(output.String()))
	}

	printed := strings.TrimSpace(output.String())
	if !strings.Contains(firstLine(printed), want) {
		return failf(ExitDownload,
			"updater: the downloaded build reports %q, not %s; nothing was installed",
			firstLine(printed), want)
	}
	return nil
}

func firstLine(s string) string {
	if index := strings.IndexByte(s, '\n'); index >= 0 {
		return strings.TrimSpace(s[:index])
	}
	return s
}
