// Package releaseserve serves a directory of build artifacts the way GitHub
// Releases does, so the installers can be exercised without publishing
// anything.
//
// It exists because the one-line installers are the part of Intenter most
// likely to be broken by a change and least likely to be noticed: they run once
// on someone else's machine, and a mistake shows up as a stranger's failed
// install. Testing them against a real release means a release has to exist
// first, which is exactly backwards — so the release workflow verifies the
// artifacts it has just built by pointing the real scripts at this server
// before publishing them.
package releaseserve

import (
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Handler serves the assets in dir as the release named tag.
//
// Two routes matter to an installer:
//
//	GET /releases/latest              → 302 to /releases/tag/<tag>
//	GET /releases/download/<tag>/<a>  → the asset bytes
//
// The redirect is how the installers learn the current version without calling
// the GitHub API, so it is the behavior most worth reproducing faithfully.
//
// A third route, /releases.atom, is what the updater reads on the pre-release
// channel — the redirect above deliberately excludes pre-releases, so following
// them needs a source that does not.
func Handler(dir, tag string) (http.Handler, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("releaseserve: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("releaseserve: %s is not a directory", dir)
	}
	if tag == "" {
		return nil, fmt.Errorf("releaseserve: a tag is required")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/releases/tag/"+tag, http.StatusFound)
	})
	mux.HandleFunc("/releases/tag/", func(w http.ResponseWriter, _ *http.Request) {
		// A release page. Nothing reads its body; the installers only follow
		// the redirect to learn the tag from the URL.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "<html><body><h1>%s</h1></body></html>\n", tag)
	})
	mux.HandleFunc("/releases/download/", func(w http.ResponseWriter, r *http.Request) {
		serveAsset(w, r, dir)
	})
	mux.HandleFunc("/releases.atom", func(w http.ResponseWriter, r *http.Request) {
		serveFeed(w, r, tag)
	})
	return mux, nil
}

// serveFeed answers /releases.atom with a single entry for the served tag,
// shaped like GitHub's: an `id` ending in the tag and a `link` to the release
// page. Those are the two fields the updater reads.
func serveFeed(w http.ResponseWriter, r *http.Request, tag string) {
	page := "http://" + r.Host + "/releases/tag/" + tag
	w.Header().Set("Content-Type", "application/atom+xml; charset=utf-8")
	fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Release notes</title>
  <entry>
    <id>tag:github.com,2008:Repository/1/%s</id>
    <title>%s</title>
    <link rel="alternate" type="text/html" href="%s"/>
  </entry>
</feed>
`, tag, tag, page)
}

// serveAsset answers /releases/download/<tag>/<asset>.
func serveAsset(w http.ResponseWriter, r *http.Request, dir string) {
	rest := strings.TrimPrefix(path.Clean(r.URL.Path), "/releases/download/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		http.NotFound(w, r)
		return
	}

	// The asset name comes from a URL, so it is never allowed to escape the
	// directory being served — the same rule that applies to the real thing.
	name := parts[1]
	if name != filepath.Base(name) || name == "." || name == ".." {
		http.NotFound(w, r)
		return
	}

	file := filepath.Join(dir, name)
	if _, err := os.Stat(file); err != nil {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, file)
}
