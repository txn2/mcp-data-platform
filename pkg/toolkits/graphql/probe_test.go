package graphql

import (
	"context"
	"strings"
	"testing"
)

// An endpoint that answers proves the URL resolves and the credential was
// accepted, which is what a caller asking "does this connection work" means
// (#1805).
func TestProbeConnection_EndpointAnswers(t *testing.T) {
	u := newUpstream(t)
	u.introspection = `{"data":{"__schema":{"queryType":{"name":"Query"},"types":[]}}}`
	tk := newToolkit(t, u, "", nil)
	t.Cleanup(func() { _ = tk.Close() })

	res := tk.ProbeConnection(context.Background(), "gql")

	if !res.OK {
		t.Fatalf("probe failed: %+v", res)
	}
	if !strings.Contains(res.Detail, "HTTP 200") {
		t.Errorf("detail does not report the status: %q", res.Detail)
	}
}

// An endpoint that refuses introspection has still ANSWERED, so it is a
// success that says so: that connection's schema comes from an upload or a
// catalog, and reporting it unhealthy would send an operator looking for a
// fault that is not there.
func TestProbeConnection_IntrospectionDisabled(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "", nil)
	t.Cleanup(func() { _ = tk.Close() })

	res := tk.ProbeConnection(context.Background(), "gql")

	if !res.OK {
		t.Fatalf("an endpoint that answered was reported as a failure: %+v", res)
	}
	if !strings.Contains(res.Detail, "declines introspection") {
		t.Errorf("detail does not say introspection was declined: %q", res.Detail)
	}
}

// A read_only connection says so, because writability is the other thing a
// caller is about to assume.
func TestProbeConnection_ReportsReadOnly(t *testing.T) {
	u := newUpstream(t)
	u.introspection = `{"data":{"__schema":{"queryType":{"name":"Query"},"types":[]}}}`
	tk := newToolkit(t, u, "", map[string]any{"read_only": true})
	t.Cleanup(func() { _ = tk.Close() })

	res := tk.ProbeConnection(context.Background(), "gql")

	if !strings.Contains(res.Detail, "read_only") {
		t.Errorf("detail does not mention read_only: %q", res.Detail)
	}
}

// An endpoint that cannot be reached is a failure carrying the transport's own
// words, which is what tells an operator whether the host, the TLS or the
// credential is wrong.
func TestProbeConnection_Unreachable(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "", nil)
	t.Cleanup(func() { _ = tk.Close() })
	u.server.Close()

	res := tk.ProbeConnection(context.Background(), "gql")

	if res.OK {
		t.Fatal("a closed endpoint was reported as healthy")
	}
	if res.Error == "" {
		t.Error("the failure carries no error")
	}
}

// A name this toolkit does not serve is an answer about that name, not an
// error the caller has to interpret.
func TestProbeConnection_UnknownConnection(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "", nil)
	t.Cleanup(func() { _ = tk.Close() })

	res := tk.ProbeConnection(context.Background(), "nope")

	if res.OK {
		t.Fatal("an unknown connection was reported as healthy")
	}
	if !strings.Contains(res.Detail, "not served") {
		t.Errorf("detail does not say the connection is unknown: %q", res.Detail)
	}
}
