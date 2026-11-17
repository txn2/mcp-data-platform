package siteenv

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
)

func yes() *bool { v := true; return &v }
func no() *bool  { v := false; return &v }

func addOn(t *testing.T, ops Ops, host string, role DomainRole) *DomainResult {
	t.Helper()
	res, err := ops.AddDomainWithRole(context.Background(), owner, sitesvc.DomainRequest{Hostname: host, Site: "marketing", Environment: "stage"}, role)
	require.NoError(t, err)
	return res
}

// TestDomainRole_PrimaryAndRedirectInEitherDirection pins #2687: one domain is
// primary, another redirects to it, and the roles swap with one call each.
func TestDomainRole_PrimaryAndRedirectInEitherDirection(t *testing.T) {
	ops, store, _ := fixture(t)
	ctx := context.Background()
	var invalidated []string
	ops.Invalidate = func(h string) { invalidated = append(invalidated, h) }

	bare := addOn(t, ops, "example.com", DomainRole{Primary: yes()})
	assert.True(t, bare.Primary)
	www := addOn(t, ops, "www.example.com", DomainRole{Mode: site.DomainRedirect})
	assert.Equal(t, site.DomainRedirect, www.Mode)
	assert.False(t, www.Primary)

	// The other direction: www becomes primary and serves; the bare domain
	// stops being primary and is then set to redirect.
	d, err := ops.SetDomainRole(ctx, owner, "www.example.com", DomainRole{Primary: yes()})
	require.NoError(t, err)
	assert.True(t, d.Primary)
	assert.Equal(t, site.DomainServe, d.Mode, "a domain made primary serves")
	assert.False(t, store.domains["example.com"].Primary, "the previous primary gave up the mark")
	assert.Contains(t, invalidated, "example.com", "the domains beside it are re-read")
	d, err = ops.SetDomainRole(ctx, owner, "example.com", DomainRole{Mode: site.DomainRedirect})
	require.NoError(t, err)
	assert.Equal(t, site.DomainRedirect, d.Mode)
}

func TestDomainRole_Refusals(t *testing.T) {
	ops, _, _ := fixture(t)
	ctx := context.Background()
	refusedWith := func(t *testing.T, err error, text string) {
		t.Helper()
		var r *sitesvc.RefusedError
		require.ErrorAs(t, err, &r)
		assert.Contains(t, r.Msg, text)
	}

	_, err := ops.AddDomainWithRole(ctx, owner, sitesvc.DomainRequest{Hostname: "www.example.com", Site: "marketing", Environment: "stage"}, DomainRole{Mode: site.DomainRedirect})
	refusedWith(t, err, "no primary domain to redirect to")
	_, err = ops.Store.Domain(ctx, "www.example.com")
	require.ErrorIs(t, err, site.ErrNoDomain, "a domain the role refuses is never added")

	_, err = ops.AddDomainWithRole(ctx, owner, sitesvc.DomainRequest{Hostname: "x.example.com", Site: "marketing", Environment: "stage"}, DomainRole{Mode: "bounce"})
	refusedWith(t, err, `mode "bounce"`)
	_, err = ops.AddDomainWithRole(ctx, owner, sitesvc.DomainRequest{Hostname: "x.example.com", Site: "marketing", Environment: "stage"}, DomainRole{Primary: yes(), Mode: site.DomainRedirect})
	refusedWith(t, err, "cannot be the primary and redirect")
	_, err = ops.AddDomainWithRole(ctx, admin, sitesvc.DomainRequest{Hostname: "app.example.com", Application: "shop"}, DomainRole{Primary: yes()})
	refusedWith(t, err, "application")

	addOn(t, ops, "example.com", DomainRole{Primary: yes()})
	addOn(t, ops, "www.example.com", DomainRole{Mode: site.DomainRedirect})
	_, err = ops.SetDomainRole(ctx, owner, "example.com", DomainRole{Primary: no()})
	refusedWith(t, err, "www.example.com redirect to")
	refusedWith(t, ops.RemoveDomain(ctx, owner, "example.com"), "before removing it")
	_, err = ops.SetDomainRole(ctx, stranger, "example.com", DomainRole{Primary: yes()})
	require.ErrorIs(t, err, site.ErrNotAllowed)
	_, err = ops.SetDomainRole(ctx, owner, "none.example.com", DomainRole{Primary: yes()})
	require.ErrorIs(t, err, site.ErrNoDomain)

	// With the redirect set to serve, the primary may go.
	_, err = ops.SetDomainRole(ctx, owner, "www.example.com", DomainRole{Mode: site.DomainServe})
	require.NoError(t, err)
	require.NoError(t, ops.RemoveDomain(ctx, owner, "example.com"))
}

func TestDomainRole_AnApplicationDomainHasNoRole(t *testing.T) {
	ops, store, _ := fixture(t)
	store.domains["shop.example.com"] = &site.Domain{Hostname: "shop.example.com", ApplicationID: "app_1", Application: "shop"}
	_, err := ops.SetDomainRole(context.Background(), admin, "shop.example.com", DomainRole{Primary: yes()})
	var r *sitesvc.RefusedError
	require.ErrorAs(t, err, &r)
	assert.Contains(t, r.Msg, "never a primary")
}

func TestApplied(t *testing.T) {
	served := &site.Domain{}
	p, m := applied(served, DomainRole{})
	assert.False(t, p)
	assert.Equal(t, site.DomainServe, m)
	p, m = applied(&site.Domain{Mode: site.DomainRedirect}, DomainRole{Primary: yes()})
	assert.True(t, p)
	assert.Equal(t, site.DomainServe, m, "made primary, it serves")
}

func TestSetDomain_RoleThenAccess(t *testing.T) {
	ops, store, _ := fixture(t)
	ctx := context.Background()
	addOn(t, ops, "example.com", DomainRole{})
	_, err := ops.SetDomain(ctx, owner, "example.com", DomainChange{})
	var r *sitesvc.RefusedError
	require.ErrorAs(t, err, &r)
	assert.Contains(t, r.Msg, "say public, primary or mode")

	res, err := ops.SetDomain(ctx, owner, "example.com", DomainChange{Role: DomainRole{Primary: yes()}})
	require.NoError(t, err)
	assert.True(t, res.Primary)
	assert.False(t, res.Public, "the access is left alone")

	res, err = ops.SetDomain(ctx, owner, "example.com", DomainChange{Public: yes(), Reason: "launch", Role: DomainRole{Mode: site.DomainServe}})
	require.NoError(t, err)
	assert.True(t, store.domains["example.com"].Primary)
	assert.Equal(t, StatusDone, res.Status)

	_, err = ops.SetDomain(ctx, owner, "example.com", DomainChange{Role: DomainRole{Mode: site.DomainRedirect}})
	require.ErrorAs(t, err, &r)
}
