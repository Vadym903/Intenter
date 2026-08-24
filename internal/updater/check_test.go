package updater

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Vadym903/Intenter/internal/config"
	"github.com/Vadym903/Intenter/internal/platform"
)

// requestLog records what a fake release host was asked for, so the tests can
// assert on what a check sends as well as on what it reads.
type requestLog struct {
	mu       sync.Mutex
	requests []*http.Request
}

func (l *requestLog) add(r *http.Request) {
	l.mu.Lock()
	defer l.mu.Unlock()
	clone := r.Clone(r.Context())
	l.requests = append(l.requests, clone)
}

func (l *requestLog) all() []*http.Request {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]*http.Request(nil), l.requests...)
}

// releaseHost starts a fake release host that publishes one tag.
func releaseHost(t *testing.T, tag string) (*httptest.Server, *requestLog) {
	t.Helper()
	log := &requestLog{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.add(r)
		switch r.URL.Path {
		case "/releases/latest":
			http.Redirect(w, r, "/releases/tag/"+tag, http.StatusFound)
		case "/releases.atom":
			w.Header().Set("Content-Type", "application/atom+xml")
			fmt.Fprintf(w, `<?xml version="1.0"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <entry>
    <id>tag:github.com,2008:Repository/1/%s</id>
    <title>Some release name</title>
    <link rel="alternate" href="http://%s/releases/tag/%s"/>
  </entry>
</feed>`, tag, r.Host, tag)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server, log
}

// checkerFor builds a checker pointed at a fake host.
func checkerFor(t *testing.T, store *Store, base string, cfg config.UpdatesConfig) *Checker {
	t.Helper()
	return &Checker{
		Store:   store,
		Updates: cfg,
		Sources: Sources{
			Repo:         "Vadym903/Intenter",
			LatestURL:    base + "/releases/latest",
			AtomURL:      base + "/releases.atom",
			DownloadBase: base + "/releases/download",
			Overridden:   true,
		},
		Installed:      "0.1.0",
		InstallChannel: ChannelScript,
		Now:            func() time.Time { return at(t, "2026-08-16T12:00:00Z") },
	}
}

func TestAStableCheckReadsTheVersionFromTheRedirect(t *testing.T) {
	server, log := releaseHost(t, "v0.2.0")
	store := newStore(t)
	checker := checkerFor(t, store, server.URL, updatesConfig())

	state, err := checker.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if state.LatestKnown == nil || state.LatestKnown.Version != "0.2.0" {
		t.Fatalf("latest_known = %+v, want 0.2.0", state.LatestKnown)
	}
	if !strings.HasSuffix(state.LatestKnown.NotesURL, "/releases/tag/v0.2.0") {
		t.Errorf("notes url = %q", state.LatestKnown.NotesURL)
	}
	if !state.LastCheckOK || state.CheckFailures != 0 {
		t.Errorf("a successful check must be recorded as one: %+v", state)
	}
	if state.InstalledVersion != "0.1.0" || state.InstallChannel != ChannelScript {
		t.Errorf("the check must stamp who wrote the state: %+v", state)
	}

	// Only the redirect was fetched, and only the release page followed it.
	requests := log.all()
	if len(requests) != 1 || requests[0].URL.Path != "/releases/latest" {
		t.Fatalf("requests = %v, want exactly one for /releases/latest", pathsOf(requests))
	}
}

func TestTheCheckSendsNothingIdentifying(t *testing.T) {
	// A security tool that phones home is a security tool people uninstall.
	server, log := releaseHost(t, "v0.2.0")
	checker := checkerFor(t, newStore(t), server.URL, updatesConfig())

	if _, err := checker.Check(context.Background()); err != nil {
		t.Fatalf("Check: %v", err)
	}

	requests := log.all()
	if len(requests) == 0 {
		t.Fatal("no request was made")
	}
	for _, request := range requests {
		if got := request.Header.Get("User-Agent"); got != UserAgent() {
			t.Errorf("User-Agent = %q, want %q", got, UserAgent())
		}
		if request.URL.RawQuery != "" {
			t.Errorf("%s carried a query string %q", request.URL.Path, request.URL.RawQuery)
		}
		if len(request.Cookies()) != 0 {
			t.Errorf("%s carried cookies", request.URL.Path)
		}
		for _, header := range []string{"Authorization", "Cookie", "X-Forwarded-For"} {
			if request.Header.Get(header) != "" {
				t.Errorf("%s carried a %s header", request.URL.Path, header)
			}
		}
	}
	if got := UserAgent(); !strings.HasPrefix(got, "intenter-updater/") {
		t.Errorf("user agent = %q", got)
	}
}

func TestAPrereleaseCheckReadsTheFeed(t *testing.T) {
	server, log := releaseHost(t, "v0.3.0-rc.1")
	cfg := updatesConfig()
	cfg.Channel = config.ChannelPrerelease

	state, err := checkerFor(t, newStore(t), server.URL, cfg).Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if state.LatestKnown == nil || state.LatestKnown.Version != "0.3.0-rc.1" {
		t.Fatalf("latest_known = %+v, want the pre-release", state.LatestKnown)
	}
	if state.Channel != config.ChannelPrerelease {
		t.Errorf("channel = %q, want prerelease", state.Channel)
	}

	requests := log.all()
	if len(requests) != 1 || requests[0].URL.Path != "/releases.atom" {
		t.Errorf("requests = %v, want the feed only", pathsOf(requests))
	}
}

func TestAFailedCheckIsRecordedAndBacksOff(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer server.Close()

	store := newStore(t)
	checker := checkerFor(t, store, server.URL, updatesConfig())

	state, err := checker.Check(context.Background())
	if err == nil {
		t.Fatal("a 429 must be reported as a failed check")
	}
	if state.LastCheckOK || state.CheckFailures != 1 {
		t.Errorf("failure not recorded: %+v", state)
	}
	if !strings.Contains(state.LastCheckError, "429") {
		t.Errorf("last_check_error = %q, want it to name the status", state.LastCheckError)
	}
	if state.NextCheckAfter == nil {
		t.Fatal("a failed check must still schedule the next one")
	}

	entries := store.Tail(10)
	if len(entries) != 1 || entries[0].Event != EventCheckFailed {
		t.Errorf("history = %+v, want one check_failed", entries)
	}
}

func TestASlowHostTimesOutRatherThanHanging(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer func() {
		close(release)
		server.Close()
	}()

	checker := checkerFor(t, newStore(t), server.URL, updatesConfig())
	checker.Timeout = 100 * time.Millisecond

	start := time.Now()
	state, err := checker.Check(context.Background())
	if err == nil {
		t.Fatal("a host that never answers must be a failed check")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("the check took %s despite a 100ms timeout", elapsed)
	}
	if state.LastCheckError != "timed out" {
		t.Errorf("last_check_error = %q, want a readable reason", state.LastCheckError)
	}
}

func TestPlainHTTPIsRefusedUnlessTheSourcesWereOverridden(t *testing.T) {
	// The whole trust chain rests on the transport: an update fetched over
	// plain HTTP is an update an intermediary chose.
	server, _ := releaseHost(t, "v0.2.0")
	checker := checkerFor(t, newStore(t), server.URL, updatesConfig())
	checker.Sources.Overridden = false

	_, err := checker.Check(context.Background())
	if err == nil {
		t.Fatal("an http:// release URL must be refused when it was not asked for")
	}
	if !strings.Contains(err.Error(), "https") {
		t.Errorf("error = %v, want it to say why", err)
	}
}

func TestARedirectToNoVersionIsAFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/releases", http.StatusFound)
	}))
	defer server.Close()

	if _, err := checkerFor(t, newStore(t), server.URL, updatesConfig()).Check(context.Background()); err == nil {
		t.Fatal("a redirect that names no version must fail rather than invent one")
	}
}

func TestSourcesFromEnvPreferTheEnvironment(t *testing.T) {
	// The URL and repository overrides are honored only under the test harness,
	// so a stray variable in a real environment cannot redirect the updater.
	t.Setenv(platform.EnvTestMode, "1")
	t.Setenv(EnvRepo, "someone/fork")
	sources := SourcesFromEnv()
	if sources.LatestURL != "https://github.com/someone/fork/releases/latest" {
		t.Errorf("latest url = %q", sources.LatestURL)
	}
	if sources.NotesURL("0.2.0") != "https://github.com/someone/fork/releases/tag/v0.2.0" {
		t.Errorf("notes url = %q", sources.NotesURL("0.2.0"))
	}

	t.Setenv(EnvLatestURL, "http://127.0.0.1:9/releases/latest")
	t.Setenv(EnvDownloadBase, "http://127.0.0.1:9/releases/download/")
	overridden := SourcesFromEnv()
	if !overridden.Overridden {
		t.Error("an explicit URL must mark the sources as overridden")
	}
	if strings.HasSuffix(overridden.DownloadBase, "/") {
		t.Errorf("download base = %q, want no trailing slash", overridden.DownloadBase)
	}
	if got := overridden.NotesURL("0.2.0"); got != "http://127.0.0.1:9/releases/tag/v0.2.0" {
		t.Errorf("notes url = %q, want the overridden host", got)
	}
}

func TestDefaultSourcesUseTheReleasePageNotTheAPI(t *testing.T) {
	// The API is rate-limited per IP and needs a token to be useful; the
	// redirect is neither.
	sources := SourcesFromEnv()
	for _, u := range []string{sources.LatestURL, sources.AtomURL, sources.DownloadBase} {
		if strings.Contains(u, "api.github.com") {
			t.Errorf("%q uses the API", u)
		}
		if !strings.HasPrefix(u, "https://") {
			t.Errorf("%q is not https", u)
		}
	}
}

func TestFirstFeedTagPrefersTheLink(t *testing.T) {
	body := []byte(`<feed xmlns="http://www.w3.org/2005/Atom">
	  <entry>
	    <id>tag:github.com,2008:Repository/1/v0.3.0-rc.1</id>
	    <title>Codename Badger</title>
	    <link rel="alternate" href="https://github.com/o/r/releases/tag/v0.3.0-rc.1"/>
	  </entry>
	  <entry><title>v0.2.0</title></entry>
	</feed>`)

	tag, err := firstFeedTag(body)
	if err != nil {
		t.Fatalf("firstFeedTag: %v", err)
	}
	if tag != "0.3.0-rc.1" {
		t.Errorf("tag = %q, want the newest entry's", tag)
	}
}

func TestAnEmptyOrUnreadableFeedIsAnError(t *testing.T) {
	for name, body := range map[string]string{
		"no entries":      `<feed xmlns="http://www.w3.org/2005/Atom"></feed>`,
		"not xml":         `<html>404</html>`,
		"no version":      `<feed xmlns="http://www.w3.org/2005/Atom"><entry><title>nightly</title></entry></feed>`,
		"truncated":       `<feed><entry>`,
		"an empty string": ``,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := firstFeedTag([]byte(body)); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func pathsOf(requests []*http.Request) []string {
	out := make([]string, 0, len(requests))
	for _, request := range requests {
		out = append(out, request.URL.Path)
	}
	return out
}
