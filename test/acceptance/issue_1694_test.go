//go:build integration

package acceptance

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/lib/pq" // the escalation criterion backdates one row directly
)

// Issue #1694: when an upstream rejected a connection's refresh, the platform
// discarded the credential, wrote the auth event, changed the OAuth status
// card, and told nobody. Both of those surfaces are pull: they answer an
// operator who already knows to ask. The person who authorized the connection
// is deliberately not the person using it day to day, so that person is not
// watching the card; on a scheduled run nobody is watching anything and the
// empty answer reads like data rather than like an outage.
//
// The criteria below are the push as the two people involved meet it: the
// operator who authorized the connection is emailed the moment the credential
// is discarded, once per revocation rather than once per rejected call; the
// escalation reaches the addresses an administrator named when nobody has come
// back to it; and the administrator can read and write that configuration.
//
// Every criterion runs against the running platform through the routes a
// person uses: the connection editor's save, the Connect button's oauth-start
// and callback, the status card's Reacquire, the settings page's alert
// section, and the admin Notifications tab. The upstream is a real HTTP token
// endpoint this test serves, so the rejection is a real RFC 6749 response over
// the wire rather than a stubbed error.
//
// Wire forms: these are admin REST routes with typed JSON bodies, so each
// touched field admits exactly ONE form and is sent as a literal request body:
// `enabled` is a bool, `escalate_after_hours` is a number, and `recipients` is
// an array of strings. The connection config fields (`base_url`, `auth_mode`,
// `oauth_grant`, `oauth_authorization_url`, `oauth_token_url`,
// `oauth_client_id`, `oauth_client_secret`) are strings. The oauth-start body's
// `return_url` is a string. The callback's `code` and `state` are query
// parameters, the one form a query admits.

const (
	issue1694Path = "/api/v1/admin/settings/connection-alert"
	// issue1694Operator is the identity the dev administrator's API key
	// authenticates as, and so the identity every connection it authorizes is
	// recorded against. It is the address the first alert must reach.
	issue1694Operator = "admin@example.com"
	// issue1694Escalation is the address an administrator names to hear about
	// a revocation nobody has acted on.
	issue1694Escalation = "platform-oncall@example.com"
)

func issue1694Name(label string) string {
	return fmt.Sprintf("acc-1694-%s-%d", label, time.Now().UnixNano())
}

// fakeUpstream is the OAuth token endpoint the connection authenticates
// against. It issues a credential on the authorization-code exchange, and
// rejects the refresh once the test flips it, which is the incident.
type fakeUpstream struct {
	server  *httptest.Server
	rejects atomic.Bool
}

func newFakeUpstream(t *testing.T) *fakeUpstream {
	t.Helper()
	up := &fakeUpstream{}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		if up.rejects.Load() && r.PostFormValue("grant_type") == "refresh_token" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"Refresh token revoked"}`))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"acc-1694-at","refresh_token":"acc-1694-rt",` +
			`"expires_in":3600,"token_type":"Bearer"}`))
	})
	up.server = httptest.NewServer(mux)
	t.Cleanup(up.server.Close)
	return up
}

func (u *fakeUpstream) tokenURL() string { return u.server.URL + "/token" }

// issue1694Connect creates an OAuth-backed api connection and authorizes it
// through the real oauth-start and callback pair, returning its name. The
// credential it leaves behind is what a revocation then discards.
func issue1694Connect(t *testing.T, c *client, up *fakeUpstream, label string) string {
	t.Helper()
	name := issue1694Name(label)
	path := "/api/v1/admin/connection-instances/api/" + name
	t.Cleanup(func() { c.rest(http.MethodDelete, path, http.NoBody) })

	status, body := c.rest(http.MethodPut, path, jsonBody(t, map[string]any{
		"config": map[string]any{
			"base_url":                "https://upstream.example.com",
			"auth_mode":               "oauth",
			"oauth_grant":             "authorization_code",
			"oauth_authorization_url": up.server.URL + "/authorize",
			"oauth_token_url":         up.tokenURL(),
			"oauth_client_id":         "acceptance-1694",
			"oauth_client_secret":     "acceptance-1694-secret",
			"connection_name":         name,
		},
		"description": "Acceptance for #1694: a connection whose upstream revokes its refresh.",
	}))
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("saving the connection: HTTP %d (%v)", status, body)
	}

	status, body = c.rest(http.MethodPost,
		"/api/v1/admin/connections/api/"+name+"/oauth-start",
		jsonBody(t, map[string]any{"return_url": "/portal/admin/connections"}))
	if status != http.StatusOK {
		t.Fatalf("starting the OAuth flow: HTTP %d (%v)", status, body)
	}
	state, _ := body["state"].(string)
	if state == "" {
		t.Fatalf("oauth-start returned no state: %v", body)
	}

	// The callback is the public route the upstream redirects the operator's
	// browser to. It answers 302, which is not followed here: the redirect
	// target is a portal page, and what is under test is the credential the
	// callback persisted.
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	req, err := http.NewRequestWithContext(c.ctx, http.MethodGet,
		c.base+"/api/v1/admin/oauth/callback?code=acc-1694-code&state="+url.QueryEscape(state), http.NoBody)
	if err != nil {
		t.Fatalf("building the callback request: %v", err)
	}
	res, err := noRedirect.Do(req)
	if err != nil {
		t.Fatalf("the OAuth callback: %v", err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after read
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusFound {
		t.Fatalf("the OAuth callback answered HTTP %d: %s", res.StatusCode, string(raw))
	}

	status, body = c.rest(http.MethodGet,
		"/api/v1/admin/connections/api/"+name+"/oauth-status", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading the OAuth status: HTTP %d (%v)", status, body)
	}
	if acquired, _ := body["token_acquired"].(bool); !acquired {
		t.Fatalf("the connection holds no credential after Connect: %v", body)
	}
	return name
}

// issue1694Revoke forces the refresh the upstream now rejects, through the
// status card's own Reacquire route, and asserts the platform reached the
// verdict the ticket is about.
func issue1694Revoke(t *testing.T, c *client, name string) {
	t.Helper()
	status, body := c.rest(http.MethodPost,
		"/api/v1/admin/connections/api/"+name+"/reacquire-oauth", http.NoBody)
	if status != http.StatusConflict {
		t.Fatalf("reacquire against a rejecting upstream: HTTP %d (%v); want 409 needs-reauth", status, body)
	}
}

// issue1694Alerts returns the queued connection-revocation notifications for
// one recipient, newest first, as the admin Notifications tab reads them.
type issue1694Row struct {
	Recipient string `json:"recipient"`
	Category  string `json:"category"`
	Subject   string `json:"subject"`
	ItemTitle string `json:"item_title"`
	Link      string `json:"link"`
}

func issue1694Alerts(t *testing.T, c *client, recipient string) []issue1694Row {
	t.Helper()
	status, body := c.rest(http.MethodGet,
		"/api/v1/admin/notifications?category=connection_auth&recipient="+
			url.QueryEscape(recipient)+"&per_page=200", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading the notification history: HTTP %d (%v)", status, body)
	}
	raw, err := json.Marshal(body["data"])
	if err != nil {
		t.Fatalf("re-encoding the notification page: %v", err)
	}
	var rows []issue1694Row
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("decoding the notification page: %v", err)
	}
	return rows
}

// issue1694AwaitAlert waits for a queued alert naming the connection, since the
// enqueue happens on the refresh path the HTTP response has already returned
// from.
func issue1694AwaitAlert(t *testing.T, c *client, recipient, name string, within time.Duration) []issue1694Row {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		var found []issue1694Row
		for _, row := range issue1694Alerts(t, c, recipient) {
			if row.ItemTitle == name {
				found = append(found, row)
			}
		}
		if len(found) > 0 || time.Now().After(deadline) {
			return found
		}
		time.Sleep(time.Second)
	}
}

// TestIssue1694_TheOperatorWhoAuthorizedItIsTold is the ticket's central
// criterion: the moment the upstream's rejection discards the credential, the
// identity that authorized the connection is emailed, and the mail names the
// connection and points at the page that fixes it.
func TestIssue1694_TheOperatorWhoAuthorizedItIsTold(t *testing.T) {
	c := connect(t)
	issue1694SetAlert(t, c, true, 24, []string{})

	up := newFakeUpstream(t)
	name := issue1694Connect(t, c, up, "told")
	up.rejects.Store(true)
	issue1694Revoke(t, c, name)

	rows := issue1694AwaitAlert(t, c, issue1694Operator, name, 30*time.Second)
	if len(rows) == 0 {
		t.Fatalf("nothing was queued for %s after %s was revoked", issue1694Operator, name)
	}
	row := rows[0]
	if row.Category != "connection_auth" {
		t.Errorf("category = %q; want connection_auth", row.Category)
	}
	if !strings.Contains(row.Subject, name) {
		t.Errorf("the subject does not name the connection: %q", row.Subject)
	}
	if !strings.Contains(strings.ToLower(row.Subject), "reauthorized") {
		t.Errorf("the subject does not say what the connection needs: %q", row.Subject)
	}
	if !strings.Contains(row.Link, "/admin/connections") {
		t.Errorf("the alert does not link to the page that fixes it: %q", row.Link)
	}
}

// TestIssue1694_OneAlertPerRevocation is the de-duplication criterion: a
// connection that keeps being called is announced once, not once per rejected
// call.
func TestIssue1694_OneAlertPerRevocation(t *testing.T) {
	c := connect(t)
	issue1694SetAlert(t, c, true, 24, []string{})

	up := newFakeUpstream(t)
	name := issue1694Connect(t, c, up, "once")
	up.rejects.Store(true)
	issue1694Revoke(t, c, name)

	if rows := issue1694AwaitAlert(t, c, issue1694Operator, name, 30*time.Second); len(rows) != 1 {
		t.Fatalf("the first revocation queued %d alerts; want exactly 1", len(rows))
	}

	// The credential is gone, so every later call comes back needing
	// reauthorization. None of them is news.
	for range 3 {
		status, _ := c.rest(http.MethodPost,
			"/api/v1/admin/connections/api/"+name+"/reacquire-oauth", http.NoBody)
		if status != http.StatusConflict {
			t.Fatalf("a later reacquire answered HTTP %d; want 409", status)
		}
	}
	time.Sleep(3 * time.Second)
	if rows := issue1694Alerts(t, c, issue1694Operator); issue1694CountFor(rows, name) != 1 {
		t.Fatalf("%d alerts for one revocation; want exactly 1", issue1694CountFor(rows, name))
	}
}

// TestIssue1694_ReauthorizingMakesTheNextRevocationNews is the other half of
// de-duplication: the row is closed when the connection is authorized again,
// so a connection that lapses twice is reported twice.
func TestIssue1694_ReauthorizingMakesTheNextRevocationNews(t *testing.T) {
	c := connect(t)
	issue1694SetAlert(t, c, true, 24, []string{})

	up := newFakeUpstream(t)
	name := issue1694Connect(t, c, up, "again")
	up.rejects.Store(true)
	issue1694Revoke(t, c, name)
	if rows := issue1694AwaitAlert(t, c, issue1694Operator, name, 30*time.Second); len(rows) != 1 {
		t.Fatalf("the first revocation queued %d alerts; want 1", len(rows))
	}

	// Reconnect through the same routes an operator uses, then lapse again.
	up.rejects.Store(false)
	issue1694Reconnect(t, c, name)
	up.rejects.Store(true)
	issue1694Revoke(t, c, name)

	deadline := time.Now().Add(30 * time.Second)
	for {
		got := issue1694CountFor(issue1694Alerts(t, c, issue1694Operator), name)
		if got == 2 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d alerts after two separate revocations; want 2", got)
		}
		time.Sleep(time.Second)
	}
}

// TestIssue1694_ARevocationNobodyActedOnIsEscalated is the second half of the
// ticket: the first alert's recipient can be unreachable for days, which is the
// premise the whole feature rests on, so a revocation still open after the
// administrator's window reaches the addresses they named.
func TestIssue1694_ARevocationNobodyActedOnIsEscalated(t *testing.T) {
	c := connect(t)
	issue1694SetAlert(t, c, true, 1, []string{issue1694Escalation})
	t.Cleanup(func() { issue1694SetAlert(t, c, true, 24, []string{}) })

	up := newFakeUpstream(t)
	name := issue1694Connect(t, c, up, "escalated")
	up.rejects.Store(true)
	issue1694Revoke(t, c, name)
	if rows := issue1694AwaitAlert(t, c, issue1694Operator, name, 30*time.Second); len(rows) != 1 {
		t.Fatalf("the revocation queued %d alerts for the operator; want 1", len(rows))
	}

	// The window is the one thing no surface can drive: it is wall-clock time.
	// Moving the revocation into the past is the same as waiting out the hour,
	// and everything after it -- the sweep, the claim, the enqueue -- is the
	// running platform's own.
	issue1694Backdate(t, name, 3*time.Hour)

	rows := issue1694AwaitAlert(t, c, issue1694Escalation, name, 150*time.Second)
	if len(rows) == 0 {
		t.Fatalf("nothing reached %s for a revocation nobody acted on", issue1694Escalation)
	}
	if !strings.Contains(rows[0].Subject, name) {
		t.Errorf("the escalation does not name the connection: %q", rows[0].Subject)
	}
	if !strings.Contains(strings.ToLower(rows[0].Subject), "still") {
		t.Errorf("the escalation reads like the first alert rather than like a connection nobody came back to: %q",
			rows[0].Subject)
	}

	// And it is raised once, not once per sweep.
	time.Sleep(65 * time.Second)
	if got := issue1694CountFor(issue1694Alerts(t, c, issue1694Escalation), name); got != 1 {
		t.Fatalf("%d escalations for one revocation; want exactly 1", got)
	}
}

// TestIssue1694_TheAdministratorConfiguresWhoIsTold is the configuration
// criterion: the settings page's section reads and writes through a real route,
// answers with the defaults before anybody has written one, and says plainly
// when it will escalate nowhere.
func TestIssue1694_TheAdministratorConfiguresWhoIsTold(t *testing.T) {
	c := connect(t)
	t.Cleanup(func() { issue1694SetAlert(t, c, true, 24, []string{}) })

	view := issue1694SetAlert(t, c, true, 24, []string{})
	warnings := issue1694Warnings(view)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "only the person who authorized") {
		t.Errorf("an alert that escalates nowhere does not say so: %v", warnings)
	}

	view = issue1694SetAlert(t, c, true, 6, []string{"Oncall <ONCALL@Example.com>", "oncall@example.com"})
	if hours, _ := view["escalate_after_hours"].(float64); hours != 6 {
		t.Errorf("escalate_after_hours = %v; want the 6 that was written", view["escalate_after_hours"])
	}
	recipients, _ := view["recipients"].([]any)
	if len(recipients) != 1 || recipients[0] != "oncall@example.com" {
		t.Errorf("recipients = %v; one person listed in two shapes is one recipient", recipients)
	}
	if len(issue1694Warnings(view)) != 0 {
		t.Errorf("a configured escalation still warns: %v", issue1694Warnings(view))
	}

	status, body := c.rest(http.MethodPut, issue1694Path, jsonBody(t, map[string]any{
		"enabled": true, "escalate_after_hours": 6, "recipients": []string{"not-an-address"},
	}))
	if status != http.StatusBadRequest {
		t.Errorf("an unparseable address was accepted: HTTP %d (%v)", status, body)
	}

	status, body = c.rest(http.MethodPut, issue1694Path, jsonBody(t, map[string]any{
		"enabled": true, "escalate_after_hours": 100000, "recipients": []string{},
	}))
	if status != http.StatusBadRequest {
		t.Errorf("an out-of-range window was accepted: HTTP %d (%v)", status, body)
	}
}

// issue1694SetAlert writes the alert configuration through the admin route and
// returns the stored view it answers with.
func issue1694SetAlert(t *testing.T, c *client, enabled bool, hours int, recipients []string) map[string]any {
	t.Helper()
	status, body := c.rest(http.MethodPut, issue1694Path, jsonBody(t, map[string]any{
		"enabled": enabled, "escalate_after_hours": hours, "recipients": recipients,
	}))
	if status != http.StatusOK {
		t.Fatalf("writing the alert settings: HTTP %d (%v)", status, body)
	}
	return body
}

func issue1694Warnings(view map[string]any) []string {
	raw, _ := view["warnings"].([]any)
	out := make([]string, 0, len(raw))
	for _, w := range raw {
		out = append(out, fmt.Sprint(w))
	}
	return out
}

func issue1694CountFor(rows []issue1694Row, name string) int {
	n := 0
	for _, row := range rows {
		if row.ItemTitle == name {
			n++
		}
	}
	return n
}

// issue1694Reconnect authorizes an existing connection again through the same
// oauth-start and callback pair the Connect button drives.
func issue1694Reconnect(t *testing.T, c *client, name string) {
	t.Helper()
	status, body := c.rest(http.MethodPost,
		"/api/v1/admin/connections/api/"+name+"/oauth-start",
		jsonBody(t, map[string]any{"return_url": "/portal/admin/connections"}))
	if status != http.StatusOK {
		t.Fatalf("restarting the OAuth flow: HTTP %d (%v)", status, body)
	}
	state, _ := body["state"].(string)
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	req, err := http.NewRequestWithContext(c.ctx, http.MethodGet,
		c.base+"/api/v1/admin/oauth/callback?code=acc-1694-code&state="+url.QueryEscape(state), http.NoBody)
	if err != nil {
		t.Fatalf("building the callback request: %v", err)
	}
	res, err := noRedirect.Do(req)
	if err != nil {
		t.Fatalf("the OAuth callback: %v", err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after read
	_, _ = io.Copy(io.Discard, res.Body)
	if res.StatusCode != http.StatusFound {
		t.Fatalf("the OAuth callback answered HTTP %d on reconnect", res.StatusCode)
	}
}

// issue1694DevDSN is where the dev stack's database listens. dev/start.sh
// relocates the stack when the default ports are busy and records the ports in
// dev/.dev-ports.env, which `make acceptance` sources into the environment.
func issue1694DevDSN() string {
	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		return dsn
	}
	port := os.Getenv("DEV_PG_PORT")
	if port == "" {
		port = "5432"
	}
	return "postgres://platform:platform_secret@localhost:" + port + "/mcp_platform?sslmode=disable"
}

// issue1694Backdate moves one open revocation into the past, which is the only
// part of the escalation no surface can drive: the window it waits out is
// wall-clock time. Everything the criterion is actually about -- the sweep
// finding it, claiming it once, and mailing the configured addresses -- is the
// running platform's own work.
func issue1694Backdate(t *testing.T, name string, by time.Duration) {
	t.Helper()
	dsn := issue1694DevDSN()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("opening the dev database at %s: %v", dsn, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("the dev database does not answer at %s (%v). The suite needs the local stack: `make dev`", dsn, err)
	}
	res, err := db.ExecContext(ctx,
		`UPDATE connection_auth_alerts SET revoked_at = revoked_at - $1::interval
		  WHERE connection_kind = 'api' AND connection_name = $2`,
		fmt.Sprintf("%d seconds", int(by.Seconds())), name)
	if err != nil {
		t.Fatalf("backdating the open revocation: %v", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		t.Fatalf("reading the backdate result: %v", err)
	}
	if affected != 1 {
		t.Fatalf("backdated %d rows for %s; the revocation was never recorded", affected, name)
	}
}
