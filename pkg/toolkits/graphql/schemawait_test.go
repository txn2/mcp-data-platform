package graphql

import (
	"context"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/upstreamretry"
)

// pendingCode is the code a pending refusal carries in its error envelope.
func pendingCode(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if res == nil || !res.IsError {
		t.Fatalf("want an error result, got %#v", res)
	}
	sc, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("no structured content: %#v", res.StructuredContent)
	}
	env, ok := sc["error"].(map[string]any)
	if !ok {
		t.Fatalf("no error envelope: %#v", sc)
	}
	code, _ := env["code"].(string)
	return code
}

// #2707: a connection whose first schema read has not finished answers, at
// the bound, with the code a script's host reads as the upstream being
// unavailable, not an ordinary tool error the run is failed for.
func TestACallOnAConnectionStillReadingItsSchemaIsTheUpstreamsRefusal(t *testing.T) {
	tk := newToolkit(t, newUpstream(t), "", nil)
	tk.SetSchemaWait(20 * time.Millisecond)

	for name, call := range map[string]func() (*mcp.CallToolResult, any, error){
		"graphql_query": func() (*mcp.CallToolResult, any, error) {
			return tk.handleQuery(context.Background(), nil, QueryInput{Connection: "gql", Query: datasetDocument})
		},
		"graphql_discover": func() (*mcp.CallToolResult, any, error) {
			return tk.handleDiscover(context.Background(), nil, DiscoverInput{Connection: "gql"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			res, _, err := call()
			if err != nil {
				t.Fatal(err)
			}
			if code := pendingCode(t, res); code != upstreamretry.CodeUnavailable {
				t.Fatalf("code = %q, want %q", code, upstreamretry.CodeUnavailable)
			}
		})
	}
}

// #2707: a call made while the read is in flight waits for it and is served
// by the schema it installs.
func TestACallWaitsForTheFirstSchemaReadAndIsServed(t *testing.T) {
	u := newUpstream(t)
	u.respond = answer(`{"data":{"dataset":{"urn":"u1","name":"orders"}}}`)
	tk := newToolkit(t, u, "", nil)
	tk.SetSchemaWait(10 * time.Second)

	waiting := make(chan struct{})
	installed := make(chan error, 1)
	go func() {
		<-waiting
		installed <- tk.SetSchema(context.Background(), "gql", fixtureSDL(t, "flat"))
	}()
	close(waiting)
	res, _, err := tk.handleQuery(context.Background(), nil, QueryInput{
		Connection: "gql", Query: datasetDocument, Variables: jsonRaw(t, map[string]any{"urn": "u1"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-installed; err != nil {
		t.Fatalf("installing the schema: %v", err)
	}
	if res.IsError {
		t.Fatalf("the waiting call was refused: %s", resultText(res))
	}
}

// A connection that holds a schema, one whose read failed, an unknown name
// and no name all go on to the call's own handling without waiting.
func TestSchemaPendingLetsEveryOtherStateThrough(t *testing.T) {
	ready := newToolkit(t, newUpstream(t), "flat", nil)
	if res := ready.schemaPending(context.Background(), "gql"); res != nil {
		t.Fatalf("a connection holding its schema waited: %v", resultText(res))
	}
	if res := ready.schemaPending(context.Background(), "nope"); res != nil {
		t.Fatal("an unknown connection was refused here rather than by the call")
	}
	if res := ready.schemaPending(context.Background(), ""); res != nil {
		t.Fatal("an empty name was refused here rather than by the call")
	}

	failed := newToolkit(t, newUpstream(t), "", nil)
	c, _, _ := failed.lookup("gql")
	c.schemaMu.Lock()
	c.schemaErr = "introspection is disabled"
	c.schemaMu.Unlock()
	start := time.Now()
	if res := failed.schemaPending(context.Background(), "gql"); res != nil {
		t.Fatal("a connection whose read failed was answered as still reading")
	}
	if time.Since(start) > time.Second {
		t.Fatal("a failed read was waited on")
	}
}

// A call whose context ends while it waits answers at once with the pending
// refusal.
func TestSchemaPendingStopsWithTheCallsContext(t *testing.T) {
	tk := newToolkit(t, newUpstream(t), "", nil)
	tk.SetSchemaWait(time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if code := pendingCode(t, tk.schemaPending(ctx, "gql")); code != upstreamretry.CodeUnavailable {
		t.Fatalf("code = %q", code)
	}
}

func TestSchemaWaitBoundDefaultsAndOverrides(t *testing.T) {
	tk := newToolkit(t, newUpstream(t), "", nil)
	if got := tk.schemaWaitBound(); got != defaultSchemaWait {
		t.Fatalf("default = %v", got)
	}
	tk.SetSchemaWait(time.Second)
	if got := tk.schemaWaitBound(); got != time.Second {
		t.Fatalf("override = %v", got)
	}
	tk.SetSchemaWait(0)
	if got := tk.schemaWaitBound(); got != defaultSchemaWait {
		t.Fatalf("reset = %v", got)
	}
}
