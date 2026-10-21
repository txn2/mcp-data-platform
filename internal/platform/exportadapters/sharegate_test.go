package exportadapters

import (
	"context"
	"errors"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// countingShares counts the shares that reach it.
type countingShares struct {
	portal.ShareStore
	inserted int
}

func (c *countingShares) Insert(context.Context, *portal.Share) error {
	c.inserted++
	return nil
}

func TestGatedShares_RefusesAPersonaWithoutSharing(t *testing.T) {
	inner := &countingShares{}
	if GatedShares(inner, nil) != portal.ShareStore(inner) {
		t.Error("a nil check wrapped the store")
	}
	gated := GatedShares(inner, func(name string) bool { return name == "analyst" })
	for _, tc := range []struct {
		name string
		pc   *middleware.PlatformContext
		ok   bool
	}{
		{"allowed persona", &middleware.PlatformContext{PersonaName: "analyst"}, true},
		{"denied persona", &middleware.PlatformContext{PersonaName: "viewer"}, false},
		{"admin", &middleware.PlatformContext{PersonaName: "viewer", IsAdmin: true}, true},
		{"no call", nil, false},
	} {
		ctx := context.Background()
		if tc.pc != nil {
			ctx = middleware.WithPlatformContext(ctx, tc.pc)
		}
		before := inner.inserted
		err := gated.Insert(ctx, &portal.Share{})
		if tc.ok != (err == nil) || tc.ok != (inner.inserted == before+1) {
			t.Errorf("%s: err %v, inserted %d", tc.name, err, inner.inserted-before)
		}
		if !tc.ok && !errors.Is(err, errSharingNotAllowed) {
			t.Errorf("%s: err %v", tc.name, err)
		}
	}
}
