package updater

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/Vadym903/Intenter/internal/config"
	"github.com/Vadym903/Intenter/internal/platform"
	"github.com/Vadym903/Intenter/internal/version"
)

// Environment overrides that point release discovery and downloads somewhere
// else. They are the same names the installers use, so a machine that installs
// from a mirror also checks against it.
const (
	EnvRepo         = "INTENTER_REPO"
	EnvLatestURL    = "INTENTER_LATEST_URL"
	EnvAtomURL      = "INTENTER_RELEASES_ATOM_URL"
	EnvDownloadBase = "INTENTER_DOWNLOAD_BASE"
)

// DefaultRepo is where releases are published.
const DefaultRepo = "Vadym903/Intenter"

// CheckTimeout bounds a whole check. Nothing a user does waits for this, but it
// still has to end: a captive portal that accepts connections and never answers
// would otherwise leave a background process alive forever.
const CheckTimeout = 5 * time.Second

// atomLimit bounds how much of a feed is read. The answer is in the first
// entry; a feed larger than this is not one of ours.
const atomLimit = 1 << 20

// Sources are the URLs release discovery and downloads use.
type Sources struct {
	Repo         string
	LatestURL    string
	AtomURL      string
	DownloadBase string
	// Overridden is true when any URL came from the environment. Plain HTTP is
	// allowed only then, so a local test server works while the real check
	// stays HTTPS-only.
	Overridden bool
}

// SourcesFromEnv builds the sources for this machine.
//
// The URL and repository overrides are honored only under the test harness
// (INTENTER_TEST_MODE=1). A real installation always fetches from the pinned
// GitHub release URLs over HTTPS: since releases are verified by checksum rather
// than by signature, and the checksums file travels the same channel as the
// archive, an attacker who could point the updater elsewhere — through a stray
// variable in a shell profile — could serve a matching archive and have the
// tool replace itself with an attacker binary. Gating the overrides on test
// mode matches how the TTY and clock overrides are already fenced off
// (testmode.go).
func SourcesFromEnv() Sources {
	repo := DefaultRepo
	if platform.TestMode() {
		repo = envOr(EnvRepo, DefaultRepo)
	}
	sources := Sources{
		Repo:         repo,
		LatestURL:    "https://github.com/" + repo + "/releases/latest",
		AtomURL:      "https://github.com/" + repo + "/releases.atom",
		DownloadBase: "https://github.com/" + repo + "/releases/download",
	}
	if !platform.TestMode() {
		return sources
	}
	for _, override := range []struct {
		env    string
		target *string
	}{
		{EnvLatestURL, &sources.LatestURL},
		{EnvAtomURL, &sources.AtomURL},
		{EnvDownloadBase, &sources.DownloadBase},
	} {
		if value := strings.TrimSpace(os.Getenv(override.env)); value != "" {
			*override.target = strings.TrimRight(value, "/")
			sources.Overridden = true
		}
	}
	if os.Getenv(EnvRepo) != "" {
		sources.Overridden = true
	}
	return sources
}

// NotesURL is the release page for a version. When the sources were pointed
// somewhere else, the link points there too: a mirror's release notes are the
// ones that describe what a mirror will install.
func (s Sources) NotesURL(v string) string {
	base := "https://github.com/" + s.Repo
	if s.Overridden {
		base = strings.TrimSuffix(s.LatestURL, "/releases/latest")
	}
	return base + "/releases/tag/v" + Normalize(v)
}

// UserAgent identifies the checker. It carries no identifier beyond the tool
// and its version — the check has to be anonymous to be acceptable in a
// security tool (FR-002).
func UserAgent() string { return "intenter-updater/" + version.Version }

// Release is what a check found.
type Release struct {
	Version  string
	NotesURL string
}

// Checker performs one release check and records the outcome.
//
// It deliberately does not consult `updates.check`: the callers that run
// unprompted — the daemon ticker, the start-up path, the detached background
// check — are responsible for not calling it at all when the user has switched
// checking off, while `intenter update --check` is an explicit request and
// still works.
type Checker struct {
	Store   *Store
	Updates config.UpdatesConfig
	Sources Sources
	// Installed is the running version, recorded in the state file.
	Installed string
	// InstallChannel is how this copy was installed, recorded alongside it.
	InstallChannel string
	// Now is injectable so back-off and intervals can be tested.
	Now func() time.Time
	// Timeout bounds the whole check; zero means CheckTimeout.
	Timeout time.Duration
}

func (c *Checker) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Checker) timeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return CheckTimeout
}

// Check discovers the newest release for the configured channel and records the
// result. It returns the state as written, so a caller can act on it without
// reading the file again.
func (c *Checker) Check(ctx context.Context) (UpdateState, error) {
	now := c.now()
	release, err := c.discover(ctx)

	if err != nil {
		reason := shortReason(err)
		state, saveErr := c.Store.Mutate(func(s *UpdateState) {
			c.stamp(s)
			s.RecordCheckFailure(now, reason, c.Updates)
		})
		c.record(UpdateDecision{
			At: now, Event: EventCheckFailed, InstalledVersion: c.Installed,
			Channel: c.InstallChannel, Detail: reason,
		})
		if saveErr != nil {
			return state, saveErr
		}
		return state, err
	}

	state, saveErr := c.Store.Mutate(func(s *UpdateState) {
		c.stamp(s)
		s.RecordCheckOK(now, LatestKnown{
			Version:  release.Version,
			NotesURL: release.NotesURL,
			FoundAt:  now,
		}, c.Updates)
	})
	c.record(UpdateDecision{
		At: now, Event: EventCheckOK, InstalledVersion: c.Installed,
		TargetVersion: release.Version, Channel: c.InstallChannel,
	})
	return state, saveErr
}

// stamp records who wrote the state, so `doctor` can tell a state file written
// by a different installation from a stale one.
func (c *Checker) stamp(s *UpdateState) {
	if c.Installed != "" {
		s.InstalledVersion = c.Installed
	}
	if c.InstallChannel != "" {
		s.InstallChannel = c.InstallChannel
	}
}

// record appends to the decision log, ignoring a failure to do so: the check
// itself succeeded or failed on its own terms, and a full disk must not turn
// one into the other.
func (c *Checker) record(decision UpdateDecision) {
	_ = c.Store.Append(decision)
}

// discover asks the release host which version is newest for the channel.
func (c *Checker) discover(ctx context.Context) (Release, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout())
	defer cancel()

	if c.Updates.Prerelease() {
		return c.latestFromFeed(ctx)
	}
	return c.latestFromRedirect(ctx)
}

// latestFromRedirect reads the tag out of the redirect that `/releases/latest`
// answers with. This is how the installers resolve "latest" too: it needs no
// API token, is not rate-limited, and by construction excludes pre-releases.
func (c *Checker) latestFromRedirect(ctx context.Context) (Release, error) {
	client := c.Sources.client(c.timeout(), false)
	response, err := c.get(ctx, client, c.Sources.LatestURL)
	if err != nil {
		return Release{}, err
	}
	defer drainAndClose(response)

	location := response.Header.Get("Location")
	if location == "" {
		if response.StatusCode >= 300 {
			return Release{}, fmt.Errorf("updater: %s answered %s", c.Sources.LatestURL, response.Status)
		}
		return Release{}, fmt.Errorf("updater: %s did not redirect to a release", c.Sources.LatestURL)
	}

	target, err := response.Request.URL.Parse(location)
	if err != nil {
		return Release{}, fmt.Errorf("updater: unreadable redirect %q: %w", location, err)
	}
	tag, err := ParseVersion(path.Base(target.Path))
	if err != nil {
		return Release{}, fmt.Errorf("updater: %s redirected to %s, which names no version", c.Sources.LatestURL, target)
	}
	return Release{Version: tag, NotesURL: c.notesURL(tag, target)}, nil
}

// latestFromFeed reads the newest entry of the releases feed, which — unlike
// the redirect — includes pre-releases.
func (c *Checker) latestFromFeed(ctx context.Context) (Release, error) {
	client := c.Sources.client(c.timeout(), true)
	response, err := c.get(ctx, client, c.Sources.AtomURL)
	if err != nil {
		return Release{}, err
	}
	defer drainAndClose(response)

	if response.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("updater: %s answered %s", c.Sources.AtomURL, response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, atomLimit))
	if err != nil {
		return Release{}, fmt.Errorf("updater: read %s: %w", c.Sources.AtomURL, err)
	}

	tag, err := firstFeedTag(body)
	if err != nil {
		return Release{}, err
	}
	notes, _ := url.Parse(c.Sources.NotesURL(tag))
	return Release{Version: tag, NotesURL: c.notesURL(tag, notes)}, nil
}

// notesURL prefers the release page the host actually pointed at, so a mirror
// or a test server links to itself rather than to github.com.
func (c *Checker) notesURL(tag string, target *url.URL) string {
	if c.Sources.Overridden && target != nil && target.Scheme != "" {
		return target.String()
	}
	return c.Sources.NotesURL(tag)
}

func (c *Checker) get(ctx context.Context, client *http.Client, rawURL string) (*http.Response, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("updater: unreadable release URL %q: %w", rawURL, err)
	}
	if err := c.Sources.allowScheme(parsed); err != nil {
		return nil, err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("updater: %w", err)
	}
	request.Header.Set("User-Agent", UserAgent())
	request.Header.Set("Accept", "*/*")

	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("updater: %w", err)
	}
	return response, nil
}

// client builds an HTTP client for one request family.
//
// Proxies come from the environment because a corporate machine has no other
// way to reach the release host; plain HTTP is refused unless the URLs were
// overridden, so an ordinary check cannot be pointed at an attacker's server by
// a redirect.
func (s Sources) client(timeout time.Duration, followRedirects bool) *http.Client {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ForceAttemptHTTP2:     true,
		MaxIdleConnsPerHost:   2,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
	}
	client := &http.Client{Timeout: timeout, Transport: transport}

	checkRedirect := func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("updater: too many redirects")
		}
		return s.allowScheme(req.URL)
	}
	if !followRedirects {
		checkRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	client.CheckRedirect = checkRedirect
	return client
}

// allowScheme refuses plain HTTP unless the caller pointed us somewhere else on
// purpose.
func (s Sources) allowScheme(u *url.URL) error {
	if u == nil {
		return fmt.Errorf("updater: no URL")
	}
	if u.Scheme == "https" {
		return nil
	}
	// Plain HTTP is permitted only for a source that was deliberately pointed
	// elsewhere; a real installation is HTTPS-only, redirects included. Overridden
	// is only ever set by SourcesFromEnv under INTENTER_TEST_MODE, so a normal
	// environment cannot reach this even with a stray INTENTER_DOWNLOAD_BASE.
	if s.Overridden {
		return nil
	}
	return fmt.Errorf("updater: refusing %s: release downloads must use https", u.Scheme)
}

// atomFeed is the part of GitHub's releases feed a check needs.
type atomFeed struct {
	XMLName xml.Name `xml:"feed"`
	Entries []struct {
		ID    string `xml:"id"`
		Title string `xml:"title"`
		Links []struct {
			Href string `xml:"href,attr"`
			Rel  string `xml:"rel,attr"`
		} `xml:"link"`
	} `xml:"entry"`
}

// firstFeedTag reads the newest release's tag out of a feed. The link is
// preferred over the title because a release's title is a free-text name that
// often is not a version at all.
func firstFeedTag(body []byte) (string, error) {
	var feed atomFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return "", fmt.Errorf("updater: unreadable release feed: %w", err)
	}
	if len(feed.Entries) == 0 {
		return "", fmt.Errorf("updater: the release feed lists no releases")
	}

	entry := feed.Entries[0]
	candidates := make([]string, 0, len(entry.Links)+2)
	for _, link := range entry.Links {
		if link.Href != "" {
			if parsed, err := url.Parse(link.Href); err == nil {
				candidates = append(candidates, path.Base(parsed.Path))
			}
		}
	}
	if entry.ID != "" {
		candidates = append(candidates, entry.ID[strings.LastIndexByte(entry.ID, '/')+1:])
	}
	candidates = append(candidates, strings.TrimSpace(entry.Title))

	for _, candidate := range candidates {
		if tag, err := ParseVersion(candidate); err == nil {
			return tag, nil
		}
	}
	return "", fmt.Errorf("updater: the newest release feed entry names no version")
}

// shortReason turns a transport error into something worth putting in a status
// line: users read "timeout" or "403", not a wrapped dial error.
func shortReason(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	lower := strings.ToLower(message)
	switch {
	// The transport reports a dozen different phrasings for "it did not answer
	// in time" — response headers, TLS handshake, dial, the client deadline.
	// They mean one thing to the person reading a status line.
	case strings.Contains(lower, "timeout"), strings.Contains(lower, "timed out"),
		strings.Contains(lower, "deadline exceeded"):
		return "timed out"
	case strings.Contains(lower, "no such host"):
		return "cannot resolve the release host (DNS)"
	case strings.Contains(lower, "proxyconnect"):
		return "cannot reach the proxy"
	}
	return strings.TrimPrefix(message, "updater: ")
}

func drainAndClose(response *http.Response) {
	if response == nil || response.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	response.Body.Close()
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
