// Package sitecontract is where a site release's contract meets the
// application it is a front end of (#2679): a release's _application file
// declares the application's contract versions it works with, a promotion
// into an environment that is a part of that application is refused when the
// release excludes the application's contract, and an application release
// reads what each of its site parts serves.
package sitecontract

import (
	"context"
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/site"
)

// Parts names the application a site environment is a part of and the
// contract version it declares; ok is false for an environment that is a part
// of none.
type Parts func(ctx context.Context, siteName, environment string) (application string, version int, ok bool)

// Check refuses a promotion of rel into the environment when the environment
// is a part of an application whose contract the release does not work with.
func Check(ctx context.Context, parts Parts, siteName, environment string, rel *site.Release) error {
	if parts == nil || rel == nil {
		return nil
	}
	app, version, ok := parts(ctx, siteName, environment)
	if !ok || !rel.Contract.For(app) || rel.Contract.Accepts(version) {
		return nil
	}
	return fmt.Errorf("release %d of %s works with %s's contract %s, and %s is at contract %d, so %s is not promoted to it. "+
		"Publish a release whose _application file accepts contract %d, or change the application's contract",
		rel.Number, siteName, app, rel.Contract.Range, app, version, environment, version)
}

// Reader reads what a site part serves and the contract a release declares,
// for an application release's check.
type Reader struct {
	Store site.Store
	// Find reads a site by name or id.
	Find func(ctx context.Context, ref string) (*site.Site, error)
}

// ServedContract is the release an environment serves and the contract it
// declares, nil for none; 0 when it serves none.
func (r Reader) ServedContract(ctx context.Context, ref, environment string) (int, *site.Contract, error) {
	st, err := r.Find(ctx, ref)
	if err != nil {
		return 0, nil, err
	}
	env := st.Environment(environment)
	if env == nil {
		return 0, nil, fmt.Errorf("site %s has no environment %s", st.Name, environment)
	}
	if env.Release == 0 {
		return 0, nil, nil
	}
	c, err := r.ReleaseContract(ctx, st.Name, env.Release)
	return env.Release, c, err
}

// ReleaseContract is the contract a release declares, nil for none.
func (r Reader) ReleaseContract(ctx context.Context, ref string, number int) (*site.Contract, error) {
	st, err := r.Find(ctx, ref)
	if err != nil {
		return nil, err
	}
	rel, err := r.Store.Release(ctx, st.ID, number)
	if err != nil {
		return nil, fmt.Errorf("reading release %d: %w", number, err)
	}
	return rel.Contract, nil
}
