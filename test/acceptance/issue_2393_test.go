//go:build integration

package acceptance

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	s3client "github.com/txn2/mcp-s3/pkg/client"
)

// Acceptance for #2393: the map routes are served to this deployment and the
// sites the maps settings allow, not to every website. Each read goes to the
// running platform through the proxy, with the headers a browser sends from
// each place a map is drawn:
//
//   - this deployment's own pages: its Origin, or its Referer;
//   - the platform's own sandboxed asset frame, which Chromium sends with
//     Origin: null and no Referer (observed for #2393);
//   - curl and the renderers, which send neither;
//   - another site, by Origin, by Referer, and from its own sandboxed frame
//     carrying its Referer.
//
// Wire forms: the settings route takes one JSON object whose allowed_origins
// is a JSON array of strings, the one form its schema admits; the map routes
// are GETs and OPTIONS with no body.

const (
	issue2393Runtime = "/portal/vendor/maplibre/maplibre-gl.js"
	issue2393Regions = "/portal/maps/regions"
	issue2393Foreign = "https://freeloader-2393.example"
	issue2393Listed  = "https://stores-2393.example"
)

// issue2393Region uploads the seed's San Francisco archive as a ready region
// and returns its archive path; maps are on for the test and restored after.
func issue2393Region(t *testing.T) string {
	t.Helper()
	issue2068Restore(t)
	issue2393SetSites(t, nil)
	id := issue2068Name("acc-2393")
	key := "maps/uploads/" + id + ".pmtiles"
	src, err := os.ReadFile(filepath.Join("..", "..", "dev", "seed-content", "san-francisco.pmtiles"))
	if err != nil {
		t.Fatal(err)
	}
	store := issue1870S3(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if _, err := store.PutObjectStream(ctx, &s3client.PutObjectStreamInput{
		Bucket: issue2068Bucket, Key: key, Body: strings.NewReader(string(src)), ContentType: "application/vnd.pmtiles",
	}); err != nil {
		t.Fatalf("uploading the archive: %v", err)
	}
	t.Cleanup(func() {
		del, done := context.WithTimeout(context.Background(), time.Minute)
		defer done()
		_ = store.DeleteObject(del, issue2068Bucket, key)
	})
	issue2393SetSites(t, nil) // a settings write starts the pass that lists it
	deadline := time.Now().Add(90 * time.Second)
	for {
		if r, ok := issue2068GetRegion(t, id); ok && r.State == "ready" {
			return "/portal/maps/" + id + ".pmtiles"
		}
		if time.Now().After(deadline) {
			t.Fatalf("the uploaded archive %s was not listed ready", key)
		}
		time.Sleep(time.Second)
	}
}

// issue2393SetSites turns maps on with these allowed sites. issue2068Restore
// puts the settings back after, and its write, naming no sites, leaves none.
func issue2393SetSites(t *testing.T, sites []string) {
	t.Helper()
	if sites == nil {
		sites = []string{}
	}
	status, raw := issue2068Do(t, baseURL(), http.MethodPut, issue2068MapsPath, map[string]any{
		"enabled": true, "s3_connection": "", "bucket": "", "max_zoom": 14, "source_url": "", "allowed_origins": sites,
	})
	if status != http.StatusOK {
		t.Fatalf("PUT %s with %v: %d %s", issue2068MapsPath, sites, status, raw)
	}
}

// issue2393Read sends one unauthenticated read as a browser at origin and
// referer would ("" sends no such header).
func issue2393Read(t *testing.T, method, path, origin, referer string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, baseURL()+path, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Range", "bytes=0-126")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	if method == http.MethodOptions {
		req.Header.Set("Access-Control-Request-Method", http.MethodGet)
		req.Header.Set("Access-Control-Request-Headers", "range")
	}
	res, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close() //nolint:errcheck // read below
	body, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	return res, string(body)
}

// issue2393Own is this deployment's origin as a browser on it sends it.
func issue2393Own(t *testing.T) string {
	t.Helper()
	u, err := url.Parse(baseURL())
	if err != nil {
		t.Fatal(err)
	}
	return u.Scheme + "://" + u.Host
}

func issue2393Served(t *testing.T, res *http.Response, wantOrigin string) {
	t.Helper()
	if res.StatusCode != http.StatusPartialContent && res.StatusCode != http.StatusOK {
		t.Fatalf("answered %d, want it served", res.StatusCode)
	}
	if !strings.Contains(res.Header.Get("Vary"), "Origin") || !strings.Contains(res.Header.Get("Vary"), "Referer") {
		t.Errorf("Vary = %q, want Origin and Referer", res.Header.Get("Vary"))
	}
	if wantOrigin != "" && res.Header.Get("Access-Control-Allow-Origin") != wantOrigin {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", res.Header.Get("Access-Control-Allow-Origin"), wantOrigin)
	}
}

func issue2393Refused(t *testing.T, res *http.Response, body string) {
	t.Helper()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("answered %d, want 403", res.StatusCode)
	}
	if got := res.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("a refusal grants CORS to %q", got)
	}
	if strings.Contains(body, "freeloader") {
		t.Errorf("the refusal repeats the site: %q", body)
	}
}

func TestIssue2393_ThePlatformItselfIsServed(t *testing.T) {
	archive := issue2393Region(t)
	own := issue2393Own(t)

	res, _ := issue2393Read(t, http.MethodGet, archive, own, "")
	issue2393Served(t, res, own)
	if res.StatusCode != http.StatusPartialContent {
		t.Errorf("the archive answered %d to a ranged read, want 206", res.StatusCode)
	}
	if got := res.Header.Get("Cache-Control"); got != "private, no-cache" {
		t.Errorf("the archive's Cache-Control = %q, want private, no-cache", got)
	}
	for _, path := range []string{archive, issue2393Regions, issue2393Runtime} {
		res, _ := issue2393Read(t, http.MethodGet, path, "", own+"/portal/assets/a1")
		issue2393Served(t, res, "")
	}
}

func TestIssue2393_ReadsNamingNoSiteAreServed(t *testing.T) {
	archive := issue2393Region(t)
	// curl, a script, a renderer: no Origin, no Referer.
	res, _ := issue2393Read(t, http.MethodGet, archive, "", "")
	issue2393Served(t, res, "")
	// The platform's own sandboxed asset frame, as Chromium sends it.
	for _, path := range []string{archive, issue2393Regions, issue2393Runtime} {
		res, _ := issue2393Read(t, http.MethodGet, path, "null", "")
		issue2393Served(t, res, "null")
	}
}

func TestIssue2393_AnotherSiteIsRefused(t *testing.T) {
	archive := issue2393Region(t)
	for _, path := range []string{archive, issue2393Regions, issue2393Runtime} {
		t.Run(path, func(t *testing.T) {
			res, body := issue2393Read(t, http.MethodGet, path, issue2393Foreign, "")
			issue2393Refused(t, res, body)
			res, body = issue2393Read(t, http.MethodGet, path, "", issue2393Foreign+"/locator")
			issue2393Refused(t, res, body)
			// Its own sandboxed frame sends null and, here, its Referer.
			res, body = issue2393Read(t, http.MethodGet, path, "null", issue2393Foreign+"/")
			issue2393Refused(t, res, body)
			res, body = issue2393Read(t, http.MethodOptions, path, issue2393Foreign, "")
			issue2393Refused(t, res, body)
		})
	}
}

func TestIssue2393_AnAllowedSiteIsServedUntilItIsRemoved(t *testing.T) {
	archive := issue2393Region(t)
	issue2393SetSites(t, []string{issue2393Listed, "https://*.wild-2393.example"})

	res, _ := issue2393Read(t, http.MethodGet, archive, issue2393Listed, "")
	issue2393Served(t, res, issue2393Listed)
	res, _ = issue2393Read(t, http.MethodGet, archive, "https://a.b.wild-2393.example", "")
	issue2393Served(t, res, "https://a.b.wild-2393.example")
	res, _ = issue2393Read(t, http.MethodOptions, issue2393Runtime, issue2393Listed, "")
	if res.StatusCode != http.StatusNoContent || res.Header.Get("Access-Control-Allow-Origin") != issue2393Listed {
		t.Errorf("the preflight answered %d with %q", res.StatusCode, res.Header.Get("Access-Control-Allow-Origin"))
	}
	res, body := issue2393Read(t, http.MethodGet, archive, "https://wild-2393.example", "")
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("the wildcard's bare domain answered %d %s, want 403", res.StatusCode, body)
	}

	// Removed, the next reads are refused on every replica within the ten
	// seconds a replica keeps the list it read, with no restart.
	issue2393SetSites(t, nil)
	deadline := time.Now().Add(20 * time.Second)
	for {
		refused := true
		for _, r := range replicas(t) {
			req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, r.base+archive, http.NoBody)
			req.Header.Set("Origin", issue2393Listed)
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = res.Body.Close()
			refused = refused && res.StatusCode == http.StatusForbidden
		}
		if refused {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("a removed site was still served 20 seconds later")
		}
		time.Sleep(time.Second)
	}
}

func TestIssue2393_TheSettingRefusesAMalformedSiteByName(t *testing.T) {
	issue2068Restore(t)
	t.Cleanup(func() { issue2393SetSites(t, nil) })
	for _, bad := range []string{"https://example.com/path", "http://example.com", "garbage"} {
		status, raw := issue2068Do(t, baseURL(), http.MethodPut, issue2068MapsPath, map[string]any{
			"enabled": true, "max_zoom": 14, "allowed_origins": []string{"https://ok-2393.example", bad},
		})
		if status != http.StatusBadRequest || !strings.Contains(string(raw), bad) {
			t.Errorf("%q answered %d %s, want 400 naming it", bad, status, raw)
		}
	}
	issue2393SetSites(t, []string{"https://Example.com:443", "http://localhost:3000"})
	status, raw := issue2068Do(t, baseURL(), http.MethodGet, issue2068MapsPath, nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s: %d", issue2068MapsPath, status)
	}
	var view struct {
		Settings struct {
			AllowedOrigins []string `json:"allowed_origins"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(view.Settings.AllowedOrigins, " "); got != "https://example.com http://localhost:3000" {
		t.Errorf("the stored sites read back %q, want them normalized", got)
	}
}
