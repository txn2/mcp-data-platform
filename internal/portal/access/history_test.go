package access

import (
	"context"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/persona"
)

// sharesFor is a share store answering one direct permission and one through
// a collection.
type sharesFor struct {
	portaldomain.ShareStore
	direct, viaCollection portaldomain.SharePermission
}

func (s sharesFor) ListByAsset(context.Context, string) ([]portaldomain.Share, error) {
	if s.direct == "" {
		return nil, nil
	}
	return []portaldomain.Share{{SharedWithUserID: "reader", Permission: s.direct}}, nil
}

func (s sharesFor) GetUserAssetPermissionViaCollection(context.Context, string, string, string) (portaldomain.SharePermission, error) {
	return s.viaCollection, nil
}

// TestSeesAssetHistory: the owner, an administrator and an editor read an
// asset's earlier versions always; a viewer, and a person of an external
// persona whatever the share says, only when their persona's Earlier versions
// setting is on (#2226, #2290).
func TestSeesAssetHistory(t *testing.T) {
	asset := &portaldomain.Asset{ID: "a1", OwnerID: "owner"}
	reader := &User{UserID: "reader", Roles: []string{"r"}}
	on, off := true, false
	history := func([]string) persona.PortalAccess { return persona.PortalAccess{History: &on} }
	external := func([]string) persona.PortalAccess { return persona.PortalAccess{External: true, History: &off} }
	externalHistory := func([]string) persona.PortalAccess { return persona.PortalAccess{External: true, History: &on} }
	for _, tc := range []struct {
		name   string
		shares sharesFor
		user   *User
		rule   func([]string) bool
		portal func([]string) persona.PortalAccess
		want   bool
	}{
		{"the owner", sharesFor{}, &User{UserID: "owner"}, nil, nil, true},
		{"an administrator", sharesFor{}, &User{UserID: "x", Roles: []string{"root"}}, func(r []string) bool { return len(r) == 1 && r[0] == "root" }, nil, true},
		{"a direct editor, setting off", sharesFor{direct: portaldomain.PermissionEditor}, reader, nil, nil, true},
		{"an editor through a collection, setting off", sharesFor{viaCollection: portaldomain.PermissionEditor}, reader, nil, nil, true},
		{"an external editor, setting off", sharesFor{direct: portaldomain.PermissionEditor}, reader, nil, external, false},
		{"an external editor, setting on", sharesFor{direct: portaldomain.PermissionEditor}, reader, nil, externalHistory, true},
		{"a direct editor, setting on", sharesFor{direct: portaldomain.PermissionEditor}, reader, nil, history, true},
		{"an editor through a collection, setting on", sharesFor{viaCollection: portaldomain.PermissionEditor}, reader, nil, history, true},
		{"a viewer, setting off", sharesFor{direct: portaldomain.PermissionViewer}, reader, nil, nil, false},
		{"a viewer, setting on", sharesFor{direct: portaldomain.PermissionViewer}, reader, nil, history, true},
		{"a viewer through a collection, setting on", sharesFor{viaCollection: portaldomain.PermissionViewer}, reader, nil, history, true},
		{"no share, setting on", sharesFor{}, reader, nil, history, false},
		{"nobody", sharesFor{}, nil, nil, history, false},
	} {
		c := New(Config{Shares: tc.shares, AdminRule: tc.rule, PortalPermissions: tc.portal})
		if got := c.SeesAssetHistory(context.Background(), asset, tc.user); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
	if New(Config{}).SeesAssetHistory(context.Background(), nil, reader) {
		t.Error("no asset answered true")
	}
}

// TestIsAdmin_TheRuleWinsOverTheRoles: a wired admin rule decides, not the
// admin persona's roles (#2228).
func TestIsAdmin_TheRuleWinsOverTheRoles(t *testing.T) {
	c := New(Config{AdminRoles: []string{"admin"}, AdminRule: func([]string) bool { return false }})
	if c.IsAdmin(&User{Roles: []string{"admin"}}) {
		t.Error("holding the admin role made an administrator against the rule")
	}
	if !New(Config{AdminRoles: []string{"admin"}}).IsAdmin(&User{Roles: []string{"admin"}}) {
		t.Error("with no rule, the admin role no longer makes an administrator")
	}
}
