package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/admin"
	"github.com/txn2/mcp-data-platform/pkg/connoauth"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

func TestParseDurationWithDays(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want time.Duration
		err  bool
	}{
		{"60d", 60 * 24 * time.Hour, false},
		{"1d", 24 * time.Hour, false},
		{"0d", 0, false},
		{"-5d", 0, true},
		{"abc", 0, true},
		{"30m", 30 * time.Minute, false},
		{"1h30m", 90 * time.Minute, false},
		{"", 0, true},
		{"d", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := parseDurationWithDays(tc.in)
			if tc.err {
				if err == nil {
					t.Errorf("want error for %q, got %v", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Errorf("parseDurationWithDays(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("parseDurationWithDays(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestReadMaxLifetime(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cfg  map[string]any
		want time.Duration
	}{
		{"absent", map[string]any{}, 0},
		{"empty", map[string]any{configKeyOAuthRefreshMaxLifetime: ""}, 0},
		{"non-string", map[string]any{configKeyOAuthRefreshMaxLifetime: 60}, 0},
		{"bad value", map[string]any{configKeyOAuthRefreshMaxLifetime: "garbage"}, 0},
		{"60d", map[string]any{configKeyOAuthRefreshMaxLifetime: "60d"}, 60 * 24 * time.Hour},
		{"24h", map[string]any{configKeyOAuthRefreshMaxLifetime: "24h"}, 24 * time.Hour},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := readMaxLifetime("api", "x", tc.cfg)
			if got != tc.want {
				t.Errorf("readMaxLifetime = %v, want %v", got, tc.want)
			}
		})
	}
}

// stubConnStore satisfies the admin.ConnectionStore interface for the
// resolver tests. Only Get is exercised by the resolver; other
// methods return zero values.
type stubConnStore struct {
	inst *platform.ConnectionInstance
	err  error
}

func (s *stubConnStore) Get(_ context.Context, _, _ string) (*platform.ConnectionInstance, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.inst, nil
}

func (*stubConnStore) List(_ context.Context) ([]platform.ConnectionInstance, error) {
	return nil, nil
}

func (*stubConnStore) Set(_ context.Context, _ platform.ConnectionInstance) error {
	return nil
}

func (*stubConnStore) Delete(_ context.Context, _, _ string) error {
	return nil
}

type stubOAuthKindHandler struct {
	cfg connoauth.Config
	err error
}

func (s stubOAuthKindHandler) ParseOAuthConfig(_ map[string]any) (connoauth.Config, error) {
	return s.cfg, s.err
}

func (stubOAuthKindHandler) AfterConnect(_ context.Context, _ string, _ map[string]any) error {
	return nil
}

func TestConnOAuthConfigResolverResolveConfigUnknownKind(t *testing.T) {
	t.Parallel()
	r := &connOAuthConfigResolver{
		store: &stubConnStore{inst: &platform.ConnectionInstance{}},
		kinds: admin.OAuthKindHandlers{},
	}
	_, err := r.ResolveConfig(context.Background(), connoauth.Key{Kind: "unknown", Name: "x"})
	if !errors.Is(err, connoauth.ErrConfigNotResolvable) {
		t.Errorf("want ErrConfigNotResolvable for unknown kind, got %v", err)
	}
}

func TestConnOAuthConfigResolverResolveConfigStoreMiss(t *testing.T) {
	t.Parallel()
	r := &connOAuthConfigResolver{
		store: &stubConnStore{err: errors.New("not found")},
		kinds: admin.OAuthKindHandlers{"api": stubOAuthKindHandler{}},
	}
	_, err := r.ResolveConfig(context.Background(), connoauth.Key{Kind: "api", Name: "x"})
	if !errors.Is(err, connoauth.ErrConfigNotResolvable) {
		t.Errorf("want ErrConfigNotResolvable for store miss, got %v", err)
	}
}

func TestConnOAuthConfigResolverResolveConfigParseError(t *testing.T) {
	t.Parallel()
	r := &connOAuthConfigResolver{
		store: &stubConnStore{inst: &platform.ConnectionInstance{Config: map[string]any{}}},
		kinds: admin.OAuthKindHandlers{
			"api": stubOAuthKindHandler{err: errors.New("not oauth")},
		},
	}
	_, err := r.ResolveConfig(context.Background(), connoauth.Key{Kind: "api", Name: "x"})
	if !errors.Is(err, connoauth.ErrConfigNotResolvable) {
		t.Errorf("want ErrConfigNotResolvable for parse error, got %v", err)
	}
}

func TestConnOAuthConfigResolverResolveConfigSuccess(t *testing.T) {
	t.Parallel()
	want := connoauth.Config{TokenURL: "https://idp/token", ClientID: "c"}
	r := &connOAuthConfigResolver{
		store: &stubConnStore{inst: &platform.ConnectionInstance{Config: map[string]any{}}},
		kinds: admin.OAuthKindHandlers{"api": stubOAuthKindHandler{cfg: want}},
	}
	got, err := r.ResolveConfig(context.Background(), connoauth.Key{Kind: "api", Name: "x"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got.TokenURL != want.TokenURL {
		t.Errorf("TokenURL = %q, want %q", got.TokenURL, want.TokenURL)
	}
}

func TestConnOAuthConfigResolverMaxLifetimeAbsentFn(t *testing.T) {
	t.Parallel()
	r := &connOAuthConfigResolver{}
	if d := r.MaxLifetime(context.Background(), connoauth.Key{}); d != 0 {
		t.Errorf("MaxLifetime with nil fn = %v, want 0", d)
	}
}

func TestConnOAuthConfigResolverMaxLifetimeFromConfig(t *testing.T) {
	t.Parallel()
	r := &connOAuthConfigResolver{
		store: &stubConnStore{inst: &platform.ConnectionInstance{
			Config: map[string]any{configKeyOAuthRefreshMaxLifetime: "60d"},
		}},
		maxLifeFn: readMaxLifetime,
	}
	got := r.MaxLifetime(context.Background(), connoauth.Key{Kind: "api", Name: "x"})
	if got != 60*24*time.Hour {
		t.Errorf("MaxLifetime = %v, want 60d", got)
	}
}

func TestConnOAuthConfigResolverMaxLifetimeStoreMiss(t *testing.T) {
	t.Parallel()
	r := &connOAuthConfigResolver{
		store:     &stubConnStore{err: errors.New("not found")},
		maxLifeFn: readMaxLifetime,
	}
	got := r.MaxLifetime(context.Background(), connoauth.Key{Kind: "api", Name: "x"})
	if got != 0 {
		t.Errorf("MaxLifetime on miss = %v, want 0", got)
	}
}
