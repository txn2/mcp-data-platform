//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Issue #2597: requests the site and page gateway answered were recorded under
// the route the platform's mux would have matched for the path (the MCP
// transport's "/", or a platform route such as GET /api/v1/resources when a
// site's path matched one), with no series per site. Now every request the
// gateway answers is recorded under its own route ("site", or "pages" for an
// application's page origin), and counted per site and environment in
// site_requests_total and site_request_duration_seconds, labeled only from
// the route the gateway resolved, with site_cache_reads_total counting where
// each file body came from.
//
// Against the dev stack: a site created and published through manage_site,
// requested on a hostname added to its dev environment, and every replica's
// metrics endpoint scraped.
//
// Wire forms: manage_site's command, site, environment, hostname and the
// files' path and content are JSON strings, the files a JSON array of objects,
// requires_approval a JSON boolean; the site requests are plain HTTP GETs
// naming the hostname in Host.

// issue2597Series sums every replica's samples of one series.
func issue2597Series(t *testing.T, prefix string) float64 {
	t.Helper()
	var total float64
	for line := range strings.SplitSeq(scrapeRaw(t), "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		if v, err := strconv.ParseFloat(line[strings.LastIndex(line, " ")+1:], 64); err == nil {
			total += v
		}
	}
	return total
}

// TestIssue2597_SiteTrafficIsCountedAsTheSite: a site answers two pages and a
// path it has no file for that is also a platform route; each is recorded
// under route "site" and counted under the site's name and environment, and
// the hostname that names nothing mints no series.
func TestIssue2597_SiteTrafficIsCountedAsTheSite(t *testing.T) {
	c := connect(t)
	s := newSite2518(t, c)
	s.publish(t, "dev", "v1")
	host := s.toStage(t) // dev is the working copy and has no domain (#2604); stage's is public
	served2518(t, host, "v1")

	routeBefore := issue2597Series(t, `http_server_request_duration_seconds_count{method="GET",route="site",status_class="2xx"}`)
	for _, target := range []string{"/", "/about/"} {
		if r := onHost(t, host, http.MethodGet, target, nil, ""); r.status != http.StatusOK {
			t.Fatalf("GET %s on %s: %d", target, host, r.status)
		}
	}
	if r := onHost(t, host, http.MethodGet, "/api/v1/resources", nil, ""); r.status != http.StatusNotFound || !strings.Contains(r.body, "nothing here") {
		t.Fatalf("a path the site has no file for answered %d: %.200s; want the site's own 404 page", r.status, r.body)
	}

	ok := fmt.Sprintf(`site_requests_total{environment="stage",kind="site",site=%q,status_class="2xx"}`, s.name)
	missing := fmt.Sprintf(`site_requests_total{environment="stage",kind="site",site=%q,status_class="4xx"}`, s.name)
	duration := fmt.Sprintf(`site_request_duration_seconds_count{environment="stage",kind="site",site=%q}`, s.name)
	deadline := time.Now().Add(30 * time.Second)
	for {
		okN, missingN, durationN := issue2597Series(t, ok), issue2597Series(t, missing), issue2597Series(t, duration)
		routeN := issue2597Series(t, `http_server_request_duration_seconds_count{method="GET",route="site",status_class="2xx"}`)
		// served2518 polled the page before the counted requests, so the 2xx
		// count is at least the two counted here.
		if okN >= 2 && missingN >= 1 && durationN >= okN+missingN && routeN >= routeBefore+2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after 30s: %s=%g %s=%g %s=%g, route=site went %g to %g", ok, okN, missing, missingN, duration, durationN, routeBefore, routeN)
		}
		time.Sleep(500 * time.Millisecond)
	}
	if issue2597Series(t, `site_cache_reads_total{outcome=`) == 0 {
		t.Error("no site_cache_reads_total sample after serving files")
	}

	nobody := "nobody-" + unique1579() + "." + pagesDomain2517
	_ = onHost(t, nobody, http.MethodGet, "/", nil, "")
	label := regexp.MustCompile(`site="nobody-`)
	if label.MatchString(scrapeRaw(t)) {
		t.Errorf("a hostname nobody added minted a site series")
	}
}
