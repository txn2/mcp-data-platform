//go:build integration

package acceptance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"
)

// Issue #1715: every replica answered API key requests from the keys it held
// in memory, and learned of a key written through another replica only when a
// reload event reached it. Right after a key write, the other replica listed a
// created key as absent and refused a deleted name with 409. The replica that
// took the write was wrong too, and not only for a moment: a key it generated
// was held twice once a peer's reload arrived, a delete removed one copy, and
// the deleted key went on authenticating there until the next reload.
//
// What these hold, through the admin REST routes the portal's API keys page
// calls and the MCP endpoint a key is used on, with two replicas over one
// database reached one at a time: a deleted key is refused by every replica
// from the moment the delete returns, including the replica that deleted it;
// a created key is listed by, and authenticates on, the other replica at once;
// a deleted name can be created again through the other replica at once; a
// delete of a name no replica holds answers 404; and two replicas creating one
// name at once hand out one key, which works.
//
// Wire forms: the create route decodes its body strictly into
// authKeyCreateRequest, whose `name` is a string and whose `roles` is a
// []string, so each admits exactly one JSON form: `name` is sent as a JSON
// string and `roles` as a JSON array of strings, as literal request bytes. The
// MCP endpoint is sent a JSON-RPC initialize request as literal bytes.

const issue1715KeysPath = "/api/v1/admin/auth/keys"

// issue1715Name is a key name unique to this run, so a key a failed earlier
// run left behind cannot answer for this one.
func issue1715Name(label string) string {
	return fmt.Sprintf("issue-1715-%s-%d", label, time.Now().UnixNano())
}

// issue1715CreateStatus sends the literal create body through c and returns
// the status and the key handed back, if any.
func issue1715CreateStatus(t *testing.T, c *client, name string) (int, string) {
	t.Helper()
	status, key, err := issue1715CreateRequest(c, name)
	if err != nil {
		t.Fatalf("POST %s name=%s through %s: %v", issue1715KeysPath, name, c.base, err)
	}
	return status, key
}

// issue1715CreateRequest is the create request itself, reporting a transport
// failure as an error rather than through the test, so it can run on a
// goroutine other than the test's.
func issue1715CreateRequest(c *client, name string) (int, string, error) {
	body := bytes.NewReader([]byte(`{"name":"` + name + `","roles":["admin"]}`))
	req, err := http.NewRequestWithContext(c.ctx, http.MethodPost, c.base+issue1715KeysPath, body) // #nosec G704 -- the platform under test, named by the suite's own environment
	if err != nil {
		return 0, "", fmt.Errorf("building the request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req) // #nosec G704 -- the platform under test, named by the suite's own environment
	if err != nil {
		return 0, "", fmt.Errorf("sending the request: %w", err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after read
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return res.StatusCode, "", fmt.Errorf("reading the body: %w", err)
	}
	var out struct {
		Key string `json:"key"`
	}
	_ = json.Unmarshal(raw, &out) //nolint:errcheck // a refusal's body carries no key
	return res.StatusCode, out.Key, nil
}

// issue1715Create mints an admin key through c, registers its deletion, and
// returns the key value.
func issue1715Create(t *testing.T, c *client, name string) string {
	t.Helper()
	status, key := issue1715CreateStatus(t, c, name)
	if status != http.StatusCreated || key == "" {
		t.Fatalf("POST %s name=%s through %s: status %d, key %q", issue1715KeysPath, name, c.base, status, key)
	}
	t.Cleanup(func() { c.rest(http.MethodDelete, issue1715KeysPath+"/"+name, nil) })
	return key
}

// issue1715Delete deletes a key through c and returns the status.
func issue1715Delete(c *client, name string) int {
	status, _ := c.rest(http.MethodDelete, issue1715KeysPath+"/"+name, nil)
	return status
}

// issue1715Listed reports whether the listing c's replica answers carries name.
func issue1715Listed(t *testing.T, c *client, name string) bool {
	t.Helper()
	status, out := c.rest(http.MethodGet, issue1715KeysPath, nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s through %s: status %d, body %v", issue1715KeysPath, c.base, status, out)
	}
	keys, _ := out["keys"].([]any)
	for _, k := range keys {
		if entry, _ := k.(map[string]any); entry["name"] == name {
			return true
		}
	}
	return false
}

// issue1715MCPStatus sends a JSON-RPC initialize to the MCP endpoint at base
// with key as the bearer credential and returns the HTTP status: 200 when the
// key authenticates, 401 when it is refused.
func issue1715MCPStatus(t *testing.T, base, key string) int {
	t.Helper()
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"acceptance-1715","version":"1.0.0"}}}`)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, base, bytes.NewReader(body)) // #nosec G704 -- the platform under test, named by the suite's own environment
	if err != nil {
		t.Fatalf("initialize at %s: %v", base, err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	res, err := http.DefaultClient.Do(req) // #nosec G704 -- the platform under test, named by the suite's own environment
	if err != nil {
		t.Fatalf("initialize at %s: %v", base, err)
	}
	_ = res.Body.Close() //nolint:errcheck // only the status is read
	return res.StatusCode
}

// issue1715RESTStatus reads the key listing with key as the credential and
// returns the status: the keys this suite mints carry the admin role, so 200
// when the key authenticates and 401 when it is refused.
func issue1715RESTStatus(c *client, key string) int {
	as := *c
	as.apiKey = key
	status, _ := as.rest(http.MethodGet, issue1715KeysPath, nil)
	return status
}

// issue1715AwaitAuthenticates waits until key authenticates on c's replica.
// It is setup, not a criterion: it brings a replica to the state a key write
// through another replica eventually produces, so what follows starts there.
func issue1715AwaitAuthenticates(t *testing.T, c *client, key string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for issue1715MCPStatus(t, c.base, key) != http.StatusOK {
		if time.Now().After(deadline) {
			t.Fatalf("a created key never authenticated on %s", c.base)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestIssue1715_ADeletedKeyIsRefusedByEveryReplica is the ticket's first
// criterion, on the sequence that left a key authenticating after its delete:
// the key is generated on one replica, a key written through the other replica
// reaches the first as a reload, and the first replica deletes the key. From
// the moment the delete returns, both replicas refuse it on the MCP endpoint
// and on the admin routes, and neither lists it.
func TestIssue1715_ADeletedKeyIsRefusedByEveryReplica(t *testing.T) {
	a, b := connectReplicaPair(t)
	name := issue1715Name("deleted")
	key := issue1715Create(t, a, name)
	issue1715Create(t, b, issue1715Name("reload"))
	issue1715AwaitAuthenticates(t, a, key)
	issue1715AwaitAuthenticates(t, b, key)

	if status := issue1715Delete(a, name); status != http.StatusOK {
		t.Fatalf("DELETE %s/%s through %s: status %d", issue1715KeysPath, name, a.base, status)
	}
	for _, c := range []*client{a, b} {
		if status := issue1715MCPStatus(t, c.base, key); status != http.StatusUnauthorized {
			t.Errorf("the deleted key's MCP initialize on %s answered %d; want 401", c.base, status)
		}
		if status := issue1715RESTStatus(c, key); status != http.StatusUnauthorized {
			t.Errorf("the deleted key's GET %s on %s answered %d; want 401", issue1715KeysPath, c.base, status)
		}
		if issue1715Listed(t, c, name) {
			t.Errorf("%s still lists the deleted key %q", c.base, name)
		}
	}
}

// TestIssue1715_ACreatedKeyIsListedAndAuthenticatesOnTheOtherReplica is the
// ticket's second criterion: the next request after a create returns, sent to
// the other replica, finds the key in the listing and authenticates with it.
func TestIssue1715_ACreatedKeyIsListedAndAuthenticatesOnTheOtherReplica(t *testing.T) {
	a, b := connectReplicaPair(t)
	name := issue1715Name("created")
	key := issue1715Create(t, a, name)

	if !issue1715Listed(t, b, name) {
		t.Errorf("%s does not list the key %q created through %s", b.base, name, a.base)
	}
	if status := issue1715MCPStatus(t, b.base, key); status != http.StatusOK {
		t.Errorf("the created key's MCP initialize on %s answered %d; want 200", b.base, status)
	}
}

// TestIssue1715_ADeletedNameCanBeCreatedAgainThroughTheOtherReplica is the
// ticket's third criterion: a name deleted through one replica is free on the
// other the moment the delete returns, and the key created under it is the
// one that works on both replicas while the deleted one is refused.
func TestIssue1715_ADeletedNameCanBeCreatedAgainThroughTheOtherReplica(t *testing.T) {
	a, b := connectReplicaPair(t)
	name := issue1715Name("reused")
	oldKey := issue1715Create(t, a, name)
	issue1715AwaitAuthenticates(t, b, oldKey)

	if status := issue1715Delete(a, name); status != http.StatusOK {
		t.Fatalf("DELETE %s/%s through %s: status %d", issue1715KeysPath, name, a.base, status)
	}
	newKey := issue1715Create(t, b, name)
	for _, c := range []*client{a, b} {
		if status := issue1715MCPStatus(t, c.base, newKey); status != http.StatusOK {
			t.Errorf("the re-created key's MCP initialize on %s answered %d; want 200", c.base, status)
		}
		if status := issue1715MCPStatus(t, c.base, oldKey); status != http.StatusUnauthorized {
			t.Errorf("the deleted key's MCP initialize on %s answered %d; want 401", c.base, status)
		}
	}
}

// TestIssue1715_DeletingANameNoReplicaHoldsAnswers404 holds that a delete of a
// key already deleted, through either replica, is a key that is not there,
// not a database failure.
func TestIssue1715_DeletingANameNoReplicaHoldsAnswers404(t *testing.T) {
	a, b := connectReplicaPair(t)
	name := issue1715Name("gone")
	issue1715Create(t, a, name)

	if status := issue1715Delete(a, name); status != http.StatusOK {
		t.Fatalf("DELETE %s/%s through %s: status %d", issue1715KeysPath, name, a.base, status)
	}
	for _, c := range []*client{b, a} {
		if status := issue1715Delete(c, name); status != http.StatusNotFound {
			t.Errorf("a second DELETE of %q through %s answered %d; want 404", name, c.base, status)
		}
	}
}

// TestIssue1715_TwoReplicasCreatingOneNameHandOutOneKey holds that a name
// created through both replicas at once is created once: one request answers
// 201 and the other 409, and the key the 201 handed back authenticates on
// both replicas. Before, both could answer 201, the second write replaced the
// first key's hash, and the first caller held a key that no replica accepted.
func TestIssue1715_TwoReplicasCreatingOneNameHandOutOneKey(t *testing.T) {
	a, b := connectReplicaPair(t)
	const rounds = 5
	for round := range rounds {
		name := issue1715Name(fmt.Sprintf("race%d", round))
		t.Cleanup(func() { a.rest(http.MethodDelete, issue1715KeysPath+"/"+name, nil) })

		statuses := make([]int, 2)
		keys := make([]string, 2)
		errs := make([]error, 2)
		var wg sync.WaitGroup
		for i, c := range []*client{a, b} {
			wg.Go(func() { statuses[i], keys[i], errs[i] = issue1715CreateRequest(c, name) })
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("round %d: POST %s name=%s, request %d: %v", round, issue1715KeysPath, name, i, err)
			}
		}

		created := 0
		for i := range statuses {
			switch statuses[i] {
			case http.StatusCreated:
				created++
				issue1715AssertKeyWorksEverywhere(t, keys[i], a, b)
			case http.StatusConflict:
			default:
				t.Errorf("round %d: POST %s name=%s answered %d; want 201 or 409", round, issue1715KeysPath, name, statuses[i])
			}
		}
		if created != 1 {
			t.Errorf("round %d: %d of two concurrent creates of %q answered 201; want exactly 1", round, created, name)
		}
	}
}

// issue1715AssertKeyWorksEverywhere reports each replica that refuses key.
func issue1715AssertKeyWorksEverywhere(t *testing.T, key string, replicas ...*client) {
	t.Helper()
	for _, c := range replicas {
		if status := issue1715MCPStatus(t, c.base, key); status != http.StatusOK {
			t.Errorf("a key handed back with 201 answered %d on %s; want 200", status, c.base)
		}
	}
}
