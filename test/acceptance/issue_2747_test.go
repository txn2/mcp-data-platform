//go:build integration

package acceptance

import (
	"bytes"
	"image/png"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2747: a site environment's tile is drawn as the site's
// owner. A private environment that is a part of an application, whose page
// fetches its application's endpoint before it paints, drew the endpoint's
// 401 because the tile page answered every /endpoints/ request as a visitor
// who had not signed in, which no pages address has. The owner is resolved
// from the live directory when the tile is drawn; only GET and HEAD reach an
// endpoint; the draw is not the site's traffic; its endpoint run is audited
// as the owner, marked tile_draw; an owner the directory cannot speak for
// draws the page for nobody; the renderer generation is raised so every
// stored tile is drawn again.
//
// Wire forms: manage_site takes command, site, note and files ([{path,
// content}] of strings); manage_application takes command, application, kind,
// part_id and release_mode as strings; the portal site PATCH takes owner_email
// as a string; the tile route takes the site and environment in its path. A
// request the tile page makes is answered in process. No tool parameter is
// touched.

const (
	owner2747     = "analyst@example.com"
	ownerPass2747 = "analyst-password"
	// nobody2747 is an address the directory holds no row for: an owner the
	// platform cannot speak for, as one removed from the directory is.
	nobody2747 = "gone-2747@example.com"
)

// handler2747 answers who called; a request with no caller is refused, as an
// authenticated endpoint refuses it.
const handler2747 = `def handle(req):
    """Answers who called."""
    caller = req["caller"]
    if caller == None:
        return {"status": 401, "json": {"error": "nobody"}}
    return {"json": {"email": caller["email"], "auth_type": caller["auth_type"]}}
`

// page2747 paints the whole page green when the endpoint answered a caller's
// address to a GET and refused a POST with 405, and red otherwise: what the
// tile shows is what the page's requests were answered with.
func page2747(ep string) string {
	return `<!doctype html><html><body style="margin:0;background:#fff"><script>
(async function(){
  var ok = false;
  try {
    var got = await fetch("/endpoints/` + ep + `/", {credentials: "same-origin"});
    var body = got.ok ? await got.json() : {};
    var post = await fetch("/endpoints/` + ep + `/", {method: "POST", credentials: "same-origin", body: "{}"});
    ok = got.status === 200 && String(body.email || "").indexOf("@") > 0 && post.status === 405;
  } catch (e) {}
  var d = document.createElement("div");
  d.style.cssText = "position:fixed;inset:0;background:" + (ok ? "rgb(0,170,0)" : "rgb(220,0,0)");
  d.textContent = ok ? "drawn as the owner" : "refused";
  document.body.appendChild(d);
})();
</script></body></html>`
}

// app2747 is an application with a dev environment holding the endpoint, and
// a site whose dev is a part of it.
type app2747 struct {
	admin *client
	ep    string
	site  *site2604
}

func newApp2747(t *testing.T) *app2747 {
	t.Helper()
	admin := connectFor(t, 6*time.Minute)
	a := &app2747{admin: admin, ep: endpointName("acc-2747")}
	name := "acc2747-" + unique1579()
	if status := admin.restJSON(http.MethodPost, "/api/v1/portal/applications", map[string]any{
		"name": name, "description": "Acceptance #2747", "environments": []string{"dev"},
	}); status != http.StatusOK {
		t.Fatalf("creating the application answered %d", status)
	}
	t.Cleanup(func() {
		_, _, _ = admin.callRaw("manage_application", map[string]any{"command": "delete", "application": name})
	})
	id := saveEndpointScript(t, admin, a.ep, handler2747, sample2696)
	bindEndpoint(t, admin, a.ep, "", map[string]any{
		"endpoint": a.ep, "methods": []string{"GET", "POST"}, "path": "/", "auth_mode": "authenticated", "callers": []string{"*"},
	})
	admin.application(map[string]any{"command": "add_part", "application": name, "kind": "script", "part_id": id})

	a.site = newSite2604(t, keyAdmin2604, "acc2747-web-")
	a.publish(t, "one")
	admin.application(map[string]any{"command": "add_part", "application": name, "kind": "site", "part_id": a.site.name + "/dev", "release_mode": "coupled"})
	return a
}

// publish puts a new release of the page on dev, which the tile worker draws.
func (a *app2747) publish(t *testing.T, note string) {
	t.Helper()
	connectAs(t, keyAdmin2604).call("manage_site", map[string]any{
		"command": "publish", "site": a.site.name, "note": note,
		"files": []map[string]any{{"path": "index.html", "content": page2747(a.ep)}},
	})
}

// own makes email the site's owner.
func (a *app2747) own(t *testing.T, email string) {
	t.Helper()
	status, got := a.site.rest(t, keyAdmin2604, http.MethodPatch, "", map[string]any{"owner_email": email})
	want2559(t, "moving the site to "+email, status, http.StatusOK, got)
}

// drawnRelease waits for dev's tile of release and returns its PNG.
func (a *app2747) drawnRelease(t *testing.T, release int) []byte {
	t.Helper()
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		var drawn int
		_ = devDB(t).QueryRowContext(t.Context(),
			`SELECT COALESCE(t.drawn_release, 0) FROM site_tiles t JOIN sites s ON s.id = t.site_id
			  WHERE s.name = $1 AND t.environment = 'dev' AND t.tile_key <> ''`, a.site.name).Scan(&drawn)
		if drawn == release {
			req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL()+a.site.path("/environments/dev/tile"), http.NoBody)
			req.Header.Set("X-API-Key", keyAdmin2604)
			res, err := http.DefaultClient.Do(req) // #nosec G704 -- the platform under test
			if err == nil {
				body, _ := io.ReadAll(res.Body)
				_ = res.Body.Close()
				if res.StatusCode == http.StatusOK {
					return body
				}
			}
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("dev's tile of release %d was not drawn within three minutes", release)
	return nil
}

// centre is the colour at the middle of a tile.
func centre2747(t *testing.T, shot []byte) (r, g, b uint32) {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(shot))
	if err != nil {
		t.Fatalf("the tile is not a PNG: %v", err)
	}
	bounds := img.Bounds()
	r, g, b, _ = img.At(bounds.Dx()/2, bounds.Dy()/2).RGBA()
	return r >> 8, g >> 8, b >> 8
}

// runs2747 is the endpoint's audit rows since since: each method, caller and
// whether it is marked a tile draw, and every error message.
type run2747 struct {
	method, caller, tile, errText string
}

func (a *app2747) runs(t *testing.T, since time.Time) []run2747 {
	t.Helper()
	rows, err := devDB(t).QueryContext(t.Context(), `
		SELECT COALESCE(parameters->>'method', ''), COALESCE(parameters->>'caller', ''),
		       COALESCE(parameters->>'tile_draw', ''), COALESCE(error_message, '')
		  FROM audit_logs WHERE tool_name = $1 AND timestamp >= $2 ORDER BY timestamp`, "endpoint:"+a.ep, since)
	if err != nil {
		t.Fatalf("reading the endpoint's audit rows: %v", err)
	}
	defer func() { _ = rows.Close() }()
	out := []run2747{}
	for rows.Next() {
		var r run2747
		if err := rows.Scan(&r.method, &r.caller, &r.tile, &r.errText); err != nil {
			t.Fatalf("reading an audit row: %v", err)
		}
		out = append(out, r)
	}
	return out
}

// waitRuns polls until the endpoint has at least n audit rows since since:
// rows are written off the request path.
func (a *app2747) waitRuns(t *testing.T, since time.Time, n int) []run2747 {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for {
		got := a.runs(t, since)
		if len(got) >= n || time.Now().After(deadline) {
			return got
		}
		time.Sleep(time.Second)
	}
}

// TestIssue2747_ATileIsDrawnAsTheOwner covers criteria 1, 2, 3 and 6: the
// page's GET runs as the owner and its POST is refused 405, so the tile is
// painted green; the run is audited with the owner as caller and marked as a
// tile draw, and no POST run is audited; the draw adds nothing to the site's
// traffic; and the owner's address is in no log line, audit error or search
// index entry the draw wrote.
func TestIssue2747_ATileIsDrawnAsTheOwner(t *testing.T) {
	_ = browserSignIn(t, baseURL(), owner2747, ownerPass2747)
	a := newApp2747(t)
	logsBefore := len(platformLogs2709(t))
	since := time.Now().Add(-time.Second)
	a.own(t, owner2747)
	a.publish(t, "two")

	shot := a.drawnRelease(t, 2)
	if r, g, b := centre2747(t, shot); g < 140 || r > 60 || b > 60 {
		t.Fatalf("the tile's centre is rgb(%d,%d,%d); the page paints it green only when its endpoint answered the owner's address and refused a POST with 405", r, g, b)
	}

	runs := a.waitRuns(t, since, 1)
	var drawRuns int
	for _, r := range runs {
		if r.method == http.MethodPost {
			t.Errorf("a POST from the tile page ran the endpoint: %+v", r)
		}
		if r.tile == "true" {
			drawRuns++
			if r.caller != owner2747 {
				t.Errorf("the draw's run was audited with caller %q, want the owner %s", r.caller, owner2747)
			}
		}
		if strings.Contains(r.errText, owner2747) {
			t.Errorf("an audit row's error repeats the owner's address: %q", r.errText)
		}
	}
	if drawRuns == 0 {
		t.Fatalf("no endpoint run was audited as a tile draw: %+v", runs)
	}

	// Criterion 2: the site's traffic is flushed every minute; a flush past
	// the draw adds no request for the site.
	settle := time.Now().Add(75 * time.Second)
	for time.Now().Before(settle) {
		time.Sleep(5 * time.Second)
	}
	var requests int64
	if err := devDB(t).QueryRowContext(t.Context(), `
		SELECT COALESCE(SUM(c2xx + c3xx + c4xx + c5xx + cother), 0) FROM site_traffic st JOIN sites s ON s.id = st.subject
		 WHERE st.kind = 'site' AND s.name = $1`, a.site.name).Scan(&requests); err != nil {
		t.Fatalf("reading the site's traffic: %v", err)
	}
	if requests != 0 {
		t.Errorf("the site's traffic counts %d requests; a tile's draw is not the site's traffic", requests)
	}

	// Criterion 6.
	if logs := platformLogs2709(t); len(logs) > logsBefore && strings.Contains(logs[logsBefore:], owner2747) {
		t.Errorf("a log line written during the draw names the owner %s", owner2747)
	}
	var indexed int
	if err := devDB(t).QueryRowContext(t.Context(),
		`SELECT count(*) FROM index_jobs WHERE created_at >= $1 AND payload::text ILIKE '%' || $2 || '%'`, since, owner2747).Scan(&indexed); err == nil && indexed > 0 {
		t.Errorf("%d search index jobs written since the draw carry the owner's address", indexed)
	}
}

// TestIssue2747_AnOwnerTheDirectoryCannotSpeakForDrawsForNobody covers
// criterion 4: with the site moved to an address the directory holds no row
// for, the next draw's endpoint request answers 401, the tile is painted red,
// and no endpoint run is audited.
func TestIssue2747_AnOwnerTheDirectoryCannotSpeakForDrawsForNobody(t *testing.T) {
	a := newApp2747(t)
	a.own(t, nobody2747)
	since := time.Now().Add(-time.Second)
	a.publish(t, "two")
	shot := a.drawnRelease(t, 2)
	if r, g, b := centre2747(t, shot); r < 180 || g > 60 || b > 60 {
		t.Fatalf("the tile's centre is rgb(%d,%d,%d); with no owner the endpoint answers 401 and the page paints red", r, g, b)
	}
	// Audit rows are written off the request path: give a run that should
	// not exist the time one that does would take.
	time.Sleep(5 * time.Second)
	if runs := a.runs(t, since); len(runs) != 0 {
		t.Errorf("an endpoint ran for a tile whose owner does not resolve: %+v", runs)
	}
}

// TestIssue2747_AnUpgradeDrawsEveryTileAgain covers criterion 5: a tile drawn
// under renderer 3, and one recorded as a failure under it, are drawn again
// by the worker with nobody asking.
func TestIssue2747_AnUpgradeDrawsEveryTileAgain(t *testing.T) {
	for name, set := range map[string]string{
		"drawn":  `UPDATE site_tiles t SET renderer = 3 FROM sites s WHERE s.id = t.site_id AND s.name = $1`,
		"failed": `UPDATE site_tiles t SET renderer = 3, tile_key = '', failure = 'drawn under renderer 3' FROM sites s WHERE s.id = t.site_id AND s.name = $1`,
	} {
		t.Run(name, func(t *testing.T) {
			s, _ := drawn2701(t, "acc2747-"+name+"-")
			if _, err := devDB(t).ExecContext(t.Context(), set, s.name); err != nil {
				t.Fatalf("recording the tile under renderer 3: %v", err)
			}
			deadline := time.Now().Add(2 * time.Minute)
			for {
				key, renderer := tileKey2701(t, s, "dev")
				if renderer > 3 && key != "" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("the worker did not draw renderer 3's tile again: renderer %d, key %q", renderer, key)
				}
				time.Sleep(time.Second)
			}
		})
	}
}
