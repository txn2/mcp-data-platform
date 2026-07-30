package connoauthapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// mockConnectionStore is the read-side double for ConnectionReader, mirroring
// the mock the parent admin package uses for the same store.
type mockConnectionStore struct {
	instances []platform.ConnectionInstance
	getResult *platform.ConnectionInstance
	listErr   error
	getErr    error
}

func (m *mockConnectionStore) List(_ context.Context) ([]platform.ConnectionInstance, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.instances, nil
}

func (m *mockConnectionStore) Get(_ context.Context, _, _ string) (*platform.ConnectionInstance, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	return m.getResult, nil
}

// strictDecode stands in for the parent's injected strict decoder. The
// parent's decode_test.go covers the real decoder's unknown-field and
// over-size behavior.
func strictDecode(_ http.ResponseWriter, r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return errors.New("invalid request body")
	}
	return nil
}

// strictDecodeOptional is strictDecode treating an empty body as success,
// standing in for the parent's decodeStrictOptional.
func strictDecodeOptional(w http.ResponseWriter, r *http.Request, dst any) error {
	if r.Body == nil || r.ContentLength == 0 {
		return nil
	}
	return strictDecode(w, r, dst)
}

// oauthCallbackPrefixes are the paths the parent's ServeHTTP routes to the
// public mux, because the IdP redirect carries PKCE state rather than an
// admin session.
var oauthCallbackPrefixes = []string{
	"/api/v1/admin/oauth/",
	"/api/v1/admin/api-gateway/oauth/",
}

// seamMux mounts the routes on two muxes and dispatches between them the way
// the parent's ServeHTTP does. Using one mux for both would hide a route
// registered on the wrong side — and a callback stranded behind admin auth is
// exactly the failure that would reach production silently.
type seamMux struct{ authed, public *http.ServeMux }

func (s *seamMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	for _, p := range oauthCallbackPrefixes {
		if strings.HasPrefix(r.URL.Path, p) {
			s.public.ServeHTTP(w, r)
			return
		}
	}
	s.authed.ServeHTTP(w, r)
}

// testMux mounts the routes with test defaults over cfg's stores and mode.
func testMux(cfg Config) *seamMux {
	if !cfg.Mutable {
		cfg.Mutable = true
	}
	if cfg.Author == nil {
		cfg.Author = func(context.Context) string { return "" }
	}
	if cfg.Decode == nil {
		cfg.Decode = strictDecode
	}
	if cfg.DecodeOptional == nil {
		cfg.DecodeOptional = strictDecodeOptional
	}
	s := &seamMux{authed: http.NewServeMux(), public: http.NewServeMux()}
	Register(s.authed, s.public, cfg)
	return s
}
