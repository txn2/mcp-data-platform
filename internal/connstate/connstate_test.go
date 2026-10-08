package connstate

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
)

// forget drops every recorded state, so a test starts from none.
func forget() {
	mu.Lock()
	defer mu.Unlock()
	state = map[key]string{}
}

func TestObserveAndState(t *testing.T) {
	forget()
	t.Cleanup(forget)

	if _, ok := State("api", "crm"); ok {
		t.Fatal("a connection no call used has a state")
	}
	Observe("api", "crm", Unreachable)
	Observe("api", "crm", Healthy)
	if s, ok := State("api", "crm"); !ok || s != Healthy {
		t.Errorf("State = %q, %v; want the last call's healthy", s, ok)
	}
	if _, ok := State("graphql", "crm"); ok {
		t.Error("a state leaked to another kind's connection of the same name")
	}

	Observe("", "crm", AuthFailed)
	Observe("api", "", AuthFailed)
	Observe("api", "crm", "")
	Observe("api", "crm", "broken")
	if s, _ := State("api", "crm"); s != Healthy {
		t.Errorf("an empty kind, connection or state, or one this package does not define, changed the record: %q", s)
	}
}

func TestFromHTTP(t *testing.T) {
	for _, tc := range []struct {
		status int
		err    error
		want   string
	}{
		{http.StatusOK, nil, Healthy},
		{http.StatusNotFound, nil, Healthy},
		{http.StatusForbidden, nil, Healthy},
		{http.StatusInternalServerError, nil, Healthy},
		{http.StatusUnauthorized, nil, AuthFailed},
		{http.StatusProxyAuthRequired, nil, AuthFailed},
		{http.StatusBadGateway, nil, Unreachable},
		{http.StatusServiceUnavailable, nil, Unreachable},
		{http.StatusGatewayTimeout, nil, Unreachable},
		{0, errors.New("dial tcp: connection refused"), Unreachable},
		{0, fmt.Errorf("round trip: %w", context.Canceled), ""},
		{0, nil, ""},
	} {
		if got := FromHTTP(tc.status, tc.err); got != tc.want {
			t.Errorf("FromHTTP(%d, %v) = %q, want %q", tc.status, tc.err, got, tc.want)
		}
	}
}

func TestFromResultText(t *testing.T) {
	codes := []string{"InvalidAccessKeyId", "SignatureDoesNotMatch"}
	for _, tc := range []struct {
		text string
		want string
	}{
		{"failed to list buckets: operation error S3: ListBuckets, api error InvalidAccessKeyId: The key does not exist", AuthFailed},
		{"api error SIGNATUREDOESNOTMATCH", AuthFailed},
		{"failed to list objects: dial tcp 10.0.0.1:9000: connect: connection refused", Unreachable},
		{"lookup minio: no such host", Unreachable},
		{"failed to get object: NoSuchKey: The specified key does not exist", Healthy},
	} {
		if got := FromResultText(tc.text, codes...); got != tc.want {
			t.Errorf("FromResultText(%q) = %q, want %q", tc.text, got, tc.want)
		}
	}
}
