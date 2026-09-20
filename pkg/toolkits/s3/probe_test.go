package s3

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// s3Stub answers a ListBuckets with the S3 XML an SDK expects, or refuses it.
func s3Stub(t *testing.T, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`<Error><Code>AccessDenied</Code><Message>Access Denied</Message></Error>`))
			return
		}
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<ListAllMyBucketsResult><Buckets>
<Bucket><Name>exports</Name><CreationDate>2026-01-01T00:00:00.000Z</CreationDate></Bucket>
</Buckets><Owner><ID>owner</ID></Owner></ListAllMyBucketsResult>`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// probeConfig is a connection pointed at a stub endpoint.
func probeConfig(endpoint string) Config {
	return Config{
		Region: "us-east-1", AccessKeyID: "a", SecretAccessKey: "b",
		Endpoint: endpoint, UsePathStyle: true,
	}
}

// An endpoint that answers a bucket listing is a connection that works: the
// endpoint resolved and the credential was accepted. The count is reported
// because a listing of zero against the right credential and a listing of zero
// against a prefix that admits nothing are different situations (#1805).
func TestProbeConnection_EndpointAnswers(t *testing.T) {
	srv := s3Stub(t, http.StatusOK)
	readOnly := probeConfig(srv.URL)
	readOnly.ReadOnly = true

	tk, err := NewMulti(MultiConfig{
		DefaultConnection: "lake",
		Instances:         map[string]Config{"lake": probeConfig(srv.URL), "archive": readOnly},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tk.Close() })

	res := tk.ProbeConnection(context.Background(), "lake")
	require.True(t, res.OK, "probe failed: %+v", res)
	assert.Contains(t, res.Detail, "1 bucket(s)")
	assert.NotContains(t, res.Detail, "read_only")

	archive := tk.ProbeConnection(context.Background(), "archive")
	require.True(t, archive.OK)
	assert.Contains(t, archive.Detail, "read_only")
}

// An endpoint that refuses the listing is a failure carrying its own words.
func TestProbeConnection_EndpointRefuses(t *testing.T) {
	srv := s3Stub(t, http.StatusForbidden)

	tk, err := NewMulti(MultiConfig{
		DefaultConnection: "lake",
		Instances:         map[string]Config{"lake": probeConfig(srv.URL)},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tk.Close() })

	res := tk.ProbeConnection(context.Background(), "lake")
	assert.False(t, res.OK)
	assert.Contains(t, res.Detail, "refused a bucket listing")
	assert.NotEmpty(t, res.Error)
}

// A name the toolkit does not serve is reported as that.
func TestProbeConnection_UnknownConnection(t *testing.T) {
	srv := s3Stub(t, http.StatusOK)

	tk, err := NewMulti(MultiConfig{
		DefaultConnection: "lake",
		Instances:         map[string]Config{"lake": probeConfig(srv.URL)},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tk.Close() })

	res := tk.ProbeConnection(context.Background(), "nope")
	assert.False(t, res.OK)
	assert.Contains(t, res.Detail, "could not be opened")
}

// A toolkit with no client at all answers rather than panicking, which is the
// shape an admin process that wired no S3 has.
func TestProbeConnection_NoClient(t *testing.T) {
	res := (&Toolkit{}).ProbeConnection(context.Background(), "lake")
	assert.False(t, res.OK)
	assert.Contains(t, res.Detail, "no S3 client")
}
