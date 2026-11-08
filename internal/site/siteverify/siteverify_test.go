package siteverify

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/site/siteconfig"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
)

const (
	host  = "www.example.com"
	token = "platform-site-verify=abc123"
)

// toServer sends every request to srv, leaving the Host the verifier asked for.
type toServer struct{ srv *httptest.Server }

func (t toServer) RoundTrip(r *http.Request) (*http.Response, error) {
	u, err := url.Parse(t.srv.URL)
	if err != nil {
		return nil, err //nolint:wrapcheck // the fixture's own address
	}
	c := r.Clone(r.Context())
	c.Host = r.URL.Host
	c.URL.Scheme, c.URL.Host = u.Scheme, u.Host
	return http.DefaultTransport.RoundTrip(c) //nolint:wrapcheck // a test transport hands back what the server said
}

func serving(t *testing.T, status int, body string) *http.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != host || r.URL.Path != WellKnownPath {
			w.WriteHeader(http.StatusTeapot)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return &http.Client{Transport: toServer{srv}}
}

func txt(records []string, err error) Lookup {
	return func(_ context.Context, name string) ([]string, error) {
		if name != TXTPrefix+host {
			return nil, errors.New("looked up the wrong name: " + name)
		}
		return records, err
	}
}

func TestVerify_TXT(t *testing.T) {
	cases := []struct {
		name    string
		records []string
		err     error
		pass    bool
	}{
		{"the token", []string{token}, nil, true},
		{"among others", []string{"v=spf1 -all", token}, nil, true},
		{"with space around it", []string{"  " + token + " "}, nil, true},
		{"absent", []string{"v=spf1 -all"}, nil, false},
		{"none", nil, nil, false},
		{"a record that only holds the token", []string{"x" + token}, nil, false},
		{"a failed lookup", []string{token}, errors.New("servfail"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := &Verifier{TXT: txt(tc.records, tc.err)}
			err := v.Verify(context.Background(), host, token)
			if tc.pass {
				require.NoError(t, err)
				return
			}
			var r *sitesvc.RefusedError
			require.ErrorAs(t, err, &r)
		})
	}
}

func TestVerify_HTTP(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		pass   bool
	}{
		{"the token", http.StatusOK, token, true},
		{"with a trailing newline", http.StatusOK, token + "\n", true},
		{"another body", http.StatusOK, "something else", false},
		{"an empty body", http.StatusOK, "", false},
		{"the token inside a page", http.StatusOK, "<p>" + token + "</p>", false},
		{"the token past the bound", http.StatusOK, strings.Repeat(" ", maxBody) + token, false},
		{"not found", http.StatusNotFound, token, false},
		{"a server error", http.StatusInternalServerError, token, false},
		{"a redirect status", http.StatusFound, token, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := &Verifier{HTTP: serving(t, tc.status, tc.body)}
			err := v.Verify(context.Background(), host, token)
			if tc.pass {
				require.NoError(t, err)
				return
			}
			var r *sitesvc.RefusedError
			require.ErrorAs(t, err, &r)
		})
	}
}

func TestVerify_EitherCheckIsEnough(t *testing.T) {
	v := &Verifier{TXT: txt(nil, errors.New("nxdomain")), HTTP: serving(t, http.StatusOK, token)}
	require.NoError(t, v.Verify(context.Background(), host, token), "the HTTP check stands in for a failed lookup")

	v = &Verifier{TXT: txt([]string{token}, nil), HTTP: serving(t, http.StatusNotFound, "")}
	require.NoError(t, v.Verify(context.Background(), host, token), "the TXT record stands in for a failed fetch")
}

// What a hostname serves is never repeated: the refusal is a fixed sentence
// naming only what the operator must do.
func TestVerify_TheRefusalSaysHowAndRepeatsNothingServed(t *testing.T) {
	v := &Verifier{TXT: txt([]string{"secret-record"}, nil), HTTP: serving(t, http.StatusOK, "secret-body")}
	err := v.Verify(context.Background(), host, token)
	var r *sitesvc.RefusedError
	require.ErrorAs(t, err, &r)
	assert.Contains(t, r.Msg, TXTPrefix+host)
	assert.Contains(t, r.Msg, "http://"+host+WellKnownPath)
	assert.Contains(t, r.Msg, token)
	assert.NotContains(t, r.Msg, "secret-record")
	assert.NotContains(t, r.Msg, "secret-body")
}

func TestVerify_NothingToCheckWith(t *testing.T) {
	var r *sitesvc.RefusedError
	require.ErrorAs(t, (&Verifier{}).Verify(context.Background(), host, token), &r)
}

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("refused by the egress guard")
}

type brokenBody struct{}

func (brokenBody) Read([]byte) (int, error) { return 0, errors.New("broke off") }
func (brokenBody) Close() error             { return nil }

type bodyTransport struct{}

func (bodyTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Body: brokenBody{}, Header: http.Header{}}, nil
}

func TestVerify_HTTPFailuresAreANo(t *testing.T) {
	var r *sitesvc.RefusedError
	for name, rt := range map[string]http.RoundTripper{"no answer": failingTransport{}, "a body that breaks off": bodyTransport{}} {
		t.Run(name, func(t *testing.T) {
			v := &Verifier{HTTP: &http.Client{Transport: rt}}
			require.ErrorAs(t, v.Verify(context.Background(), host, token), &r)
		})
	}
	t.Run("a hostname that cannot be requested", func(t *testing.T) {
		v := &Verifier{HTTP: &http.Client{Transport: failingTransport{}}}
		require.ErrorAs(t, v.Verify(context.Background(), "bad host\x7f", token), &r)
	})
}

func TestNew(t *testing.T) {
	v, err := New(siteconfig.NetConfig{AllowPrivateCIDRs: []string{"10.0.0.0/8"}})
	require.NoError(t, err)
	assert.NotNil(t, v.TXT)
	assert.NotNil(t, v.HTTP)

	_, err = New(siteconfig.NetConfig{AllowPrivateCIDRs: []string{"not-a-cidr"}})
	require.ErrorContains(t, err, "egress guard")
}

// The real client's guard refuses a private address, so the HTTP check cannot
// be aimed inside the deployment.
func TestNew_TheHTTPCheckCannotReachAPrivateAddress(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write([]byte(token))
	}))
	t.Cleanup(srv.Close)

	v, err := New(siteconfig.NetConfig{})
	require.NoError(t, err)
	v.TXT = nil
	var r *sitesvc.RefusedError
	require.ErrorAs(t, v.Verify(context.Background(), srv.Listener.Addr().String(), token), &r)
	assert.Zero(t, hits)

	admitted, err := New(siteconfig.NetConfig{AllowPrivateCIDRs: []string{"127.0.0.0/8"}})
	require.NoError(t, err)
	admitted.TXT = nil
	require.NoError(t, admitted.Verify(context.Background(), srv.Listener.Addr().String(), token), "an operator can admit a private range")
}

func TestResolver(t *testing.T) {
	assert.Same(t, net.DefaultResolver, resolver(""))

	l, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
	accepted := make(chan struct{})
	go func() {
		c, aerr := l.Accept()
		if aerr == nil {
			_ = c.Close()
		}
		close(accepted)
	}()

	r := resolver(l.Addr().String())
	assert.True(t, r.PreferGo)
	c, err := r.Dial(context.Background(), "tcp", "8.8.8.8:53")
	require.NoError(t, err, "the address the resolver asked for is replaced by the configured server")
	_ = c.Close()
	<-accepted
}
