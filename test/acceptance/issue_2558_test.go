//go:build integration

package acceptance

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2558: the public applications guide is followed as written.
// The criteria read the guide's own schema, scripts and site files out of the
// page the platform serves (internal/platform/knowledgebuiltin/pages/
// public-applications.md), change only the names a deployment chooses (the
// schema, the hostnames, the endpoint, the secret, the mail connection and the
// provider's path), build the application and its storefront site through
// manage_application, manage_schema, manage_script and manage_site, and drive
// it as an anonymous visitor over real HTTP on the site's public hostname: the
// storefront's pages, and the JSON API its script calls on the same address.
// The mail provider is the dev stack's api-test upstream, reached through an
// api connection of the criterion's own; it is made unreachable by pointing
// that connection at a port nothing answers, which is how a provider that is
// down looks to the platform.
//
// Wire forms: manage_application, manage_schema, manage_script and
// manage_site take their arguments as JSON strings (version, rate_limit and
// ip_rate_limit as numbers, capture, methods, callers and files as
// lists, public as a boolean); the api connection and the webhook source are
// one JSON object each through the admin API. A visitor's request is an HTTP
// GET, or a POST of a JSON object to the API, naming the site's hostname in
// Host, with the application cookie in a Cookie header and, on a POST, the
// site's own Origin.

const guide2558 = "../../internal/platform/knowledgebuiltin/pages/public-applications.md"

// apiTestBase2558 is the dev stack's api-test upstream, the mail provider.
const apiTestBase2558 = "http://localhost:9282"

// fences2558 is every fenced block of the guide in the language lang, in order.
func fences2558(t *testing.T, lang string) []string {
	t.Helper()
	raw, err := os.ReadFile(guide2558)
	if err != nil {
		t.Fatalf("reading the guide: %v", err)
	}
	var out []string
	var cur *strings.Builder
	for line := range strings.SplitSeq(string(raw), "\n") {
		switch {
		case cur == nil && strings.TrimSpace(line) == "```"+lang:
			cur = &strings.Builder{}
		case cur != nil && strings.TrimSpace(line) == "```":
			out = append(out, cur.String())
			cur = nil
		case cur != nil:
			cur.WriteString(line + "\n")
		}
	}
	return out
}

// fence2558 is the one block in lang holding marker.
func fence2558(t *testing.T, lang, marker string) string {
	t.Helper()
	for _, b := range fences2558(t, lang) {
		if strings.Contains(b, marker) {
			return b
		}
	}
	t.Fatalf("the guide has no %s block holding %q", lang, marker)
	return ""
}

// store2558 is the guide's application and its storefront site on the dev stack.
type store2558 struct {
	admin    *client
	app      string
	schema   string
	api      string
	web      string
	webHost  string
	secret   string
	mailConn string
	source   string
	mail     string
}

// names rewrites the guide's chosen names to this store's.
func (s *store2558) names(src string) string {
	src = regexp.MustCompile(`\bstore\.(products|carts|orders|mails)\b`).ReplaceAllString(src, s.schema+".$1")
	for from, to := range map[string]string{
		`schemas = ["store"]`:           `schemas = ["` + s.schema + `"]`,
		"https://www.store.example":     "http://" + s.webHost,
		"/endpoints/store-api/":         "/endpoints/" + s.api + "/",
		`SIGN_IN_KEY = "store-sign-in"`: `SIGN_IN_KEY = "` + s.secret + `"`,
		`PROVIDER = "mail-provider"`:    `PROVIDER = "` + s.mailConn + `"`,
		`SEND_PATH = "/v1/send"`:        `SEND_PATH = "/v1/echo"`,
		`CONNECTION = "orders"`:         `CONNECTION = "` + conn2539 + `"`,
		`connection = "orders"`:         `connection = "` + conn2539 + `"`,
	} {
		src = strings.ReplaceAll(src, from, to)
	}
	return src
}

// siteFiles are the storefront's files as the guide writes them.
func (s *store2558) siteFiles(t *testing.T) []map[string]any {
	t.Helper()
	file := func(path, body string) map[string]any { return map[string]any{"path": path, "content": s.names(body)} }
	return []map[string]any{
		file("index.html", fence2558(t, "html", `<main id="app">`)),
		file("404.html", fence2558(t, "html", "We could not find that page.")),
		file("app.js", fence2558(t, "javascript", "app.js: the storefront")),
		file("_redirects", fence2558(t, "text", "/index.html 200")),
		file("_headers", fence2558(t, "text", "Content-Security-Policy")),
		file("robots.txt", fence2558(t, "text", "User-agent")),
		{"path": "images/tee.3f2a9c1b.png", "content": base64.StdEncoding.EncodeToString(tinyPNG2558), "encoding": "base64"},
	}
}

// newStore2558 builds the guide's application with ipLimit requests a minute
// for each address, publishes its storefront, and releases it.
func newStore2558(t *testing.T, ipLimit int) *store2558 {
	t.Helper()
	admin := connectFor(t, 5*time.Minute)
	stamp := unique1579()
	s := &store2558{
		admin: admin, app: "acc2558-" + stamp, schema: schema2539(t, "s2558"), api: "acc2558-api-" + stamp,
		web: "acc2558-web-" + stamp, mailConn: "acc2558-mail-" + stamp, source: "acc2558-outbox-" + stamp,
		mail: "acc2558-mail-" + stamp,
	}
	s.webHost = s.web + ".example.test"
	admin.call("manage_application", map[string]any{"command": "create", "name": s.app, "description": "Acceptance #2558: the public applications guide"})
	addSchema2539(admin, s.app, conn2539, s.schema)

	migration2539(admin, conn2539, s.schema, 1, fences2558(t, "sql")[0], "DROP TABLE mails; DROP TABLE orders; DROP TABLE carts; DROP TABLE products")
	admin.call("manage_schema", map[string]any{
		"command": "add", "connection": conn2539, "schema": s.schema, "version": 2, "template": "outbox", "capture": []string{"mails"},
	})
	role := s.schema + "_write"
	migration2539(admin, conn2539, s.schema, 3,
		writeRole(s.schema, "SELECT, INSERT, UPDATE", "products, carts, orders, mails")+";\nGRANT INSERT ON outbox TO "+role+
			";\nGRANT USAGE ON ALL SEQUENCES IN SCHEMA "+s.schema+" TO "+role,
		"DROP OWNED BY "+role+"; DROP ROLE "+role)
	admin.call("manage_schema", map[string]any{"command": "apply", "connection": conn2539, "schema": s.schema})
	targetPSQL(t, "INSERT INTO "+s.schema+".products VALUES ('tee', 'Tee', 20.00, 'tee.3f2a9c1b.png')")

	s.secret, _ = issue2175Secret(t, admin, nil)
	s.connect(t, apiTestBase2558)
	t.Cleanup(func() {
		admin.rest(http.MethodDelete, "/api/v1/admin/connection-instances/api/"+s.mailConn, http.NoBody)
	})

	api := s.names(fence2558(t, "python", "ROUTES = {") + "\n" + fence2558(t, "python", "def visit("))
	admin.saveScript(map[string]any{"command": "create", "name": s.api, "source": api, "description": "Acceptance #2558: the store's API"}, nil)
	admin.call("manage_application", map[string]any{"command": "add_part", "application": s.app, "kind": "script", "part_id": scriptID2540(admin, s.api)})
	bindEndpoint(t, admin, s.api, "", map[string]any{
		"endpoint": s.api, "methods": []string{"GET", "POST"}, "path": "/{route...}", "auth_mode": "public", "callers": []string{},
		"rate_limit": 600, "ip_rate_limit": ipLimit,
	})
	// The draft wrote a cart, an order and two mails; the source starts empty.
	targetPSQL(t, "DELETE FROM "+s.schema+".outbox; DELETE FROM "+s.schema+".mails")
	admin.saveScript(map[string]any{"command": "create", "name": s.mail, "source": s.names(fence2558(t, "python", "PROVIDER = ")), "description": "Acceptance #2558: the store's mail"}, nil)

	issue1870Create(t, admin, map[string]any{
		"name": s.source, "connection": issue1870Conn,
		"config": map[string]any{"input": "postgres", "postgres": map[string]any{
			"connection": conn2539, "mode": "outbox", "table": s.schema + ".outbox", "poll_seconds": 300,
		}},
	})
	if got := admin.call("manage_script", map[string]any{"command": "update", "name": s.mail, "event_source": s.source}); got["status"] != "updated" {
		t.Fatalf("naming the mail script's event source: %v", got)
	}
	admin.call("manage_script", map[string]any{"command": "schedule_set", "name": s.mail, "cron": "*/10 * * * *", "timezone": "UTC"})
	admin.call("manage_application", map[string]any{"command": "add_part", "application": s.app, "kind": "script", "part_id": scriptID2540(admin, s.mail)})
	admin.call("manage_application", map[string]any{"command": "add_part", "application": s.app, "kind": "source", "part_id": s.source})

	s.storefront(t)
	admin.call("manage_application", map[string]any{"command": "release", "application": s.app, "note": "first"})
	t.Cleanup(func() {
		_, _, _ = admin.callRaw("manage_application", map[string]any{"command": "delete", "application": s.app})
		_, _, _ = admin.callRaw("manage_site", map[string]any{"command": "delete", "site": s.web})
		_, _, _ = admin.callRaw("manage_script", map[string]any{"command": "update", "name": s.mail, "event_source": ""})
		for _, name := range []string{s.api, s.mail} {
			_, _, _ = admin.callRaw("manage_script", map[string]any{"command": "delete", "name": name})
		}
	})
	listening2408(t, admin, s.source)
	pollUntil2518(t, func() (pageResponse, bool) {
		r := onHost(t, s.webHost, http.MethodGet, "/robots.txt", nil, "")
		return r, r.status == http.StatusOK
	})
	return s
}

// storefront creates the site, publishes the guide's files and the product
// image into dev, gives prod its public hostname and makes prod a part of the
// application.
func (s *store2558) storefront(t *testing.T) {
	t.Helper()
	s.admin.call("manage_site", map[string]any{"command": "create", "name": s.web, "description": "Acceptance #2558: the storefront"})
	if got := s.admin.call("manage_site", map[string]any{"command": "publish", "site": s.web, "files": s.siteFiles(t), "note": "storefront"}); got["release"] == nil {
		t.Fatalf("publishing the storefront: %v", got)
	}
	s.admin.call("manage_site", map[string]any{"command": "add_domain", "hostname": s.webHost, "site": s.web, "environment": "prod", "public": true})
	s.admin.call("manage_application", map[string]any{"command": "add_part", "application": s.app, "kind": "site", "part_id": s.web + "/prod"})
}

// connect saves the mail connection pointing at base.
func (s *store2558) connect(t *testing.T, base string) {
	t.Helper()
	status := s.admin.restJSON(http.MethodPut, "/api/v1/admin/connection-instances/api/"+s.mailConn, map[string]any{
		"config":      map[string]any{"base_url": base, "auth_mode": "none", "connection_name": s.mailConn},
		"description": "Acceptance #2558: the store's mail provider",
	})
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("saving the mail connection: HTTP %d", status)
	}
}

// reaches waits until every replica's mail connection answers (up) or fails.
func (s *store2558) reaches(t *testing.T, up bool) {
	t.Helper()
	a, b := connectReplicaPair(t)
	waitFor(t, fmt.Sprintf("the mail connection answering=%t on both replicas", up), func() (bool, string) {
		for _, c := range []*client{a, b} {
			res, text, err := c.callRaw("api_invoke_endpoint", map[string]any{
				"connection": s.mailConn, "method": "GET", "path": "/v1/whoami", "purpose": "Acceptance #2558: is the provider reachable",
			})
			var got struct {
				Status int `json:"status"`
			}
			answered := err == nil && !res.IsError && json.Unmarshal([]byte(text), &got) == nil && got.Status == http.StatusOK
			if answered != up {
				return false, text
			}
		}
		return true, ""
	})
}

// visit is one anonymous request to the storefront's hostname.
func (s *store2558) visit(t *testing.T, method, path, cart string, body map[string]any) pageResponse {
	t.Helper()
	headers := map[string]string{}
	if cart != "" {
		headers["Cookie"] = "app-cart=" + cart
	}
	payload := ""
	if body != nil {
		raw, _ := json.Marshal(body)
		payload = string(raw)
		headers["Content-Type"] = "application/json"
		headers["Origin"] = "http://" + s.webHost
	}
	return onHost(t, s.webHost, method, path, headers, payload)
}

// call is one request to the store's API on the storefront's hostname.
func (s *store2558) call(t *testing.T, method, route, cart string, body map[string]any) pageResponse {
	t.Helper()
	return s.visit(t, method, "/endpoints/"+s.api+"/"+route, cart, body)
}

// cartCookie is the cart id a response sets.
func cartCookie2558(t *testing.T, r pageResponse) string {
	t.Helper()
	for _, c := range (&http.Response{Header: r.header}).Cookies() {
		if c.Name == "app-cart" {
			if !c.HttpOnly || c.Path != "/" {
				t.Errorf("the cart cookie is not HttpOnly on /: %+v", c)
			}
			return c.Value
		}
	}
	t.Fatalf("the response sets no cart cookie: %v", r.header.Values("Set-Cookie"))
	return ""
}

func decode2558(t *testing.T, r pageResponse) map[string]any {
	t.Helper()
	out := map[string]any{}
	if err := json.Unmarshal([]byte(r.body), &out); err != nil {
		t.Fatalf("the store answered %d %q: %v", r.status, r.body, err)
	}
	return out
}

// checkout fills a cart with two tees and checks it out for email.
func (s *store2558) checkout(t *testing.T, email string) (cart string, order map[string]any) {
	t.Helper()
	first := s.call(t, http.MethodPost, "cart", "", map[string]any{"slug": "tee"})
	if got := decode2558(t, first); fmt.Sprint(got["items"]) != "1" {
		t.Fatalf("the first add answered %v", got)
	}
	cart = cartCookie2558(t, first)
	if got := decode2558(t, s.call(t, http.MethodPost, "cart", cart, map[string]any{"slug": "tee"})); fmt.Sprint(got["items"]) != "2" {
		t.Fatalf("the cart did not survive to the second request: %v", got)
	}
	if got := decode2558(t, s.call(t, http.MethodGet, "cart", cart, nil)); fmt.Sprint(got["items"]) != "2" {
		t.Fatalf("reading the cart answered %v", got)
	}
	placed := s.call(t, http.MethodPost, "checkout", cart, map[string]any{"email": email})
	if placed.status != http.StatusOK {
		t.Fatalf("the checkout answered %d: %s", placed.status, placed.body)
	}
	return cart, decode2558(t, placed)
}

// mailState is a recipient's newest mail: whether it was sent, and the tries.
func (s *store2558) mailState(t *testing.T, recipient string) string {
	t.Helper()
	return strings.TrimSpace(targetPSQL(t, "SELECT (sent_at IS NOT NULL)::text || '|' || attempts FROM "+s.schema+
		".mails WHERE recipient = '"+recipient+"' ORDER BY id DESC LIMIT 1"))
}

// Criteria 1, 2 and 3: the storefront's routes and its API, the policy the
// site serves its pages under, and an anonymous cart and order.
func TestIssue2558_AVisitorBrowsesTheStorefrontAndChecksOutWithNoAccount(t *testing.T) {
	s := newStore2558(t, 60)

	route := s.visit(t, http.MethodGet, "/products/tee", "", nil)
	if route.status != http.StatusOK || !strings.Contains(route.body, `<main id="app">`) || route.header.Get("Location") != "" {
		t.Errorf("/products/tee answered %d (Location %q): %s", route.status, route.header.Get("Location"), route.body)
	}
	if got := decode2558(t, s.call(t, http.MethodGet, "products/tee", "", nil)); got["name"] != "Tee" || got["image"] != "tee.3f2a9c1b.png" {
		t.Errorf("the API's product answered %v", got)
	}
	robots := s.visit(t, http.MethodGet, "/robots.txt", "", nil)
	sitemapURL := "http://" + s.webHost + "/endpoints/" + s.api + "/sitemap.xml"
	if robots.status != http.StatusOK || !strings.Contains(robots.body, "Sitemap: "+sitemapURL) {
		t.Errorf("robots.txt answered %d: %s", robots.status, robots.body)
	}
	if sm := s.call(t, http.MethodGet, "sitemap.xml", "", nil); sm.status != http.StatusOK || !strings.Contains(sm.body, "<loc>http://"+s.webHost+"/products/tee</loc>") {
		t.Errorf("the sitemap answered %d: %s", sm.status, sm.body)
	}
	missing := s.visit(t, http.MethodGet, "/no/such/page", "", nil)
	if missing.status != http.StatusNotFound || !strings.Contains(missing.body, "We could not find that page.") {
		t.Errorf("an unknown path answered %d: %s", missing.status, missing.body)
	}

	policy := s.visit(t, http.MethodGet, "/checkout", "", nil).header.Get("Content-Security-Policy")
	for directive, want := range map[string][]string{
		"frame-src":   {"https://js.stripe.com", "https://*.js.stripe.com"},
		"connect-src": {"'self'", "https://api.stripe.com"},
		"script-src":  {"'self'", "https://js.stripe.com"},
	} {
		got := strings.Join(directive2554(policy, directive), " ")
		for _, origin := range want {
			if !strings.Contains(got, origin) {
				t.Errorf("the checkout page's %s %q does not name %s", directive, got, origin)
			}
		}
	}

	email := "ann-" + unique1579() + "@example.com"
	_, order := s.checkout(t, email)
	if order["caller"] != nil || order["placed_by"] != nil || fmt.Sprint(order["total"]) != "40.00" {
		t.Errorf("the order carries a platform identity or the wrong total: %v", order)
	}
	row := strings.TrimSpace(targetPSQL(t, fmt.Sprintf("SELECT email || '|' || total || '|' || coalesce(placed_by, '-') FROM %s.orders WHERE id = %v", s.schema, order["order"])))
	if row != email+"|40.00|-" {
		t.Errorf("the order row is %q", row)
	}
}

// Criteria 4 and 5: an order's mail goes to api-test through the api
// connection with the order's recipient; with the provider down the order
// still commits, and the scheduled script sends the mail once it is back.
func TestIssue2558_AnOrderIsMailedAndAMailOwedIsSentWhenTheProviderReturns(t *testing.T) {
	s := newStore2558(t, 60)

	sent := "ann-" + unique1579() + "@example.com"
	s.checkout(t, sent)
	waitFor(t, "the order's mail sent once", func() (bool, string) {
		st := s.mailState(t, sent)
		return st == "true|1", st
	})
	waitFor(t, "the send's audit row", func() (bool, string) {
		rows := s.admin.list("/api/v1/admin/audit/events?tool_name=api_invoke_endpoint&per_page=200")
		var mine []string
		for _, r := range rows {
			raw, _ := json.Marshal(r)
			if strings.Contains(string(raw), s.mailConn) && strings.Contains(string(raw), sent) {
				mine = append(mine, string(raw))
			}
		}
		return len(mine) == 1 && strings.Contains(mine[0], `"success":true`) && strings.Contains(mine[0], "/v1/echo"), strings.Join(mine, "\n")
	})

	s.connect(t, "http://127.0.0.1:1")
	s.reaches(t, false)
	owed := "bea-" + unique1579() + "@example.com"
	_, order := s.checkout(t, owed)
	if n := strings.TrimSpace(targetPSQL(t, fmt.Sprintf("SELECT count(*) FROM %s.orders WHERE id = %v", s.schema, order["order"]))); n != "1" {
		t.Fatalf("with the provider down the order did not commit: %s", n)
	}
	waitFor(t, "the failed send recorded", func() (bool, string) {
		st := s.mailState(t, owed)
		return st == "false|1", st
	})

	s.connect(t, apiTestBase2558)
	s.reaches(t, true)
	ran := s.admin.call("run_script", map[string]any{"name": s.mail, "wait_seconds": 120})
	if ran["status"] != "succeeded" {
		t.Fatalf("the scheduled mail run: %v", ran)
	}
	if st := s.mailState(t, owed); st != "true|2" {
		t.Errorf("after the provider returned, the owed mail is %q; want sent on the second try", st)
	}
}

// Criterion 6: a sign-in link opens the storefront's sign-in page and works
// once; the second use is refused.
func TestIssue2558_ASignInLinkWorksOnce(t *testing.T) {
	s := newStore2558(t, 60)
	email := "cy-" + unique1579() + "@example.com"
	if got := decode2558(t, s.call(t, http.MethodPost, "signin", "", map[string]any{"email": email})); got["sent"] != true {
		t.Fatalf("asking for a link answered %v", got)
	}
	body := strings.TrimSpace(targetPSQL(t, "SELECT body FROM "+s.schema+".mails WHERE recipient = '"+email+"' ORDER BY id DESC LIMIT 1"))
	_, after, found := strings.Cut(body, "http://"+s.webHost+"/signin?token=")
	token, _, _ := strings.Cut(after, " ")
	if !found || token == "" {
		t.Fatalf("the mail carries no link: %q", body)
	}
	if page := s.visit(t, http.MethodGet, "/signin?token="+token, "", nil); page.status != http.StatusOK || !strings.Contains(page.body, `<main id="app">`) {
		t.Fatalf("the link's page answered %d: %s", page.status, page.body)
	}
	first := s.call(t, http.MethodPost, "signin/verify", "", map[string]any{"token": token})
	if got := decode2558(t, first); first.status != http.StatusOK || got["email"] != email {
		t.Fatalf("the link's first use answered %d: %v", first.status, got)
	}
	cartCookie2558(t, first)
	second := s.call(t, http.MethodPost, "signin/verify", "", map[string]any{"token": token})
	if second.status != http.StatusForbidden || !strings.Contains(second.body, "used or has expired") {
		t.Fatalf("the link's second use answered %d: %s", second.status, second.body)
	}
}

// Criterion 7: a product image from the site's release loads at its
// fingerprinted URL with immutable, and the page's img-src allows the site's
// own address.
func TestIssue2558_AProductImageIsServedImmutableFromTheSite(t *testing.T) {
	s := newStore2558(t, 60)
	product := s.visit(t, http.MethodGet, "/products/tee", "", nil)
	if got := strings.Join(directive2554(product.header.Get("Content-Security-Policy"), "img-src"), " "); !strings.Contains(got, "'self'") {
		t.Errorf("the page's img-src %q does not allow the site's own address", got)
	}
	img := pollUntil2518(t, func() (pageResponse, bool) {
		r := s.visit(t, http.MethodGet, "/images/tee.3f2a9c1b.png", "", nil)
		return r, r.status == http.StatusOK
	})
	if got := img.header.Get("Cache-Control"); !strings.Contains(got, "immutable") || !strings.HasPrefix(img.header.Get("Content-Type"), "image/png") {
		t.Errorf("the image answered Cache-Control %q, Content-Type %q", got, img.header.Get("Content-Type"))
	}
}

// Criterion 8: behind a trusted proxy, the client over ip_rate_limit is
// refused and another client is not. The platform trusting the proxy is a
// second process on the dev stack's database with a configuration of its own,
// which names no application database connection, so the criterion asks the
// API for a path it answers without a query.
func TestIssue2558_TheClientOverItsLimitIsRefusedAndAnotherIsNot(t *testing.T) {
	s := newStore2558(t, 3)
	p := trusting2556(t)
	path := "/endpoints/" + s.api + "/nowhere"
	for range 3 {
		if status, _, _ := ask2556(t, p.base, s.webHost, path, "203.0.113.40"); status == http.StatusTooManyRequests {
			t.Fatal("a request within the limit was refused")
		}
	}
	if status, _, header := ask2556(t, p.base, s.webHost, path, "203.0.113.40"); status != http.StatusTooManyRequests || header.Get("Retry-After") == "" {
		t.Fatalf("the fourth request from one client answered %d (Retry-After %q), want 429", status, header.Get("Retry-After"))
	}
	if status, _, _ := ask2556(t, p.base, s.webHost, path, "203.0.113.41"); status == http.StatusTooManyRequests {
		t.Fatal("another client was refused; want its own limit")
	}
}

// The apex hostname: a site holding only the guide's redirect map sends every
// path, with its rest and its query string, to the storefront's hostname.
func TestIssue2558_TheApexRedirectsToTheStorefrontKeepingThePath(t *testing.T) {
	admin := connect(t)
	stamp := unique1579()
	webHost := "acc2558-web-" + stamp + ".example.test"
	apex, apexHost := "acc2558-apex-"+stamp, "acc2558-apex-"+stamp+".example.test"
	s := &store2558{webHost: webHost}
	admin.call("manage_site", map[string]any{"command": "create", "name": apex, "description": "Acceptance #2558: the apex"})
	t.Cleanup(func() { _, _, _ = admin.callRaw("manage_site", map[string]any{"command": "delete", "site": apex}) })
	rules := s.names(fence2558(t, "text", ":splat 301"))
	files := []map[string]any{{"path": "_redirects", "content": rules}, {"path": "index.html", "content": "apex"}}
	if got := admin.call("manage_site", map[string]any{"command": "publish", "site": apex, "files": files}); got["release"] == nil {
		t.Fatalf("publishing the apex: %v", got)
	}
	admin.call("manage_site", map[string]any{"command": "add_domain", "hostname": apexHost, "site": apex, "environment": "prod", "public": true})
	admin.call("manage_site", map[string]any{"command": "promote", "site": apex, "from": "dev", "to": "prod"})
	got := pollUntil2518(t, func() (pageResponse, bool) {
		r := onHost(t, apexHost, http.MethodGet, "/products/tee?ref=ad", nil, "")
		return r, r.status == http.StatusMovedPermanently
	})
	if want := "http://" + webHost + "/products/tee?ref=ad"; got.header.Get("Location") != want {
		t.Errorf("the apex sent /products/tee?ref=ad to %q, want %q", got.header.Get("Location"), want)
	}
}

// tinyPNG2558 is a one-pixel PNG.
var tinyPNG2558 = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
	0x89, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0xf8, 0xcf, 0xc0, 0xf0,
	0x1f, 0x00, 0x05, 0x00, 0x01, 0xff, 0x89, 0x99, 0x3d, 0x1d, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45,
	0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
}
