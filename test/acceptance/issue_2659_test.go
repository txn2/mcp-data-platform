//go:build integration

package acceptance

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2659: Site routing handed to a script an administrator
// chooses. With no script nothing changes; the picker offers only scripts
// defining route(req) and a script without it is refused naming the contract;
// with one chosen, a domain added answers at once as pending, one run follows
// whose req names it, and the status the script returned shows on the domain
// in the portal and manage_site; a failed status keeps the domain; a script
// that raises fails its hosts, an owner reads the generic sentence and never
// the run's error, which an administrator reads; the timer calls route with
// every host and [] for added and removed; Retry runs it at once, an owner's
// only for a failed hostname and not within 15 seconds of a run; a removal
// and an environment rename are each one run; changes during a run wait for
// one following run and no two runs overlap; the save gate keeps route(req) on
// the chosen script; clearing the script stops the runs and hides the
// statuses.
//
// Wire forms: the admin integration routes take a JSON object with script_id
// as a string, interval_seconds as a JSON number and approve as a JSON
// boolean; the portal site routes take JSON objects of strings; manage_site
// takes command and site as strings, sent as literal tools/call params.

const (
	adminIntegrations2659 = "/api/v1/admin/integrations"
	op2659                = "site_routing"
	marker2659            = "SECRET2659marker"
	routed2659            = "routed"
	htmlMessage2659       = "<b>no</b> route"
)

// routeSource2659 answers each hostname by what its name asks for: a name
// holding raise2659 makes the run fail with the marker in its error, one
// holding fail2659 is answered failed with HTML in its message, and a run that
// adds a name holding slow2659 takes six seconds.
const routeSource2659 = `INTEGRATION = "site_routing"

def route(req):
    """Answers each hostname by what its name asks for (acceptance #2659)."""
    names = lambda hs: [h["hostname"] for h in hs]
    print("REQ " + json.encode({"reason": req["reason"], "added": names(req["added"]), "removed": names(req["removed"]), "hosts": names(req["hosts"])}))
    if [h for h in req["added"] if "slow2659" in h["hostname"]]:
        platform.sleep(6)
    if [h for h in req["hosts"] if "raise2659" in h["hostname"]]:
        fail("the ingress refused: ` + marker2659 + `")
    out = {}
    for h in req["hosts"]:
        name = h["hostname"]
        if "fail2659" in name:
            out[name] = {"status": "failed", "message": "` + htmlMessage2659 + ` for " + name}
        else:
            out[name] = {"status": "ready", "message": "` + routed2659 + `"}
    return out
`

// bound2659 is a route script an administrator chose for Site routing.
type bound2659 struct {
	id, name string
	admin    *client
}

// routeScript2659 saves a script defining route(req), which no draft can
// record: a route run is started only by the integration.
func routeScript2659(t *testing.T) (*client, string, string) {
	t.Helper()
	admin := connect(t)
	name := "acc-2659-route-" + unique1579()
	out := admin.seedPreGate(t, map[string]any{
		"command": "create", "name": name, "description": "Routes every hostname (acceptance #2659)", "source": routeSource2659,
	})
	id, _ := out["id"].(string)
	t.Cleanup(func() { admin.call("manage_script", map[string]any{"command": "delete", "name": name}) })
	return admin, id, name
}

// clear2659 clears Site routing's script; an operation with none is fine.
func clear2659(t *testing.T) {
	t.Helper()
	status, got := rest2559(t, keyAdmin2604, http.MethodDelete, adminIntegrations2659+"/"+op2659, nil)
	if status != http.StatusOK && status != http.StatusNotFound {
		t.Fatalf("clearing site routing: %d %v", status, got["_raw"])
	}
}

// bind2659 chooses a new route script for Site routing, approving its version,
// and clears it when the test ends.
func bind2659(t *testing.T, interval int) *bound2659 {
	t.Helper()
	admin, id, name := routeScript2659(t)
	status, got := rest2559(t, keyAdmin2604, http.MethodPut, adminIntegrations2659+"/"+op2659,
		map[string]any{"script_id": id, "interval_seconds": interval, "approve": true})
	want2559(t, "choosing the route script", status, http.StatusOK, got)
	t.Cleanup(func() { clear2659(t) })
	return &bound2659{id: id, name: name, admin: admin}
}

// routeRuns is the script's runs, each read in full by an administrator. Not
// through a session: a criterion waits on the timer past a session's deadline.
func (b *bound2659) routeRuns(t *testing.T) []map[string]any {
	t.Helper()
	status, body := rest2559(t, keyAdmin2604, http.MethodGet, "/api/v1/portal/scripts/"+b.id+"/runs?limit=200", nil)
	if status != http.StatusOK {
		t.Fatalf("listing the route script's runs: %d %v", status, body["_raw"])
	}
	rows, _ := body["data"].([]any)
	out := make([]map[string]any, 0, len(rows))
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		id, _ := row["id"].(string)
		if status, run := rest2559(t, keyAdmin2604, http.MethodGet, "/api/v1/portal/scripts/"+b.id+"/runs/"+id, nil); status == http.StatusOK {
			out = append(out, run)
		}
	}
	return out
}

// request2659 is the req a run's route(req) was handed, as the script printed
// it to the run's log (hostnames only), which an administrator reads.
func request2659(run map[string]any) map[string]any {
	log, _ := run["log"].(string)
	for _, line := range strings.Split(log, "\n") {
		if rest, ok := strings.CutPrefix(line, "REQ "); ok {
			var req map[string]any
			if json.Unmarshal([]byte(rest), &req) == nil {
				return req
			}
		}
	}
	return nil
}

// hostnames2659 are the hostnames of one of req's lists.
func hostnames2659(req map[string]any, field string) []string {
	list, _ := req[field].([]any)
	out := make([]string, 0, len(list))
	for _, raw := range list {
		name, _ := raw.(string)
		out = append(out, name)
	}
	return out
}

func finished2659(run map[string]any) bool {
	s, _ := run["status"].(string)
	return s == "succeeded" || s == "failed" || s == "canceled"
}

// runWhere2659 waits for a finished run of the script for which match holds.
func (b *bound2659) runWhere(t *testing.T, what string, within time.Duration, match func(req map[string]any, run map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		for _, run := range b.routeRuns(t) {
			if finished2659(run) && match(request2659(run), run) {
				return run
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no finished run of the route script %s within %s", what, within)
		}
		time.Sleep(time.Second)
	}
}

// domainRouting2659 is the routing a site's page shows on a domain, as the
// holder of key reads it; nil when it shows none.
func domainRouting2659(t *testing.T, s *site2604, key, hostname string) (map[string]any, map[string]any) {
	t.Helper()
	status, got := s.rest(t, key, http.MethodGet, "", nil)
	want2559(t, "reading the site", status, http.StatusOK, got)
	domains, _ := got["domains"].([]any)
	for _, raw := range domains {
		d, _ := raw.(map[string]any)
		if d["hostname"] == hostname {
			r, _ := d["routing"].(map[string]any)
			return r, got
		}
	}
	t.Fatalf("the site does not list %s: %v", hostname, got["_raw"])
	return nil, got
}

// waitRouting2659 waits until the domain shows status.
func waitRouting2659(t *testing.T, s *site2604, key, hostname, status string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		r, _ := domainRouting2659(t, s, key, hostname)
		if r != nil && r["status"] == status {
			return r
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s did not show %s within 90s: %v", hostname, status, r)
		}
		time.Sleep(time.Second)
	}
}

func addDomain2659(t *testing.T, s *site2604, hostname string) {
	t.Helper()
	status, got := s.rest(t, s.key, http.MethodPost, "/domains", map[string]any{"hostname": hostname, "environment": "prod"})
	want2559(t, "adding "+hostname, status, http.StatusCreated, got)
}

func removeDomain2659(t *testing.T, s *site2604, hostname string) {
	t.Helper()
	status, got := s.rest(t, s.key, http.MethodDelete, "/domains/"+url.PathEscape(hostname), nil)
	if status/100 != 2 {
		t.Fatalf("removing %s: %d %v", hostname, status, got["_raw"])
	}
}

// overview2659 is Site routing's entry in the admin list.
func overview2659(t *testing.T) map[string]any {
	t.Helper()
	status, got := rest2559(t, keyAdmin2604, http.MethodGet, adminIntegrations2659, nil)
	want2559(t, "listing integrations", status, http.StatusOK, got)
	list, _ := got["integrations"].([]any)
	for _, raw := range list {
		o, _ := raw.(map[string]any)
		if spec, _ := o["spec"].(map[string]any); spec["name"] == op2659 || o["operation"] == op2659 {
			return o
		}
	}
	t.Fatalf("the integrations list has no site_routing: %v", got["_raw"])
	return nil
}

// TestIssue2659_WithNoScriptNothingChanges: with no script chosen, adding a
// domain shows no routing status in the portal or manage_site, and the
// operation has no binding and no items.
func TestIssue2659_WithNoScriptNothingChanges(t *testing.T) {
	clear2659(t)
	s := newSite2604(t, keyOwner2604, "acc2659-none-")
	host := "www." + s.name + ".example.test"
	addDomain2659(t, s, host)
	r, got := domainRouting2659(t, s, s.key, host)
	if r != nil {
		t.Errorf("with no script the domain shows routing %v", r)
	}
	if _, ok := got["pages_routing"]; ok {
		t.Errorf("with no script the site shows pages_routing: %v", got["pages_routing"])
	}
	tool := connectAs(t, s.key).call("manage_site", map[string]any{"command": "get", "site": s.name})
	if text := fmt.Sprint(tool); strings.Contains(text, "routing") {
		t.Errorf("with no script manage_site get shows routing: %s", text)
	}
	o := overview2659(t)
	if o["binding"] != nil {
		t.Errorf("site routing has a binding with no script chosen: %v", o["binding"])
	}
	if items, _ := o["items"].([]any); len(items) != 0 {
		t.Errorf("site routing keeps %d items with no script chosen", len(items))
	}
}

// TestIssue2659_ThePickerOffersOnlyRouteScripts: Site routing is listed, its
// picker offers a script defining route(req) and not one without it, and
// choosing the one without it is refused naming the contract.
func TestIssue2659_ThePickerOffersOnlyRouteScripts(t *testing.T) {
	clear2659(t)
	admin, routeID, routeName := routeScript2659(t)
	plain := "acc-2659-main-" + unique1579()
	out := admin.saveScript(map[string]any{
		"command": "create", "name": plain, "description": "Defines main only (acceptance #2659)",
		"source": "def main():\n    \"\"\"Prints.\"\"\"\n    print(\"main\")\n",
	}, nil)
	plainID, _ := out["id"].(string)
	t.Cleanup(func() { admin.call("manage_script", map[string]any{"command": "delete", "name": plain}) })
	overview2659(t)

	status, got := rest2559(t, keyAdmin2604, http.MethodGet, adminIntegrations2659+"/"+op2659+"/scripts?search=acc-2659-", nil)
	want2559(t, "listing the picker's scripts", status, http.StatusOK, got)
	raw, _ := got["_raw"].(string)
	if !strings.Contains(raw, routeID) {
		t.Errorf("the picker does not offer %s, which defines route(req): %s", routeName, raw)
	}
	if strings.Contains(raw, plainID) {
		t.Errorf("the picker offers %s, which does not define route(req): %s", plain, raw)
	}

	status, got = rest2559(t, keyAdmin2604, http.MethodPut, adminIntegrations2659+"/"+op2659,
		map[string]any{"script_id": plainID, "interval_seconds": 900, "approve": true})
	if status != http.StatusBadRequest || !strings.Contains(fmt.Sprint(got["_raw"]), "route(req)") {
		t.Fatalf("choosing a script without route(req) answered %d, not a refusal naming route(req): %v", status, got["_raw"])
	}
	if overview2659(t)["binding"] != nil {
		t.Error("the refused choice left a binding")
	}
}

// TestIssue2659_ADomainIsRoutedByTheChosenScript: with a script chosen, adding
// a domain answers at once and shows pending; one run follows whose req.added
// holds it and whose req.hosts holds every host; the status and message the
// script returned show on the domain in the portal and manage_site; a failed
// answer keeps the domain and shows its message as sent, HTML included; a
// removal is one run whose req.removed holds it, and its status is gone.
func TestIssue2659_ADomainIsRoutedByTheChosenScript(t *testing.T) {
	// The site first: a pass lists sites and then domains, so a site made
	// while it runs is in the next pass.
	s := newSite2604(t, keyOwner2604, "acc2659-route-")
	b := bind2659(t, 900)
	waitPages2659(t, s, "dev")
	host := "slow2659." + s.name + ".example.test"

	start := time.Now()
	addDomain2659(t, s, host)
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("adding a domain took %s; it does not wait on the script", took)
	}
	if r, _ := domainRouting2659(t, s, s.key, host); r == nil || r["status"] != "pending" {
		t.Errorf("right after it was added the domain shows %v, not pending", r)
	}
	r := waitRouting2659(t, s, s.key, host, "ready")
	if r["message"] != routed2659 {
		t.Errorf("the domain shows message %v, not the script's %q", r["message"], routed2659)
	}
	run := b.runWhere(t, "adding "+host, 30*time.Second, func(req, _ map[string]any) bool {
		return slices.Contains(hostnames2659(req, "added"), host)
	})
	req := request2659(run)
	hosts := hostnames2659(req, "hosts")
	for _, want := range []string{host, s.host("")} {
		if !slices.Contains(hosts, want) {
			t.Errorf("req.hosts does not hold %s: %v", want, hosts)
		}
	}
	if req["reason"] != "change" {
		t.Errorf("the run's reason is %v, not change", req["reason"])
	}
	tool := connectAs(t, s.key).call("manage_site", map[string]any{"command": "get", "site": s.name})
	if text := fmt.Sprint(tool); !strings.Contains(text, host) || !strings.Contains(text, "status:ready") {
		t.Errorf("manage_site get does not show %s ready: %s", host, text)
	}

	failing := "fail2659." + s.name + ".example.test"
	addDomain2659(t, s, failing)
	r = waitRouting2659(t, s, s.key, failing, "failed")
	if want := htmlMessage2659 + " for " + failing; r["message"] != want {
		t.Errorf("the failed domain shows %q, not the script's message %q as sent", r["message"], want)
	}

	removeDomain2659(t, s, failing)
	b.runWhere(t, "removing "+failing, 60*time.Second, func(req, _ map[string]any) bool {
		return slices.Contains(hostnames2659(req, "removed"), failing)
	})
	deadline := time.Now().Add(30 * time.Second)
	for strings.Contains(fmt.Sprint(overview2659(t)["items"]), failing) {
		if time.Now().After(deadline) {
			t.Fatalf("%s's status is still kept after its removal ran", failing)
		}
		time.Sleep(time.Second)
	}
}

// TestIssue2659_AScriptThatRaisesFailsItsHosts: a run that raises marks its
// hosts failed; the owner reads the generic sentence and never the run's
// error, in the portal, manage_site or the retry answer, in any encoding; an
// administrator reads the error; Retry runs the script at once; the timer
// calls route with reason timer, every host, and [] for added and removed.
func TestIssue2659_AScriptThatRaisesFailsItsHosts(t *testing.T) {
	s := newSite2604(t, keyOwner2604, "acc2659-raise-")
	b := bind2659(t, 60)
	host := "raise2659." + s.name + ".example.test"
	addDomain2659(t, s, host)
	t.Cleanup(func() { removeDomain2659(t, s, host) })

	owner := waitRouting2659(t, s, s.key, host, "failed")
	if owner["message"] != "The site routing script failed" {
		t.Errorf("the owner reads %q, not the generic sentence", owner["message"])
	}
	admin := waitRouting2659(t, s, keyAdmin2604, host, "failed")
	if !strings.Contains(fmt.Sprint(admin["run_error"]), marker2659) {
		t.Errorf("an administrator does not read the run's error: %v", admin)
	}

	_, ownerSite := domainRouting2659(t, s, s.key, host)
	ownerTool := fmt.Sprint(connectAs(t, s.key).call("manage_site", map[string]any{"command": "get", "site": s.name}))
	ownerDomains := fmt.Sprint(connectAs(t, s.key).call("manage_site", map[string]any{"command": "domains", "site": s.name}))
	retried, retry := ownerRetry2659(t, s)
	for where, text := range map[string]string{
		"the site page": fmt.Sprint(ownerSite["_raw"]), "manage_site get": ownerTool,
		"manage_site domains": ownerDomains, "the retry answer": fmt.Sprint(retry["_raw"]),
	} {
		for _, form := range []string{
			marker2659, base64.StdEncoding.EncodeToString([]byte(marker2659)), hex.EncodeToString([]byte(marker2659)), url.QueryEscape(marker2659),
		} {
			if strings.Contains(text, form) {
				t.Errorf("%s shows the owner the run's error (%q)", where, form)
			}
		}
	}

	retryRun := b.runWhere(t, "after Retry", 30*time.Second, func(_, run map[string]any) bool {
		return startedAfter2659(run, retried)
	})
	// The timer's pass is the first run the interval (60s) starts after the
	// retry's run, with nothing changed and nothing asked for.
	retryStarted, _ := time.Parse(time.RFC3339Nano, fmt.Sprint(retryRun["started_at"]))
	timer := b.runWhere(t, "on the timer", 150*time.Second, func(req, run map[string]any) bool {
		return req["reason"] == "timer" && startedAfter2659(run, retryStarted.Add(55*time.Second))
	})
	req := request2659(timer)
	for _, field := range []string{"added", "removed"} {
		list, ok := req[field].([]any)
		if !ok || len(list) != 0 {
			t.Errorf("a timer pass hands req.%s %v, not []", field, req[field])
		}
	}
	if hosts := hostnames2659(req, "hosts"); !slices.Contains(hosts, host) || !slices.Contains(hosts, s.host("")) {
		t.Errorf("a timer pass does not hand every host: %v", hosts)
	}
}

// ownerRetry2659 is the owner's Retry on a failed row: refused with 429,
// saying when to ask again, within 15 seconds of the last run's start, and
// accepted after. It returns when it was accepted and the answer.
func ownerRetry2659(t *testing.T, s *site2604) (time.Time, map[string]any) {
	t.Helper()
	deadline := time.Now().Add(40 * time.Second)
	for {
		at := time.Now()
		status, got := s.rest(t, s.key, http.MethodPost, "/routing/retry", nil)
		switch {
		case status == http.StatusAccepted:
			return at, got
		case status == http.StatusTooManyRequests && strings.Contains(fmt.Sprint(got["_raw"]), "ask again in"):
		default:
			t.Fatalf("the owner's retry of a failed row answered %d: %v", status, got["_raw"])
		}
		if time.Now().After(deadline) {
			t.Fatal("the owner's retry was still refused 40 seconds after the failure")
		}
		time.Sleep(2 * time.Second)
	}
}

// TestIssue2659_AReaderRetriesOnlyWhatFailed: a site owner's retry is refused
// with 409 when nothing of their site failed, and starts no run; an
// administrator's is accepted.
func TestIssue2659_AReaderRetriesOnlyWhatFailed(t *testing.T) {
	s := newSite2604(t, keyOwner2604, "acc2659-ok-")
	b := bind2659(t, 900)
	waitPages2659(t, s, "dev")
	before := len(b.routeRuns(t))
	status, got := s.rest(t, s.key, http.MethodPost, "/routing/retry", nil)
	if status != http.StatusConflict {
		t.Fatalf("an owner's retry with nothing failed answered %d: %v", status, got["_raw"])
	}
	time.Sleep(3 * time.Second)
	if n := len(b.routeRuns(t)); n != before {
		t.Errorf("a refused retry started a run (%d runs, then %d)", before, n)
	}
	status, got = s.rest(t, keyAdmin2604, http.MethodPost, "/routing/retry", nil)
	want2559(t, "an administrator's retry", status, http.StatusAccepted, got)
}

func startedAfter2659(run map[string]any, at time.Time) bool {
	raw, _ := run["started_at"].(string)
	started, err := time.Parse(time.RFC3339Nano, raw)
	return err == nil && !started.Before(at.Add(-time.Second))
}

// TestIssue2659_ARenameIsOneRun: renaming an environment is one run, with the
// old pages address in req.removed and the new one in req.added.
func TestIssue2659_ARenameIsOneRun(t *testing.T) {
	b := bind2659(t, 900)
	s := newSite2604(t, keyOwner2604, "acc2659-rename-")
	s.setEnv(t, s.key, "uat", map[string]any{"after": "dev"})
	waitPages2659(t, s, "uat")
	s.setEnv(t, s.key, "uat", map[string]any{"rename_to": "qa"})
	b.runWhere(t, "renaming uat to qa", 60*time.Second, func(req, _ map[string]any) bool {
		return slices.Contains(hostnames2659(req, "removed"), s.host("uat")) && slices.Contains(hostnames2659(req, "added"), s.host("qa"))
	})
	var both int
	for _, run := range b.routeRuns(t) {
		req := request2659(run)
		if slices.Contains(hostnames2659(req, "removed"), s.host("uat")) || slices.Contains(hostnames2659(req, "added"), s.host("qa")) {
			both++
		}
	}
	if both != 1 {
		t.Errorf("the rename was handed to %d runs, not one", both)
	}
}

// waitPages2659 waits until an environment's pages address shows ready.
func waitPages2659(t *testing.T, s *site2604, env string) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		status, got := s.rest(t, s.key, http.MethodGet, "", nil)
		want2559(t, "reading the site", status, http.StatusOK, got)
		pages, _ := got["pages_routing"].(map[string]any)
		if r, _ := pages[env].(map[string]any); r != nil && r["status"] == "ready" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s's pages address did not show ready: %v", env, got["pages_routing"])
		}
		time.Sleep(time.Second)
	}
}

// TestIssue2659_ChangesDuringARunWaitForIt: two domains added while a run is
// in flight are one following run whose req.added holds both, and no two runs
// of the operation overlap.
func TestIssue2659_ChangesDuringARunWaitForIt(t *testing.T) {
	s := newSite2604(t, keyOwner2604, "acc2659-queue-")
	b := bind2659(t, 900)
	waitPages2659(t, s, "dev")
	slow := "slow2659." + s.name + ".example.test"
	addDomain2659(t, s, slow)
	deadline := time.Now().Add(30 * time.Second)
	for !runningWith2659(t, b) {
		if time.Now().After(deadline) {
			t.Fatal("the run adding the slow domain did not start")
		}
		time.Sleep(200 * time.Millisecond)
	}
	one, two := "one."+s.name+".example.test", "two."+s.name+".example.test"
	addDomain2659(t, s, one)
	addDomain2659(t, s, two)
	b.runWhere(t, "adding both", 60*time.Second, func(req, _ map[string]any) bool {
		added := hostnames2659(req, "added")
		return slices.Contains(added, one) && slices.Contains(added, two)
	})
	for _, run := range b.routeRuns(t) {
		added := hostnames2659(request2659(run), "added")
		if slices.Contains(added, one) != slices.Contains(added, two) {
			t.Errorf("run %v was handed only one of the two domains: %v", run["id"], added)
		}
	}
	assertNoOverlap2659(t, b.routeRuns(t))
}

// runningWith2659 is whether a run of the script is executing; with the site's
// own addresses routed first, the one executing is the slow domain's.
func runningWith2659(t *testing.T, b *bound2659) bool {
	t.Helper()
	for _, run := range b.routeRuns(t) {
		if run["status"] == "running" {
			return true
		}
	}
	return false
}

func assertNoOverlap2659(t *testing.T, runs []map[string]any) {
	t.Helper()
	type span struct{ from, to time.Time }
	var spans []span
	for _, run := range runs {
		from, err1 := time.Parse(time.RFC3339Nano, fmt.Sprint(run["started_at"]))
		to, err2 := time.Parse(time.RFC3339Nano, fmt.Sprint(run["finished_at"]))
		if err1 == nil && err2 == nil {
			spans = append(spans, span{from, to})
		}
	}
	slices.SortFunc(spans, func(a, b span) int { return a.from.Compare(b.from) })
	for i := 1; i < len(spans); i++ {
		if spans[i].from.Before(spans[i-1].to) {
			t.Errorf("two runs overlap: one ran %s to %s, the next started %s", spans[i-1].from, spans[i-1].to, spans[i].from)
		}
	}
}

// TestIssue2659_TheSaveGateKeepsRoute: a version of the chosen script that no
// longer defines route(req) is refused, and the script keeps its version.
func TestIssue2659_TheSaveGateKeepsRoute(t *testing.T) {
	b := bind2659(t, 900)
	res, text, err := b.admin.callRaw("manage_script", map[string]any{
		"command": "update", "name": b.name, "source": "def main():\n    \"\"\"Routes nothing.\"\"\"\n    print(\"none\")\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError && b.admin.parse(text)["status"] != "invalid" {
		t.Fatalf("a version without route(req) was saved: %s", text)
	}
	if !strings.Contains(text, "route(req)") {
		t.Errorf("the refusal does not name route(req): %s", text)
	}
	got := b.admin.call("manage_script", map[string]any{"command": "get", "name": b.name})
	if source, _ := got["source"].(string); !strings.Contains(source, "def route(req):") {
		t.Errorf("the refused version was saved: %v", got["version"])
	}
}

// TestIssue2659_ClearingStopsTheRuns: once the script is cleared, a domain
// added starts no run and no status shows.
func TestIssue2659_ClearingStopsTheRuns(t *testing.T) {
	b := bind2659(t, 900)
	s := newSite2604(t, keyOwner2604, "acc2659-clear-")
	first := "a." + s.name + ".example.test"
	addDomain2659(t, s, first)
	waitRouting2659(t, s, s.key, first, "ready")
	clear2659(t)
	before := len(b.routeRuns(t))

	second := "b." + s.name + ".example.test"
	addDomain2659(t, s, second)
	for _, host := range []string{first, second} {
		if r, _ := domainRouting2659(t, s, s.key, host); r != nil {
			t.Errorf("after clearing, %s shows routing %v", host, r)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if n := len(b.routeRuns(t)); n != before {
			t.Fatalf("after clearing, the script ran again (%d runs, then %d)", before, n)
		}
		time.Sleep(time.Second)
	}
	if items, _ := overview2659(t)["items"].([]any); len(items) != 0 {
		t.Errorf("after clearing, site routing keeps %d items", len(items))
	}
}
