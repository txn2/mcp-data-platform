// Package collectionsweepwire adds the collection item sweep (#2752) to the
// platform's retention sweeps. It is assembled in the HTTP layer because the
// sweep judges each item with the decision the portal's feedback routes are
// given (internal/portal/threadreach): the owner's roles from the known-users
// directory, the persona they resolve to, and the portal's authorization core.
// The directory records people, never a key, so an owner that is a standalone
// API key's identity (apikey:<name>) is judged by the roles that key holds, as
// it was when it added the item.
package collectionsweepwire

import (
	"context"
	"database/sql"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/collectionsweep"
	"github.com/txn2/mcp-data-platform/internal/portal/threadreach"
	"github.com/txn2/mcp-data-platform/pkg/auth"
)

// Platform is what the sweep is added to.
type Platform interface {
	DB() *sql.DB
	APIKeyAuthenticator() *auth.APIKeyAuthenticator
	AddCollectionItemSweep(remove func(ctx context.Context) (int64, error))
}

// Wire adds the sweep when the database, the decision and a share store that
// lists the collections granting an asset are all present.
func Wire(p Platform, reach *threadreach.Reach, shares any) {
	if p == nil || reach == nil {
		return
	}
	grants, _ := shares.(collectionsweep.Grants)
	people := owners{reach: reach}
	if keys := p.APIKeyAuthenticator(); keys != nil {
		people.keys = keys.ListKeys
	}
	if sweep := collectionsweep.New(p.DB(), people, reach.Access(), grants); sweep != nil {
		p.AddCollectionItemSweep(sweep.Run)
	}
}

// owners judges a collection's owner by the directory, or by the roles of the
// standalone API key the owner id names.
type owners struct {
	reach *threadreach.Reach
	keys  func() []auth.APIKeySummary
}

// Person implements collectionsweep.People.
func (o owners) Person(ctx context.Context, userID, email string) (user *access.User, persona string) {
	if name, ok := strings.CutPrefix(userID, auth.APIKeySubjectPrefix); ok && o.keys != nil {
		for _, k := range o.keys() {
			if k.Name == name && k.UserEmail == "" {
				return &access.User{UserID: userID, Email: email, Roles: k.Roles}, o.reach.PersonaName(k.Roles)
			}
		}
	}
	return o.reach.Person(ctx, userID, email)
}
