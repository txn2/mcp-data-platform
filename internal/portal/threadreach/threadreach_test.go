package threadreach

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/portal/mention"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/user"
)

var errNoRow = errors.New("not found")

type fakeAssets struct {
	portaldomain.AssetStore
	assets map[string]*portaldomain.Asset
}

func (f *fakeAssets) Get(_ context.Context, id string) (*portaldomain.Asset, error) {
	if a, ok := f.assets[id]; ok {
		return a, nil
	}
	return nil, errNoRow
}

type fakeCollections struct {
	portaldomain.CollectionStore
	colls map[string]*portaldomain.Collection
}

func (f *fakeCollections) Get(_ context.Context, id string) (*portaldomain.Collection, error) {
	if c, ok := f.colls[id]; ok {
		return c, nil
	}
	return nil, errNoRow
}

type fakeShares struct {
	portaldomain.ShareStore
	byAsset  map[string][]portaldomain.Share
	assetErr error
	collPerm map[string]portaldomain.SharePermission // key: collectionID|email
	prompts  []portaldomain.SharedPromptRef
}

func (f *fakeShares) ListByAsset(_ context.Context, id string) ([]portaldomain.Share, error) {
	return f.byAsset[id], f.assetErr
}

func (*fakeShares) GetUserAssetPermissionViaCollection(context.Context, string, string, string) (portaldomain.SharePermission, error) {
	return "", nil
}

func (f *fakeShares) GetUserCollectionPermission(_ context.Context, collID, _, email string) (portaldomain.SharePermission, error) {
	return f.collPerm[collID+"|"+email], nil
}

func (f *fakeShares) ListSharedPromptsWithUser(context.Context, string, string) ([]portaldomain.SharedPromptRef, error) {
	return f.prompts, nil
}

type fakePrompts struct {
	prompts map[string]*prompt.Prompt
}

func (f *fakePrompts) GetByID(_ context.Context, id string) (*prompt.Prompt, error) {
	return f.prompts[id], nil
}

type fakePages struct {
	knowledgepage.Store
	page *knowledgepage.Page
	err  error
}

func (f *fakePages) Get(context.Context, string) (*knowledgepage.Page, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.page == nil {
		return nil, knowledgepage.ErrNotFound
	}
	return f.page, nil
}

type fakeDirectory map[string][]string

func (d fakeDirectory) Get(_ context.Context, email string) (*user.User, error) {
	roles, ok := d[email]
	if !ok {
		return nil, user.ErrNotFound
	}
	return &user.User{Email: email, Roles: roles}, nil
}

func newReach(shares *fakeShares, pages *fakePages) *Reach {
	deleted := time.Now()
	assets := &fakeAssets{assets: map[string]*portaldomain.Asset{
		"a1":   {ID: "a1", OwnerID: "owner-sub", OwnerEmail: "owner@x.io"},
		"gone": {ID: "gone", OwnerEmail: "owner@x.io", DeletedAt: &deleted},
	}}
	colls := &fakeCollections{colls: map[string]*portaldomain.Collection{
		"c1": {ID: "c1", OwnerID: "owner-sub", OwnerEmail: "owner@x.io"},
	}}
	prompts := &fakePrompts{prompts: map[string]*prompt.Prompt{
		"personal": {ID: "personal", Scope: prompt.ScopePersonal, OwnerEmail: "owner@x.io"},
		"global":   {ID: "global", Scope: prompt.ScopeGlobal},
	}}
	checker := access.New(access.Config{
		Assets: assets, Collections: colls, Shares: shares, Prompts: nil,
		AdminRoles: []string{"admin"},
	})
	return New(Config{
		Access: checker, Assets: assets, Collections: colls, Prompts: prompts, KnowledgePages: pages,
		Directory: fakeDirectory{
			"persona@x.io": {"analyst"},
			"admin@x.io":   {"admin"},
			"known@x.io":   {},
			"owner@x.io":   {},
		},
		PersonaName: func(roles []string) string {
			if len(roles) == 1 && roles[0] == "analyst" {
				return "analyst"
			}
			return ""
		},
	})
}

func TestCanOpen(t *testing.T) {
	shares := &fakeShares{
		byAsset: map[string][]portaldomain.Share{"a1": {
			{ID: "s1", AssetID: "a1", SharedWithEmail: "emailed@x.io", Permission: portaldomain.PermissionViewer},
			{ID: "s2", AssetID: "a1", SharedWithPersona: "analyst", Permission: portaldomain.PermissionViewer},
			{ID: "s3", AssetID: "a1", SharedWithEmail: "revoked@x.io", Revoked: true},
			{ID: "s4", AssetID: "a1", SharedWithUserID: "someone-sub"},
		}},
		collPerm: map[string]portaldomain.SharePermission{"c1|coll@x.io": portaldomain.PermissionViewer},
		prompts:  []portaldomain.SharedPromptRef{},
	}
	r := newReach(shares, &fakePages{page: &knowledgepage.Page{ID: "p1"}})
	ctx := context.Background()
	cases := []struct {
		name, email, kind, id string
		want                  bool
	}{
		{"owner by address", "owner@x.io", targetAsset, "a1", true},
		{"email share", "emailed@x.io", targetAsset, "a1", true},
		{"persona share", "persona@x.io", targetAsset, "a1", true},
		{"administrator", "admin@x.io", targetAsset, "a1", true},
		{"revoked share", "revoked@x.io", targetAsset, "a1", false},
		{"no grant", "known@x.io", targetAsset, "a1", false},
		{"unknown address, no grant", "stranger@x.io", targetAsset, "a1", false},
		{"deleted asset", "owner@x.io", targetAsset, "gone", false},
		{"missing asset", "owner@x.io", targetAsset, "nope", false},
		{"collection owner by address", "owner@x.io", targetCollection, "c1", true},
		{"collection share", "coll@x.io", targetCollection, "c1", true},
		{"collection no grant", "known@x.io", targetCollection, "c1", false},
		{"missing collection", "owner@x.io", targetCollection, "nope", false},
		{"personal prompt owner", "owner@x.io", targetPrompt, "personal", true},
		{"personal prompt stranger", "known@x.io", targetPrompt, "personal", false},
		{"global prompt known person", "known@x.io", targetPrompt, "global", true},
		{"global prompt unknown address", "stranger@x.io", targetPrompt, "global", false},
		{"missing prompt", "owner@x.io", targetPrompt, "nope", false},
		{"knowledge page known person", "known@x.io", targetKnowledgePage, "p1", true},
		{"knowledge page unknown address", "stranger@x.io", targetKnowledgePage, "p1", false},
		{"standalone known person", "known@x.io", targetStandalone, "", true},
		{"standalone unknown address", "stranger@x.io", targetStandalone, "", false},
		{"unknown target type", "owner@x.io", "bogus", "x", false},
		{"empty address", "  ", targetAsset, "a1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := r.CanOpen(ctx, tc.email, tc.kind, tc.id); got != tc.want {
				t.Errorf("CanOpen(%q, %s/%s) = %v; want %v", tc.email, tc.kind, tc.id, got, tc.want)
			}
		})
	}
}

func TestDecideOutcomes(t *testing.T) {
	ctx := context.Background()
	u := &access.User{UserID: "u", Email: "known@x.io"}
	r := newReach(&fakeShares{assetErr: errors.New("boom")}, &fakePages{err: errors.New("boom")})
	if got := r.Decide(ctx, u, true, targetAsset, "a1"); got != Failed {
		t.Errorf("share lookup failure = %v; want Failed", got)
	}
	if got := r.Decide(ctx, u, true, targetKnowledgePage, "p1"); got != Failed {
		t.Errorf("page lookup failure = %v; want Failed", got)
	}
	if got := r.Decide(ctx, u, true, "bogus", ""); got != BadTarget {
		t.Errorf("unknown kind = %v; want BadTarget", got)
	}
	if got := r.Decide(ctx, nil, true, targetAsset, "a1"); got != Failed {
		t.Errorf("no user = %v; want Failed", got)
	}
	var nilReach *Reach
	if nilReach.CanOpen(ctx, "a@x.io", targetAsset, "a1") {
		t.Error("a nil Reach admits nobody")
	}
	empty := New(Config{Access: access.New(access.Config{})})
	for _, kind := range []string{targetAsset, targetCollection, targetPrompt, targetKnowledgePage} {
		if got := empty.Decide(ctx, u, true, kind, "x"); got != Unavailable {
			t.Errorf("%s with no store = %v; want Unavailable", kind, got)
		}
	}
	deleted := time.Now()
	gonePage := newReach(&fakeShares{}, &fakePages{page: &knowledgepage.Page{ID: "p", DeletedAt: &deleted}})
	if got := gonePage.Decide(ctx, u, true, targetKnowledgePage, "p"); got != Missing {
		t.Errorf("deleted page = %v; want Missing", got)
	}
}

func TestEligibleAndFilter(t *testing.T) {
	shares := &fakeShares{byAsset: map[string][]portaldomain.Share{"a1": {
		{ID: "s2", AssetID: "a1", SharedWithPersona: "analyst", Permission: portaldomain.PermissionViewer},
	}}}
	r := newReach(shares, &fakePages{})
	got, err := r.Eligible(context.Background(), mention.Target{Type: targetAsset, ID: "a1"},
		[]string{"persona@x.io", "stranger@x.io", "owner@x.io"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "persona@x.io" || got[1] != "owner@x.io" {
		t.Errorf("Eligible = %v; want the persona-share recipient and the owner, in order", got)
	}
	if out := r.Filter(context.Background(), targetAsset, "a1", nil); out == nil || len(out) != 0 {
		t.Errorf("Filter(nil) = %#v; want an empty list", out)
	}
}

func TestPersonOf(t *testing.T) {
	r := newReach(&fakeShares{}, &fakePages{})
	p := r.personOf(context.Background(), " Persona@X.io ")
	if !p.Known || p.Persona != "analyst" || p.User.Email != "persona@x.io" {
		t.Errorf("personOf = %+v", p)
	}
	if p.User.UserID == "" {
		t.Error("a named person is judged under a non-empty id, so an id share with no id never matches")
	}
	noDir := New(Config{})
	if p := noDir.personOf(context.Background(), "a@x.io"); p.Known {
		t.Error("with no directory nobody is known")
	}
}
