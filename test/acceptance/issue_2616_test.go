//go:build integration

package acceptance

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2616: s3_list and s3_object refuse the platform's own
// buckets on every connection, for every persona, administrators included,
// and a listing of buckets leaves them out. On the dev stack those are
// managed-resources (the resource library, webhook events and the tables
// registered over them: internal/webhook/whlayout) and portal-assets (assets,
// exports, script outputs, site releases, and the tables registered over
// assets). A refusal names the surface that reads the bucket instead, and the
// object's bytes reach no tool answer, audit row or call record. A connection
// granted to a persona that reaches a platform bucket is warned of at boot and
// counted. A script reads a resource's bytes and hash with
// platform.read_resource under its owner's reach. No SQL statement through the
// Trino tools points a table, a schema or a partition at a platform bucket,
// on the catalog whose credentials reach it.
//
// Wire forms: s3_list takes connection, bucket and purpose as strings and
// max_keys as a number; s3_object takes action, connection, bucket, key,
// dest_bucket, dest_key, method, content, content_type and purpose as strings
// and expires_in as a number. manage_resource takes action, filename,
// display_name, path, description, content and content_type as strings;
// save_asset takes name, content, content_type and description as strings.
// run_script takes name as a string, args as an object of strings and
// wait_seconds as a number; the resource a script reads is named both ways
// the binding admits, a reference (mcp:resource:<id>) and an address
// (mcp://...). trino_execute and trino_query take connection, sql and purpose
// as strings, the statement written plain, run from a string, and with its
// location assembled from parts. Each is sent as its schema's one JSON type.

const (
	keyOwner2616  = "acme-owner-key" // collaborator: every tool on every connection, not an administrator
	keyPeer2616   = "acme-peer-key"  // collaborator, another person
	keyAdmin2616  = "acme-dev-key-2024"
	connPlain2616 = "dev-s3"        // the plain store, where portal-assets is
	connTLS2616   = "dev-resources" // the TLS store the resource library writes through
	refusal2616   = "is where the platform keeps"
)

// purpose2616 is a purpose unique to one test, which its audit rows are found
// by.
func purpose2616(t *testing.T) string {
	return "Acceptance #2616 " + t.Name() + " " + unique1579()
}

// personalResource2616 files a personal resource holding content and returns
// its id and the key it is stored under.
func personalResource2616(t *testing.T, c *client, content string) (id, key string) {
	t.Helper()
	out := c.call("manage_resource", map[string]any{
		"action": "create", "filename": "acc2616-" + unique1579() + ".txt", "display_name": "acc2616-" + unique1579(),
		"path": "acceptance-2616", "description": "Acceptance #2616: a personal resource.", "content": content, "content_type": "text/plain",
	})
	id, _ = out["resource_id"].(string)
	if id == "" {
		t.Fatalf("manage_resource create returned no resource_id: %v", out)
	}
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody) })
	status, row := c.rest(http.MethodGet, "/api/v1/resources/"+id, http.NoBody)
	key, _ = row["s3_key"].(string)
	if status != http.StatusOK || !strings.HasPrefix(key, "resources/user/") {
		t.Fatalf("the resource is not stored as a personal one: %d %v", status, row)
	}
	return id, key
}

// refusedEveryRead asks for get, metadata and presign (GET) on bucket/key and
// fails unless each is refused naming the bucket and the surface to use; it
// returns every answer's text.
func refusedEveryRead(t *testing.T, c *client, connection, bucket, key, purpose, use string) string {
	t.Helper()
	var all strings.Builder
	for _, args := range []map[string]any{
		{"action": "get"},
		{"action": "metadata"},
		{"action": "presign", "method": "GET", "expires_in": 60},
	} {
		args["connection"], args["bucket"], args["key"], args["purpose"] = connection, bucket, key, purpose
		res, text, err := c.callRaw("s3_object", args)
		if err != nil {
			t.Fatalf("s3_object %s: transport error: %v", args["action"], err)
		}
		all.WriteString(text + "\n")
		want := fmt.Sprintf("bucket %q %s", bucket, refusal2616)
		if !res.IsError || !strings.Contains(text, want) || !strings.Contains(text, use) {
			t.Errorf("s3_object %s on s3://%s/%s through %s was not refused naming %s: %s", args["action"], bucket, key, connection, use, firstRunes(text, 300))
		}
	}
	return all.String()
}

// sinks2616 is every audit row and call record of the calls made under
// purpose since start, once their audit rows have been written. The rows of
// the calls that wrote the objects (their owner's create and save) hold what
// their owner wrote and are not searched: the question is whether a refused
// read carried any of it.
func sinks2616(t *testing.T, start time.Time, purpose string, calls int) map[string]string {
	t.Helper()
	waitFor(t, "the audit rows of the refused calls", func() (bool, string) {
		n := platformCount2539(t, `SELECT count(*) FROM audit_logs WHERE timestamp >= $1 AND purpose = $2`, start, purpose)
		return n >= calls, fmt.Sprintf("%d of %d audit rows", n, calls)
	})
	return map[string]string{
		"audit rows":   platformText2539(t, `SELECT coalesce(string_agg(row_to_json(a)::text, E'\n'), '') FROM audit_logs a WHERE a.timestamp >= $1 AND a.purpose = $2`, start, purpose),
		"call records": platformText2539(t, `SELECT coalesce(string_agg(row_to_json(c)::text, E'\n'), '') FROM call_records c WHERE c.created_at >= $1 AND c.purpose = $2`, start, purpose),
	}
}

// TestIssue2616_AnotherPersonsResourceIsRefusedToANonAdministrator: a
// collaborator, who holds s3_object on the connection the resource library
// writes through, is refused get, metadata and presign on another person's
// resource key, through that connection and the other store's; the bytes
// (as written, base64, hex) are in no answer, audit row or call record.
func TestIssue2616_AnotherPersonsResourceIsRefusedToANonAdministrator(t *testing.T) {
	start := time.Now().UTC()
	canary := "acc2616-resource-canary-" + unique1579()
	_, key := personalResource2616(t, connectAs(t, keyPeer2616), canary)
	if body, ok := resourceStore(t).get(key); !ok || string(body) != canary {
		t.Fatalf("the resource is not in %s under %s", devResourceBucket, key)
	}

	caller := connectAs(t, keyOwner2616)
	purpose := purpose2616(t)
	answers := refusedEveryRead(t, caller, connTLS2616, devResourceBucket, key, purpose, "manage_resource")
	answers += refusedEveryRead(t, caller, connPlain2616, devResourceBucket, key, purpose, "manage_resource")
	canaries := map[string]string{"resource's bytes": canary}
	noCanary(t, "the S3 tools' answers", []byte(answers), canaries)
	for where, sink := range sinks2616(t, start, purpose, 6) {
		noCanary(t, where, []byte(sink), canaries)
	}
}

// TestIssue2616_TheAssetAndEventStoresAreRefusedAndUnlisted: another person's
// unshared asset in portal-assets and a webhook event segment in
// managed-resources are refused the same way, and so is a site published
// from the portal bucket; s3_list with no bucket leaves both buckets out on
// either store while listing the business bucket, and s3_list naming either
// is refused.
func TestIssue2616_TheAssetAndEventStoresAreRefusedAndUnlisted(t *testing.T) {
	start := time.Now().UTC()
	owner := connectAs(t, keyOwner2616)
	assetCanary := "acc2616-asset-canary-" + unique1579()
	assetKey := fmt.Sprint(assetRow1787(t, owner, saveAsset1787(t, owner, "text/plain", assetCanary))["s3_key"])
	eventCanary := "acc2616-event-canary-" + unique1579()
	eventKey := fmt.Sprintf("webhooks/acc2616-%s/raw/dt=2026-11-11/hour=00/minute=00/w-0000000001.jsonl.gz", unique1579())
	resourceStore(t).put(eventKey, "application/gzip", []byte(eventCanary))

	caller := connectAs(t, keyPeer2616)
	purpose := purpose2616(t)
	answers := refusedEveryRead(t, caller, connPlain2616, devAssetBucket, assetKey, purpose, "manage_asset")
	answers += refusedEveryRead(t, caller, connTLS2616, devResourceBucket, eventKey, purpose, "manage_resource")

	for _, conn := range []string{connPlain2616, connTLS2616} {
		listed := caller.call("s3_list", map[string]any{"connection": conn, "purpose": purpose})
		names := []string{}
		for _, b := range asList(listed["buckets"]) {
			names = append(names, fmt.Sprint(b["name"]))
		}
		for _, platform := range []string{devAssetBucket, devResourceBucket} {
			if contains2540(names, platform) {
				t.Errorf("s3_list on %s lists the platform's bucket %s: %v", conn, platform, names)
			}
		}
		if conn == connPlain2616 && !contains2540(names, devBusinessBucket) {
			t.Errorf("s3_list on %s does not list the business bucket %s: %v", conn, devBusinessBucket, names)
		}
		for _, platform := range []string{devAssetBucket, devResourceBucket} {
			res, text, err := caller.callRaw("s3_list", map[string]any{"connection": conn, "bucket": platform, "max_keys": 5, "purpose": purpose})
			if err != nil || !res.IsError || !strings.Contains(text, refusal2616) {
				t.Errorf("s3_list naming %s on %s was not refused: %v %s", platform, conn, err, firstRunes(text, 300))
			}
			answers += text + "\n"
		}
	}
	// A bucket source reads through the same toolkit: a site published from
	// a platform bucket is refused before anything is read.
	site := newSite2604(t, keyOwner2604, "acc2616-src-")
	prefix := strings.TrimSuffix(assetKey, "/"+lastSegment2616(assetKey)) + "/"
	res, text, err := owner.callRaw("manage_site", map[string]any{
		"command": "publish", "site": site.name,
		"from_bucket": map[string]any{"connection": connPlain2616, "bucket": devAssetBucket, "prefix": prefix},
	})
	if err != nil || !res.IsError || !strings.Contains(text, refusal2616) {
		t.Errorf("a publish from the portal bucket was not refused: %v %s", err, firstRunes(text, 300))
	}
	answers += text + "\n"
	canaries := map[string]string{"asset's bytes": assetCanary, "event's bytes": eventCanary}
	noCanary(t, "the S3 tools' answers", []byte(answers), canaries)
	for where, sink := range sinks2616(t, start, purpose, 12) {
		noCanary(t, where, []byte(sink), canaries)
	}
}

// TestIssue2616_AnAdministratorIsRefusedTheSameWay: an administrator is
// refused every read, a write, a delete and a copy into either platform
// bucket, and the objects are untouched.
func TestIssue2616_AnAdministratorIsRefusedTheSameWay(t *testing.T) {
	canary := "acc2616-admin-canary-" + unique1579()
	_, key := personalResource2616(t, connectAs(t, keyPeer2616), canary)
	owner := connectAs(t, keyOwner2616)
	assetKey := fmt.Sprint(assetRow1787(t, owner, saveAsset1787(t, owner, "text/plain", canary+"-asset"))["s3_key"])

	admin := connectAs(t, keyAdmin2616)
	purpose := purpose2616(t)
	answers := refusedEveryRead(t, admin, connTLS2616, devResourceBucket, key, purpose, "manage_resource")
	answers += refusedEveryRead(t, admin, connPlain2616, devAssetBucket, assetKey, purpose, "manage_asset")

	source := fmt.Sprintf("acceptance-2616/%s/source.txt", unique1579())
	admin.call("s3_object", map[string]any{"action": "put", "connection": connPlain2616, "bucket": devBusinessBucket, "key": source, "content": "business data", "content_type": "text/plain", "purpose": purpose})
	t.Cleanup(func() {
		_, _, _ = admin.callRaw("s3_object", map[string]any{"action": "delete", "connection": connPlain2616, "bucket": devBusinessBucket, "key": source, "purpose": purpose})
	})
	for _, args := range []map[string]any{
		{"action": "put", "connection": connTLS2616, "bucket": devResourceBucket, "key": key, "content": "overwritten", "content_type": "text/plain"},
		{"action": "delete", "connection": connTLS2616, "bucket": devResourceBucket, "key": key},
		{"action": "delete", "connection": connPlain2616, "bucket": devAssetBucket, "key": assetKey},
		{"action": "copy", "connection": connPlain2616, "bucket": devBusinessBucket, "key": source, "dest_bucket": devAssetBucket, "dest_key": assetKey},
	} {
		args["purpose"] = purpose
		res, text, err := admin.callRaw("s3_object", args)
		if err != nil || !res.IsError || !strings.Contains(text, refusal2616) {
			t.Errorf("s3_object %s into %v was not refused: %v %s", args["action"], args["dest_bucket"], err, firstRunes(text, 300))
		}
		answers += text + "\n"
	}
	if body, ok := resourceStore(t).get(key); !ok || string(body) != canary {
		t.Errorf("the resource was changed or deleted: %q %v", body, ok)
	}
	if body, ok := assetStore(t).get(assetKey); !ok || string(body) != canary+"-asset" {
		t.Errorf("the asset was changed or deleted: %q %v", body, ok)
	}
	noCanary(t, "the S3 tools' answers", []byte(answers), map[string]string{"resource's bytes": canary})
}

// issue2616Config is a deployment whose one S3 connection reaches the dev
// stack's plain store, where its portal bucket is, granted to an ordinary
// persona.
const issue2616Config = `
server:
  name: acceptance-2616
  transport: http
  address: "%s"
auth:
  api_keys:
    enabled: true
    keys:
      - key: "acceptance-2616-key"
        name: "acceptance-2616"
        roles: ["reader"]
personas:
  reader:
    display_name: Reader
    roles: ["reader"]
    tools:
      allow: ["*"]
    connections:
      allow: ["*"]
database:
  dsn: "postgres://platform:platform_secret@localhost:%s/mcp_platform?sslmode=disable"
  max_open_conns: 5
toolkits:
  s3:
    enabled: true
    instances:
      acc-2616-lake:
        enabled: true
        endpoint: "http://localhost:%s"
        region: "us-east-1"
        access_key_id: "dev-access-key"
        secret_access_key: "dev-secret-key"
        use_path_style: true
    default: acc-2616-lake
portal:
  s3_connection: acc-2616-lake
  s3_bucket: portal-assets
thumbnails:
  enabled: false
`

// TestIssue2616_BootWarnsOfAGrantedConnectionThatReachesAPlatformBucket: a
// platform started with an S3 connection granted to a persona whose
// credentials reach its portal bucket logs the warning naming the connection
// and the bucket, and counts it in config_validation_warnings_total.
func TestIssue2616_BootWarnsOfAGrantedConnectionThatReachesAPlatformBucket(t *testing.T) {
	port := devS3Port2616()
	p := startPlatform(t, fmt.Sprintf(issue2616Config, "%s", devPostgresPort(), port))
	p.serving(t, 2*time.Minute)
	metrics := ""
	for _, kv := range p.cmd.Env {
		if v, ok := strings.CutPrefix(kv, "OTEL_METRICS_ADDR="); ok {
			metrics = "http://" + v + "/metrics"
		}
	}
	const code = "s3_connection_reaches_platform_bucket"
	waitFor(t, "the boot warning", func() (bool, string) {
		out := p.out.String()
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, `"code":"`+code+`"`) && strings.Contains(line, `"connection":"acc-2616-lake"`) && strings.Contains(line, `"bucket":"portal-assets"`) {
				return true, ""
			}
		}
		return false, firstRunes(out[max(0, len(out)-2000):], 2000)
	})
	if got := metricSeries(scrapeOne(t, metrics), "config_validation_warnings_total", map[string]string{"code": code}); got < 1 {
		t.Errorf("config_validation_warnings_total{code=%q} = %v, want the warning counted", code, got)
	}
}

// lastSegment2616 is a key's last path segment.
func lastSegment2616(key string) string {
	return key[strings.LastIndex(key, "/")+1:]
}

func devS3Port2616() string {
	if v := os.Getenv("DEV_S3_PORT"); v != "" {
		return v
	}
	return "9000"
}

// script2616 reads the resource it is given and prints its hash and size.
const script2616 = `def main():
    """Reads the resource it is given and prints its hash and size."""
    r = platform.read_resource(run.params["ref"])
    print("sha256 %s size %d" % (r.sha256, r.size))
`

// TestIssue2616_AScriptReadsItsOwnersResourceAndIsRefusedAnothers: a script
// reads the bytes and sha256 of a resource in its owner's library, named by
// reference and by address, and a run naming another person's resource fails
// with nothing of that resource in it.
func TestIssue2616_AScriptReadsItsOwnersResourceAndIsRefusedAnothers(t *testing.T) {
	owner := connectAs(t, keyOwner2616)
	mine := "acc2616-mine-" + unique1579()
	ownID, _ := personalResource2616(t, owner, mine)
	theirs := "acc2616-theirs-" + unique1579()
	peerID, _ := personalResource2616(t, connectAs(t, keyPeer2616), theirs)
	_, row := owner.rest(http.MethodGet, "/api/v1/resources/"+ownID, http.NoBody)
	address, _ := row["uri"].(string)
	if !strings.HasPrefix(address, "mcp://") {
		t.Fatalf("the resource has no address: %v", row)
	}

	name := "acc2616-read-" + unique1579()
	params := []any{map[string]any{"name": "ref", "type": "string", "required": true, "description": "The resource to read."}}
	owner.saveScript(map[string]any{"command": "create", "name": name, "description": "Acceptance #2616: reads a resource.", "source": script2616, "params": params},
		map[string]any{"ref": "mcp:resource:" + ownID})
	t.Cleanup(func() { _, _, _ = owner.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })

	sum := sha256.Sum256([]byte(mine))
	want := fmt.Sprintf("sha256 %s size %d", hex.EncodeToString(sum[:]), len(mine))
	for _, ref := range []string{"mcp:resource:" + ownID, address} {
		run := owner.call("run_script", map[string]any{"name": name, "args": map[string]any{"ref": ref}, "wait_seconds": 120})
		if run["status"] != "succeeded" || !strings.Contains(fmt.Sprint(run["log"]), want) {
			t.Errorf("reading %s: want %q, got %v", ref, want, run)
		}
	}

	refused := owner.call("run_script", map[string]any{"name": name, "args": map[string]any{"ref": "mcp:resource:" + peerID}, "wait_seconds": 120})
	if refused["status"] != "failed" || !strings.Contains(fmt.Sprint(refused["error"]), peerID) {
		t.Errorf("a run naming another person's resource was not refused naming it: %v", refused)
	}
	noCanary(t, "the refused run", []byte(asJSON(refused)), map[string]string{"other person's resource": theirs})
}

// trinoRoutes2616 is each way a statement could point a table, a schema or a
// partition at the resource bucket, and each way of assembling the location
// so a check could not read it. scratch_resources is the catalog the dev
// stack registers tables over managed resources in, whose credentials reach
// the bucket by design.
func trinoRoutes2616(prefix, table string) map[string]string {
	loc := "s3a://managed-resources/" + prefix
	return map[string]string{
		"an external table": "CREATE TABLE scratch_resources.uploads." + table + " (line varchar) WITH (external_location = '" + loc + "', format = 'TEXTFILE')",
		"a schema":          "CREATE SCHEMA scratch_resources." + table + " WITH (location = '" + loc + "')",
		"a partition":       "CALL scratch_resources.system.register_partition('uploads', '" + table + "', ARRAY['dt'], ARRAY['1'], '" + loc + "')",
		"a run-time string": "EXECUTE IMMEDIATE 'CREATE TABLE scratch_resources.uploads." + table + " (line varchar) WITH (external_location = ''" + loc + "'', format = ''TEXTFILE'')'",
		"concatenation":     "CREATE TABLE scratch_resources.uploads." + table + " (line varchar) WITH (external_location = 's3a://managed-' || 'resources/" + prefix + "', format = 'TEXTFILE')",
		"a unicode string":  "CREATE TABLE scratch_resources.uploads." + table + " (line varchar) WITH (external_location = U&'s3a://managed\\002dresources/" + prefix + "', format = 'TEXTFILE')",
	}
}

// TestIssue2616_NoSQLStatementReachesAPlatformBucket: as a collaborator and as
// an administrator, trino_execute on the connection whose catalog reads the
// resource bucket is refused an external table, a schema and a partition over
// it, and the same statement run from a string or with its location assembled
// from parts; trino_query finds no such table; the planted object's bytes are
// in no answer, audit row or call record; and a registration through
// manage_table, which the platform runs itself, still reads its file.
func TestIssue2616_NoSQLStatementReachesAPlatformBucket(t *testing.T) {
	start := time.Now().UTC()
	canary := "acc2616-trino-canary-" + unique1579()
	prefix := "acc2616-trino-" + unique1579() + "/"
	resourceStore(t).put(prefix+"secret.txt", "text/plain", []byte(canary+"\n"))

	purpose := purpose2616(t)
	answers := ""
	calls := 0
	for who, key := range map[string]string{"a collaborator": keyOwner2616, "an administrator": keyAdmin2616} {
		c := connectAs(t, key)
		table := "acc2616_" + unique1579()
		for route, sql := range trinoRoutes2616(prefix, table) {
			res, text, err := c.callRaw("trino_execute", map[string]any{"connection": scratchResourceConnection, "sql": sql, "purpose": purpose})
			calls++
			answers += text + "\n"
			if err != nil || !res.IsError {
				t.Errorf("%s: %s was not refused: %v %s", who, route, err, firstRunes(text, 300))
				_, _, _ = c.callRaw("trino_execute", map[string]any{"connection": scratchResourceConnection, "sql": "DROP TABLE IF EXISTS scratch_resources.uploads." + table, "purpose": purpose})
				continue
			}
			if !strings.Contains(text, "managed-resources") && !strings.Contains(text, "one quoted string") {
				t.Errorf("%s: %s was refused without naming the bucket or the unreadable location: %s", who, route, firstRunes(text, 300))
			}
		}
		_, text, _ := c.callRaw("trino_query", map[string]any{"connection": scratchResourceConnection, "sql": "SELECT line FROM scratch_resources.uploads." + table, "purpose": purpose})
		calls++
		answers += text + "\n"
	}

	owner := connectAs(t, keyOwner2616)
	reference, _ := csvResource1577(t, owner, unique1579(), "region,total\nwest,1\neast,2\n")
	registered := register1577(t, owner, reference, "acc2616_reg_"+unique1579(), nil)
	table, _ := registered["query_table"].(string)
	if table == "" {
		t.Fatalf("the registration names no table: %v", registered)
	}
	got := owner.call("trino_query", map[string]any{"connection": scratchResourceConnection, "sql": "SELECT count(*) AS n FROM " + table, "purpose": purpose})
	if !strings.Contains(asJSON(got), `"n":2`) {
		t.Errorf("a table the platform registered does not read its file: %v", got)
	}

	canaries := map[string]string{"planted object's bytes": canary}
	noCanary(t, "the Trino tools' answers", []byte(answers), canaries)
	for where, sink := range sinks2616(t, start, purpose, calls) {
		noCanary(t, where, []byte(sink), canaries)
	}
}
