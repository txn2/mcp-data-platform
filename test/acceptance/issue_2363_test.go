//go:build integration

package acceptance

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	s3client "github.com/txn2/mcp-s3/pkg/client"

	"github.com/txn2/mcp-data-platform/pkg/platform/fieldcrypt"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// Acceptance for #2363: a secret the platform is handed is never written
// anywhere it can be read back. A canary unique to the run is planted in every
// field the platform treats as secret -- a stored secret, each sensitive key
// of each connection kind's schema, every webhook auth mode and sender preset,
// a form's sensitive field and a token key for platform.mint_token -- and each
// surface that handles them is driven through the running platform, in its
// success and its failure paths. Then every place the platform writes is
// searched for each planted value, as written, base64, hex, URL-escaped and
// JSON-escaped: every table in the platform database, every object written to
// the platform's buckets during the run, both replicas' logs, the spans and
// log records the stack's collector received, and every response body the
// run read.
//
// The fields come from the code -- fieldcrypt's key lists matched against each
// kind's config schema, whsource's auth modes and presets (through #2354's
// senders) -- so a field or a mode added later is planted without editing this
// test.
//
// Wire forms: the admin routes take one JSON object; api_invoke_endpoint's
// body is sent as an object and as a string of JSON; a webhook request is the
// content type its sender posts; a form submission is sent urlencoded and as
// JSON; run_draft takes a string source.

// canary2363 is the run's planted values and what was read back.
type canary2363 struct {
	t       *testing.T
	stamp   string
	started time.Time
	// values maps each planted value to the field it was planted in.
	values map[string]string
	// returned are values the platform legitimately hands back to the one
	// caller who asked (a minted token): searched everywhere but the
	// responses.
	returned map[string]string
	// responses are the bodies the run read, labeled by request.
	responses []sink2363
	// marks are where each log and collector file ended when the run began.
	marks map[string]int64
	// control is a value written into a field the platform stores in clear
	// (a connection's description), which the search must find in the
	// database and in the response that read it back; found is where it was.
	control string
	found   map[string]bool
}

type sink2363 struct {
	where string
	data  []byte
}

func newCanary2363(t *testing.T) *canary2363 {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	c := &canary2363{
		t: t, stamp: "cny" + hex.EncodeToString(b), started: time.Now().Add(-time.Minute),
		values: map[string]string{}, returned: map[string]string{}, marks: map[string]int64{},
		found: map[string]bool{},
	}
	c.control = c.stamp + "-control-Zq7"
	for _, f := range c.fileSinks() {
		if st, err := os.Stat(f); err == nil {
			c.marks[f] = st.Size()
		}
	}
	return c
}

// plant returns a value for field, unique to the run and to the field.
func (c *canary2363) plant(field string) string {
	v := c.stamp + "-" + strings.NewReplacer("/", "-", "_", "-", ".", "-").Replace(field) + "-Zq7"
	c.values[v] = field
	return v
}

// know records a value planted under a fixed form (a webhook preset's secret
// must look like the sender's own).
func (c *canary2363) know(field, value string) {
	if value != "" {
		c.values[value] = field
	}
}

// read records a response body the run read.
func (c *canary2363) read(where string, body []byte) {
	c.responses = append(c.responses, sink2363{where: where, data: append([]byte(nil), body...)})
}

// forms2363 are the ways a value can be written down.
func forms2363(v string) []string {
	j, _ := json.Marshal(v)
	out := []string{
		v,
		base64.StdEncoding.EncodeToString([]byte(v)),
		base64.RawStdEncoding.EncodeToString([]byte(v)),
		base64.URLEncoding.EncodeToString([]byte(v)),
		base64.RawURLEncoding.EncodeToString([]byte(v)),
		hex.EncodeToString([]byte(v)),
		strings.ToUpper(hex.EncodeToString([]byte(v))),
		url.QueryEscape(v),
		url.PathEscape(v),
		strings.Trim(string(j), `"`),
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// rest issues an admin REST request as c and records the raw response.
func (k *canary2363) rest(c *client, method, path string, body any) (int, []byte) {
	k.t.Helper()
	var reader io.Reader = http.NoBody
	if body != nil {
		reader = jsonBody(k.t, body)
	}
	req, err := http.NewRequestWithContext(c.ctx, method, c.base+path, reader)
	if err != nil {
		k.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		k.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close() //nolint:errcheck // read-only
	raw, _ := io.ReadAll(res.Body)
	k.read(method+" "+path, raw)
	return res.StatusCode, raw
}

// call invokes a tool and records its result, error or not.
func (k *canary2363) call(c *client, name string, args map[string]any) (bool, string) {
	k.t.Helper()
	res, text, err := c.callRaw(name, args)
	if err != nil {
		k.read("tools/call "+name+" (transport)", []byte(err.Error()))
		return false, err.Error()
	}
	raw, _ := json.Marshal(res)
	k.read("tools/call "+name, raw)
	return !res.IsError, text
}

func TestIssue2363_NoPlantedSecretReachesAnySink(t *testing.T) {
	admin := connectFor(t, 15*time.Minute)
	k := newCanary2363(t)

	k.driveStoredSecret(admin)
	k.driveConnections(admin)
	sources := k.driveWebhooks(admin)
	k.driveForm(admin)
	k.driveTokenKey(admin)

	k.awaitWriters(admin)

	k.searchWebhookTables(admin, sources)
	k.searchDatabase()
	k.searchBuckets()
	k.searchFiles()
	k.searchResponses()
	// The search finds what is there: the control was found where the
	// platform keeps it in clear.
	for _, sink := range []string{"table", "response"} {
		if !k.found[sink] {
			t.Errorf("the control value was not found in any %s; the search would miss a leak there", sink)
		}
	}
	t.Logf("planted %d values; searched %d responses", len(k.values)+len(k.returned), len(k.responses))
}

// awaitWriters makes one last call and waits until its audit row is stored
// and its span reached the collector: every asynchronous writer has then had
// the calls made before it.
func (k *canary2363) awaitWriters(admin *client) {
	t := k.t
	count := func() (spans, rows int) {
		for _, sp := range readSpans(t) {
			if isToolCallSpan(sp) && sp.Attrs["mcp.session_id"] == admin.sessionID && sp.Attrs["mcp.tool"] == "list_connections" {
				spans++
			}
		}
		rows = len(admin.list("/api/v1/admin/audit/events?tool_name=list_connections&per_page=200&session_id=" + admin.sessionID))
		return spans, rows
	}
	spans0, rows0 := count()
	k.call(admin, "list_connections", map[string]any{})
	deadline := time.Now().Add(2 * time.Minute)
	for {
		spans, rows := count()
		if spans > spans0 && rows > rows0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the last call's span (%d to %d) or audit row (%d to %d) did not arrive", spans0, spans, rows0, rows)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// driveStoredSecret stores a secret through the admin route and through the
// platform-admin connection, reads it back, and sends it through an api
// connection by its placeholder from a call and from a script.
func (k *canary2363) driveStoredSecret(admin *client) {
	t := k.t
	name := "acc2363_" + k.stamp
	value := k.plant("stored-secret")
	if status, body := k.rest(admin, http.MethodPut, "/api/v1/admin/secrets/"+name, map[string]any{
		"description": "Acceptance #2363", "value": value, "allow_connections": []string{fixture2051},
	}); status != http.StatusCreated {
		t.Fatalf("creating the secret answered %d %s", status, body)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/secrets/"+name, http.NoBody) })
	k.rest(admin, http.MethodGet, "/api/v1/admin/secrets", nil)
	k.rest(admin, http.MethodGet, "/api/v1/admin/secrets/"+name, nil)

	// The same route through the platform-admin connection, with the body as
	// an object and as a string of JSON: an audited tool call.
	for i, body := range []any{
		map[string]any{"description": "Acceptance #2363", "value": k.plant("stored-secret-via-tool-object")},
		`{"description": "Acceptance #2363", "value": "` + k.plant("stored-secret-via-tool-string") + `"}`,
	} {
		other := fmt.Sprintf("acc2363_t%d_%s", i, k.stamp)
		k.call(admin, "api_invoke_endpoint", map[string]any{
			"connection": "platform-admin", "method": http.MethodPut, "path": "/api/v1/admin/secrets/" + other,
			"body": body, "purpose": "Acceptance #2363: a secret stored through the platform-admin connection.",
		})
		t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/secrets/"+other, http.NoBody) })
	}

	ph := "{{secret:" + name + "}}"
	for _, body := range []any{map[string]any{"text": ph}, `{"text": "` + ph + `"}`} {
		k.call(admin, "api_invoke_endpoint", map[string]any{
			"connection": fixture2051, "method": http.MethodPost, "path": "/v1/echo", "body": body,
			"headers": map[string]string{"X-Login": ph}, "query_params": map[string]any{"login": ph},
			"purpose": "Acceptance #2363: a stored secret sent by its placeholder.",
		})
	}
	source := strings.Replace(script2051, "%s", name, 1)
	if ok, text := k.call(admin, "manage_script", map[string]any{
		"command": "run_draft", "name": "acceptance-2363-" + k.stamp, "source": source, "allow_writes": true,
	}); !ok {
		t.Fatalf("the draft sending the secret failed: %s", text)
	}
}

// baseConfig2363 is a connection of each kind pointed at an address nothing
// answers, so every call through it takes the failure path.
func baseConfig2363(kind string) map[string]any {
	switch kind {
	case "trino":
		return map[string]any{"host": "127.0.0.1", "port": 9, "user": "acc2363", "ssl": true}
	case "s3":
		return map[string]any{"endpoint": "http://127.0.0.1:9", "region": "us-east-1", "use_path_style": true, "access_key_id": "acc2363"}
	case "mcp":
		return map[string]any{"endpoint": "http://127.0.0.1:9/mcp", "connect_timeout": "2s", "call_timeout": "5s"}
	case "api":
		return map[string]any{"base_url": "http://127.0.0.1:9", "connect_timeout": "2s", "call_timeout": "5s"}
	case "graphql":
		return map[string]any{"endpoint_url": "http://127.0.0.1:9/graphql"}
	}
	return map[string]any{}
}

// sensitiveKeys2363 are the keys of kind's config schema the encryption layer
// treats as secret, flat and nested, in a stable order.
func sensitiveKeys2363(t *testing.T, kind string) (flat, nested []string) {
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(registry.ConnectionConfigSchema(kind), &schema); err != nil {
		t.Fatalf("%s's config schema: %v", kind, err)
	}
	for _, key := range fieldcrypt.SensitiveConfigKeyList() {
		if _, ok := schema.Properties[key]; ok {
			flat = append(flat, key)
		}
	}
	for _, key := range fieldcrypt.SensitiveNestedMapKeyList() {
		if _, ok := schema.Properties[key]; ok {
			nested = append(nested, key)
		}
	}
	sort.Strings(flat)
	sort.Strings(nested)
	return flat, nested
}

// driveConnections saves one connection per sensitive key of each kind, reads
// it back, tests it, and calls through one of each kind.
func (k *canary2363) driveConnections(admin *client) {
	t := k.t
	planted := 0
	for _, kind := range []string{"trino", "s3", "mcp", "api", "graphql"} {
		flat, nested := sensitiveKeys2363(t, kind)
		if len(flat)+len(nested) == 0 {
			t.Errorf("%s's config schema names no key the encryption layer treats as secret", kind)
			continue
		}
		saved := ""
		for _, key := range append(flat, nested...) {
			cfg := baseConfig2363(kind)
			value := k.plant(kind + "-" + key)
			switch {
			case slices.Contains(nested, key):
				cfg[key] = map[string]any{"X-Acc-2363": value}
			case key == "google_service_account_json":
				// The key file's shape, with the canary as its private key.
				cfg[key] = `{"type":"service_account","project_id":"acc-2363","private_key_id":"k1",` +
					`"private_key":"` + value + `","client_email":"acc-2363@acc-2363.iam.gserviceaccount.com",` +
					`"token_uri":"https://oauth2.googleapis.com/token"}`
				cfg["oauth_scope"] = "https://www.googleapis.com/auth/cloud-platform"
			default:
				cfg[key] = value
			}
			name := strings.ToLower("acc-2363-" + kind + "-" + strings.ReplaceAll(key, "_", "-") + "-" + k.stamp[3:9])
			path := "/api/v1/admin/connection-instances/" + kind + "/" + name
			status, body := k.rest(admin, http.MethodPut, path, map[string]any{"config": cfg, "description": "Acceptance #2363 " + k.control})
			if status != http.StatusOK && status != http.StatusCreated {
				t.Logf("%s/%s with only %s was refused %d: %s", kind, name, key, status, body)
				continue
			}
			planted++
			t.Cleanup(func() { admin.rest(http.MethodDelete, path, http.NoBody) })
			k.rest(admin, http.MethodGet, path, nil)
			k.rest(admin, http.MethodPost, path+"/test", map[string]any{})
			if saved == "" {
				saved = name
			}
		}
		if saved == "" {
			t.Errorf("no %s connection carrying a secret could be saved", kind)
			continue
		}
		k.rest(admin, http.MethodGet, "/api/v1/admin/connection-instances", nil)
		k.rest(admin, http.MethodGet, "/api/v1/admin/connection-instances/effective", nil)
		k.callThrough(admin, kind, saved)
	}
	if planted == 0 {
		t.Fatal("no connection carrying a secret was saved")
	}
}

// callThrough sends one call through a saved connection of kind; every one
// fails at the unreachable address.
func (k *canary2363) callThrough(admin *client, kind, name string) {
	purpose := "Acceptance #2363: a call through a connection holding a secret."
	switch kind {
	case "trino":
		k.call(admin, "trino_query", map[string]any{"connection": name, "sql": "SELECT 1", "purpose": purpose})
	case "s3":
		k.call(admin, "s3_list", map[string]any{"connection": name, "bucket": "acc-2363", "purpose": purpose})
	case "api":
		k.call(admin, "api_invoke_endpoint", map[string]any{"connection": name, "method": http.MethodGet, "path": "/v1/echo", "purpose": purpose})
	case "graphql":
		k.call(admin, "graphql_query", map[string]any{"connection": name, "query": "{ __typename }", "purpose": purpose})
	case "mcp":
		k.call(admin, "list_connections", map[string]any{})
	}
}

// driveWebhooks creates a source for every preset and auth mode (#2354's
// senders), posts a request to each, and returns the sources made.
func (k *canary2363) driveWebhooks(admin *client) []string {
	t := k.t
	var sources []string
	for _, s := range issue2354Senders(t) {
		if !s.public {
			k.know("webhook "+s.label, s.secret)
		}
		name := issue1870Name("s2363-" + strings.ReplaceAll(s.label, "_", "-"))
		auth := map[string]any{"secret": s.secret}
		for key, v := range s.auth {
			auth[key] = v
		}
		config := map[string]any{"persona": "admin", "compact_every_minutes": 1}
		for key, v := range s.config {
			config[key] = v
		}
		issue1870Create(t, admin, map[string]any{"name": name, "connection": issue1870Conn, "auth": auth, "config": config})
		req := s.build(time.Now(), "m-"+name)
		res, body := issue1870Post(t, baseURL(), name+s.path, req.contentType, req.body, req.header)
		k.read("POST /hooks/"+name, []byte(body))
		if res.StatusCode != http.StatusAccepted {
			t.Errorf("%s: the request was answered %d", s.label, res.StatusCode)
		}
		// A request signed with the wrong secret takes the refusal path.
		// No credential the source holds: a body with no token, wrong
		// headers, and no path token.
		bad := issue2223Request{
			contentType: "application/json", body: []byte(`{"id":"bad-` + name + `","token":"wrong"}`),
			header: map[string]string{"X-Hook-Token": "wrong", "Authorization": "Basic d3Jvbmc6d3Jvbmc="},
		}
		_, refused := issue1870Post(t, baseURL(), name, bad.contentType, bad.body, bad.header)
		k.read("POST /hooks/"+name+" (refused)", []byte(refused))
		k.rest(admin, http.MethodGet, "/api/v1/admin/webhooks/sources/"+name, nil)
		sources = append(sources, name)
	}
	return sources
}

// form2363 accepts a submission named "ok" and fails on any other, reading
// the SSN as a number.
const form2363 = `FIELDS = [
    {"name": "name", "type": "text", "required": True},
    {"name": "ssn", "type": "text", "required": True, "sensitive": True},
]

def form_spec(req):
    """The application."""
    return {"fields": FIELDS + [{"name": "t", "type": "hidden"}], "prefill": {"t": ""}}

def form_submit(req, values):
    """Accepts "ok"; reads the SSN as a number otherwise, which fails."""
    return {"confirmation": "Thanks" if values["name"] == "ok" else "Thanks " + str(int(values["ssn"]))}

def main():
    """Drafts the spec."""
    platform.result({"fields": len(form_spec({"query": {}, "caller": None})["fields"])})
`

// driveForm binds a form with a sensitive field and submits it accepted and
// failing, in both encodings.
func (k *canary2363) driveForm(admin *client) {
	t := k.t
	name := endpointName("acc-2363")
	admin.saveScript(map[string]any{"command": "create", "name": name, "source": form2363}, nil)
	out := bindEndpoint(t, admin, name, "", map[string]any{
		"endpoint": name, "methods": []string{"GET", "POST"}, "connection": formsConn, "auth_mode": "public",
	})
	form, _ := out["form"].(map[string]any)
	t.Cleanup(func() {
		admin.rest(http.MethodDelete, "/api/v1/admin/webhooks/sources/"+fmt.Sprint(form["source"]), http.NoBody)
	})
	for _, asJSON := range []bool{false, true} {
		for _, who := range []string{"ok", "fails"} {
			shown := showForm(t, admin.base, name, "", nil)
			shown.waitToFill()
			ssn := k.plant(fmt.Sprintf("form-ssn-%s-%t", who, asJSON))
			_, got := submitForm(t, admin.base, name, shown.fields(map[string]string{"name": who, "ssn": ssn}), asJSON, nil)
			raw, _ := json.Marshal(got)
			k.read("POST /forms/"+name, raw)
		}
	}
	k.call(admin, "manage_script", map[string]any{"command": "endpoint_list", "name": name})
	k.rest(admin, http.MethodGet, "/api/v1/admin/audit/events?event_kind=endpoint_request&per_page=100", nil)
}

// driveTokenKey stores a token key, mints a token through an endpoint and
// verifies it, and records the token as a value handed back to its caller.
func (k *canary2363) driveTokenKey(admin *client) {
	t := k.t
	secret := "acc2363_tok_" + k.stamp
	value := k.plant("token-key")
	if status, body := k.rest(admin, http.MethodPut, "/api/v1/admin/secrets/"+secret, map[string]any{
		"description": "Acceptance #2363", "value": value, "allow_connections": []string{"script_token"},
	}); status != http.StatusCreated {
		t.Fatalf("creating the token key answered %d %s", status, body)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/secrets/"+secret, http.NoBody) })
	name := endpointName("acc-2363-tok")
	issue2175Endpoint(t, admin, name, secret)
	token := issue2175Mint(t, admin, name, false)
	k.returned[token] = "minted token"
	issue2175Verify(t, admin, name, token)
	if ok, text := k.call(admin, "manage_script", map[string]any{
		"command": "run_draft", "name": name + "-draft", "source": issue2175Handler(secret),
	}); !ok {
		t.Fatalf("the token draft failed: %s", text)
	}
}

// searchWebhookTables reads each source's view and raw table, which serve
// the compressed files a byte search of the bucket cannot read into, and
// searches the payloads here: a planted value written into the statement
// would itself be audited.
func (k *canary2363) searchWebhookTables(admin *client, sources []string) {
	db := issue1870DB(k.t)
	for _, s := range sources {
		issue1870AwaitRows(k.t, admin, s, "", 1)
	}
	for _, s := range sources {
		issue1870WaitAllCompacted(k.t, db, s, 4*time.Minute)
	}
	forms := k.patterns(true)
	for _, s := range sources {
		for _, table := range []string{issue1870Table(s), issue1870Table(s) + "_raw"} {
			ok, text := k.call(admin, "trino_query", map[string]any{
				"connection": issue1870Conn, "sql": "SELECT payload FROM " + table + " LIMIT 1000",
				"purpose": "Acceptance #2363: reading a webhook source's stored events.",
			})
			if !ok {
				k.t.Errorf("reading %s: %s", table, text)
				continue
			}
			k.report("webhook table "+table, []byte(text), forms)
		}
	}
}

// patterns are every planted value in every form, as LIKE patterns, each with
// the value it is a form of.
func (k *canary2363) patterns(includeReturned bool) map[string]string {
	out := map[string]string{}
	add := func(m map[string]string) {
		for v, field := range m {
			for _, f := range forms2363(v) {
				out[f] = field
			}
		}
	}
	add(k.values)
	if includeReturned {
		add(k.returned)
	}
	out[k.control] = controlField2363
	return out
}

// searchDatabase reads every row of every table in the platform database as
// text, bytea as hex, and reports a row holding any form of any value.
func (k *canary2363) searchDatabase() {
	t := k.t
	db := devDB(t)
	forms := k.patterns(true)
	like := make([]string, 0, len(forms))
	esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	for f := range forms {
		like = append(like, "%"+esc.Replace(f)+"%")
	}
	rows, err := db.Query(`SELECT table_schema, table_name FROM information_schema.tables
		WHERE table_type = 'BASE TABLE' AND table_schema NOT IN ('pg_catalog', 'information_schema')`)
	if err != nil {
		t.Fatalf("listing tables: %v", err)
	}
	var tables [][2]string
	for rows.Next() {
		var s, n string
		if err := rows.Scan(&s, &n); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, [2]string{s, n})
	}
	_ = rows.Close()
	if len(tables) < 50 {
		t.Fatalf("the platform database lists %d tables; the search would not cover it", len(tables))
	}
	for _, tb := range tables {
		k.searchTable(db, tb[0], tb[1], like, forms)
	}
	t.Logf("searched %d tables", len(tables))
}

// textColumns2363 are the column types a value can be written into as text:
// a vector, a tsvector and the scalar types cannot hold one.
var textColumns2363 = []string{"text", "character varying", "character", "json", "jsonb", "bytea", "ARRAY"}

func (k *canary2363) searchTable(db *sql.DB, schema, table string, like []string, forms map[string]string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cols, err := db.QueryContext(ctx, `SELECT column_name FROM information_schema.columns
		WHERE table_schema = $1 AND table_name = $2 AND data_type = ANY($3)`, schema, table, pqArray2363(textColumns2363))
	if err != nil {
		k.t.Errorf("listing %s.%s's columns: %v", schema, table, err)
		return
	}
	var exprs []string
	for cols.Next() {
		var c string
		if err := cols.Scan(&c); err != nil {
			k.t.Errorf("reading %s.%s's columns: %v", schema, table, err)
			_ = cols.Close()
			return
		}
		exprs = append(exprs, fmt.Sprintf("coalesce(%q::text, '')", c))
	}
	_ = cols.Close()
	if len(exprs) == 0 {
		return
	}
	text := "concat_ws(' ', " + strings.Join(exprs, ", ") + ")"
	q := fmt.Sprintf(`SELECT left(%s, 2000) FROM %q.%q WHERE %s LIKE ANY($1) LIMIT 5`, text, schema, table, text)
	rows, err := db.QueryContext(ctx, q, pqArray2363(like))
	if err != nil {
		k.t.Errorf("searching %s.%s: %v", schema, table, err)
		return
	}
	defer rows.Close() //nolint:errcheck // read-only
	for rows.Next() {
		var row string
		if err := rows.Scan(&row); err != nil {
			k.t.Errorf("reading %s.%s: %v", schema, table, err)
			return
		}
		// #2354's criterion searches a source's tables for its fixed secrets
		// by writing them into the statement, and that statement is audited
		// as the test wrote it. Those rows are the test's words, not a value
		// the platform kept.
		if strings.Contains(row, "strpos(payload, ") {
			continue
		}
		k.report("table "+schema+"."+table, []byte(row), forms)
	}
}

// pqArray2363 writes a text[] literal, which lib/pq takes for ANY($1).
func pqArray2363(items []string) string {
	var b strings.Builder
	b.WriteByte('{')
	for i, s := range items {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('"')
		b.WriteString(strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s))
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}

// searchBuckets reads every object written to the platform's two stores
// during the run, gunzipping a gzip object first.
func (k *canary2363) searchBuckets() {
	forms := k.patterns(true)
	port := os.Getenv("DEV_S3_PORT")
	if port == "" {
		port = "9000"
	}
	plain, err := s3client.New(context.Background(), &s3client.Config{
		Region: "us-east-1", Endpoint: "http://localhost:" + port, UsePathStyle: true,
		AccessKeyID: "dev-access-key", SecretAccessKey: "dev-secret-key", Timeout: 2 * time.Minute,
	})
	if err != nil {
		k.t.Fatalf("a client on the plain store: %v", err)
	}
	read := 0
	for label, store := range map[string]*s3client.Client{"plain": plain, "managed": issue1870S3(k.t)} {
		read += k.searchStore(label, store, forms)
	}
	k.t.Logf("searched %d objects written during the run", read)
}

// maxObject2363 bounds an object read whole; a basemap archive is not written
// by any surface this run drives.
const maxObject2363 = 64 << 20

func (k *canary2363) searchStore(label string, store *s3client.Client, forms map[string]string) int {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	buckets, err := store.ListBuckets(ctx)
	if err != nil {
		k.t.Fatalf("listing the %s store's buckets: %v", label, err)
	}
	read := 0
	for _, b := range buckets {
		token := ""
		for {
			out, err := store.ListObjects(ctx, b.Name, "", "", 1000, token)
			if err != nil {
				k.t.Errorf("listing %s: %v", b.Name, err)
				break
			}
			for _, o := range out.Objects {
				if o.LastModified.Before(k.started) || o.Size > maxObject2363 {
					continue
				}
				obj, err := store.GetObject(ctx, b.Name, o.Key)
				if err != nil {
					k.t.Errorf("reading s3://%s/%s: %v", b.Name, o.Key, err)
					continue
				}
				read++
				k.report("object s3://"+b.Name+"/"+o.Key, gunzip2363(obj.Body), forms)
			}
			if !out.IsTruncated {
				break
			}
			token = out.NextContinueToken
		}
	}
	return read
}

func gunzip2363(b []byte) []byte {
	if len(b) < 2 || b[0] != 0x1f || b[1] != 0x8b {
		return b
	}
	r, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return b
	}
	out, err := io.ReadAll(io.LimitReader(r, maxObject2363))
	if err != nil {
		return b
	}
	return append(out, b...)
}

// fileSinks are both replicas' logs and the collector's export files.
func (k *canary2363) fileSinks() []string {
	var out []string
	for _, key := range []string{"DEV_AIR_LOG", "DEV_AIR_B_LOG"} {
		if v := os.Getenv(key); v != "" {
			out = append(out, v)
		}
	}
	dir := os.Getenv("DEV_OTEL_DIR")
	if dir == "" {
		dir = "../../dev/.otel"
	}
	for _, f := range []string{"traces.jsonl", "logs.jsonl", "metrics.jsonl"} {
		out = append(out, filepath.Join(dir, f))
	}
	return out
}

// searchFiles reads what each log and collector file gained during the run;
// a file the collector rotated is read whole.
func (k *canary2363) searchFiles() {
	forms := k.patterns(true)
	logs := 0
	for _, f := range k.fileSinks() {
		data, err := os.ReadFile(f)
		if err != nil {
			k.t.Logf("%s: %v", f, err)
			continue
		}
		if mark := k.marks[f]; mark <= int64(len(data)) {
			data = data[mark:]
		}
		if strings.Contains(f, "air") {
			logs++
		}
		k.report("file "+filepath.Base(f), data, forms)
	}
	if logs == 0 {
		k.t.Fatal("no replica log was read; `make acceptance` loads DEV_AIR_LOG from dev/.dev-ports.env")
	}
}

// searchResponses searches every body the run read for the planted values
// the platform never hands back.
func (k *canary2363) searchResponses() {
	forms := k.patterns(false)
	for _, r := range k.responses {
		k.report("response to "+r.where, r.data, forms)
	}
}

// controlField2363 labels the control value in the search.
const controlField2363 = "control"

// report fails on each form of each value data holds, naming the field and
// where, with the text around it. The control is noted, not failed.
func (k *canary2363) report(where string, data []byte, forms map[string]string) {
	for f, field := range forms {
		if i := bytes.Index(data, []byte(f)); i >= 0 {
			if field == controlField2363 {
				k.found[strings.Fields(where)[0]] = true
				continue
			}
			lo, hi := max(0, i-120), min(len(data), i+len(f)+120)
			k.t.Errorf("%s holds the %s value (as %q): ...%s...", where, field, f, bytes.ReplaceAll(data[lo:hi], []byte("\n"), []byte(" ")))
		}
	}
}
