package siterouting

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/platform/integration"
	"github.com/txn2/mcp-data-platform/internal/site"
)

func source() Source {
	return Source{
		Sites: func(context.Context) ([]site.Site, error) {
			return []site.Site{{Name: "handbook", MainEnvironment: "prod", Environments: []site.Environment{{Name: "prod"}, {Name: "dev"}}}}, nil
		},
		Domains: func(context.Context) ([]site.Domain, error) {
			return []site.Domain{
				{Hostname: "handbook.example.com", Site: "handbook", Environment: "prod", Public: true},
				{Hostname: "shop.example.com", Application: "shop"},
			}, nil
		},
		Apps: func(context.Context) ([]application.Application, error) {
			return []application.Application{{Name: "shop"}}, nil
		},
		Addressing: func() site.Addressing { return site.Addressing{PagesDomain: "pages.example.net", Scheme: "https"} },
	}
}

// TestItems pins #2659's item set: each environment's pages address, each
// application's page origin and each domain, keyed by hostname, with the
// entry fields the contract names.
func TestItems(t *testing.T) {
	items, err := source().Items(context.Background())
	require.NoError(t, err)
	byHost := map[string]map[string]any{}
	keys := make([]string, 0, len(items))
	for _, it := range items {
		byHost[it.Key] = it.Entry
		keys = append(keys, it.Key)
	}
	assert.Equal(t, []string{
		"handbook--dev.pages.example.net", "handbook.example.com", "handbook.pages.example.net",
		"shop.example.com", "shop.pages.example.net",
	}, keys)
	assert.Equal(t, map[string]any{"hostname": "handbook.pages.example.net", "kind": KindPages, "site": "handbook", "environment": "prod", "public": false},
		byHost["handbook.pages.example.net"])
	assert.Equal(t, map[string]any{"hostname": "shop.pages.example.net", "kind": KindApplication, "application": "shop", "public": false},
		byHost["shop.pages.example.net"])
	assert.Equal(t, true, byHost["handbook.example.com"]["public"])
	assert.Equal(t, "shop", byHost["shop.example.com"]["application"])
}

// TestItems_NoPagesDomain: with no pages domain only domains are served.
func TestItems_NoPagesDomain(t *testing.T) {
	s := source()
	s.Addressing = func() site.Addressing { return site.Addressing{} }
	items, err := s.Items(context.Background())
	require.NoError(t, err)
	assert.Len(t, items, 2)

	s.Domains = func(context.Context) ([]site.Domain, error) { return nil, nil }
	items, err = s.Items(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []integration.Item{}, items, "none is [], never null")
}

func TestItems_Failures(t *testing.T) {
	boom := errors.New("down")
	s := source()
	s.Sites = func(context.Context) ([]site.Site, error) { return nil, boom }
	_, err := s.Items(context.Background())
	require.ErrorContains(t, err, "listing sites")
	s = source()
	s.Apps = func(context.Context) ([]application.Application, error) { return nil, boom }
	_, err = s.Items(context.Background())
	require.ErrorContains(t, err, "listing applications")
	s = source()
	s.Domains = func(context.Context) ([]site.Domain, error) { return nil, boom }
	_, err = s.Items(context.Background())
	require.ErrorContains(t, err, "listing domains")
	assert.Empty(t, hostOf(""))
	assert.Empty(t, hostOf("::bad"))
}

// TestRefusal: each refused retry is answered with its own status.
func TestRefusal(t *testing.T) {
	for err, want := range map[error]int{
		fmt.Errorf("x: %w", integration.ErrNothingFailed):         http.StatusConflict,
		fmt.Errorf("ask again in 3s: %w", integration.ErrTooSoon): http.StatusTooManyRequests,
		ErrNoRouting:                http.StatusNotFound,
		integration.ErrNotBound:     http.StatusNotFound,
		errors.New("database down"): http.StatusInternalServerError,
	} {
		status, msg := (*Lookup)(nil).Refusal(err)
		assert.Equal(t, want, status, err.Error())
		assert.NotEmpty(t, msg)
		assert.NotContains(t, msg, "database down", "an unexpected error is not repeated")
	}
	_, msg := (*Lookup)(nil).Refusal(fmt.Errorf("ask again in 3s: %w", integration.ErrTooSoon))
	assert.Contains(t, msg, "3s", "a reader is told when to ask again")
}

// fakeBindings answers the three reads the Reader makes.
type fakeBindings struct {
	integration.Bindings
	err     error
	bound   bool
	items   map[string]integration.StoredItem
	retried bool
}

func (f *fakeBindings) Binding(context.Context, string) (*integration.Binding, error) {
	if f.err != nil {
		return nil, f.err
	}
	if !f.bound {
		return nil, nil //nolint:nilnil // no binding, as the store answers
	}
	return &integration.Binding{Operation: integration.OpSiteRouting, LastRunID: "run1", LastError: "upstream said no"}, nil
}

func (f *fakeBindings) ItemsByKey(context.Context, string, []string) (map[string]integration.StoredItem, error) {
	return f.items, nil
}

func (f *fakeBindings) RequestRetry(context.Context, string) error {
	if !f.bound {
		return integration.ErrNotBound
	}
	f.retried = true
	return nil
}

// TestLookup: no script chosen reads as nil and retries nothing; a failed
// host carries the run error for an administrator only; a site's domains and
// pages addresses each carry their status.
func TestLookup(t *testing.T) {
	store := &fakeBindings{items: map[string]integration.StoredItem{
		"a.example.com":              {Key: "a.example.com", Status: integration.StatusFailed, Message: "The site routing script failed", FailedRun: "run1"},
		"handbook.pages.example.net": {Key: "handbook.pages.example.net", Status: integration.StatusReady, Message: "routed"},
	}}
	l := &Lookup{Service: &integration.Service{Store: store}, Addressing: source().Addressing}
	assert.Nil(t, l.Of(context.Background(), true, []string{"a.example.com"}))
	require.ErrorIs(t, l.Retry(context.Background(), true, &site.Site{}, nil), integration.ErrNotBound)

	var none *Lookup
	assert.Nil(t, none.Of(context.Background(), true, []string{"a"}))
	require.ErrorIs(t, none.Retry(context.Background(), true, &site.Site{}, nil), ErrNoRouting)
	ds, pages := none.Site(context.Background(), false, &site.Site{}, []site.Domain{{Hostname: "a"}})
	assert.Nil(t, ds[0].Routing)
	assert.Nil(t, pages)

	store.bound = true
	owner := l.Of(context.Background(), false, []string{"a.example.com", "b.example.com"})
	assert.Equal(t, "failed", owner["a.example.com"].Status)
	assert.True(t, owner["a.example.com"].ScriptFailed)
	assert.Empty(t, owner["a.example.com"].RunError)
	assert.Equal(t, "pending", owner["b.example.com"].Status)
	admin := l.Of(context.Background(), true, []string{"a.example.com"})
	assert.Equal(t, "upstream said no", admin["a.example.com"].RunError)
	require.NoError(t, l.Retry(context.Background(), true, &site.Site{}, nil))
	assert.True(t, store.retried)

	st := &site.Site{Name: "handbook", MainEnvironment: "prod", Environments: []site.Environment{{Name: "prod"}}}
	domains := []site.Domain{{Hostname: "a.example.com"}}
	got, pages := l.Site(context.Background(), false, st, domains)
	assert.Equal(t, "failed", got[0].Routing.Status)
	assert.Nil(t, domains[0].Routing, "the caller's slice is not changed")
	assert.Equal(t, "ready", pages["prod"].Status)
	assert.Equal(t, "failed", l.Domains(context.Background(), false, domains)[0].Routing.Status)
	assert.Equal(t, "pending", l.Origin(context.Background(), false, "https://shop.pages.example.net").Status)
	assert.Nil(t, l.Origin(context.Background(), false, ""))

	// A reader retries only a site with a failed hostname (#2659).
	store.retried = false
	require.NoError(t, l.Retry(context.Background(), false, st, domains))
	assert.True(t, store.retried, "the site's failed domain may be retried by a reader")
	store.retried = false
	require.ErrorIs(t, l.Retry(context.Background(), false, st, nil), integration.ErrNothingFailed)
	assert.False(t, store.retried, "a site whose hostnames are routed has nothing to retry")

	store.err = errors.New("down")
	assert.Nil(t, l.Of(context.Background(), false, []string{"a.example.com"}), "a failed read shows nothing")
	got, pages = l.Site(context.Background(), false, st, domains)
	assert.Nil(t, got[0].Routing)
	assert.Nil(t, pages)
}
