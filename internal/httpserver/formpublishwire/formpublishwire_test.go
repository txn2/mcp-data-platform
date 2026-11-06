package formpublishwire

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/internal/approval"
	"github.com/txn2/mcp-data-platform/internal/approval/apprhttp"
	"github.com/txn2/mcp-data-platform/internal/endpoint/eppreview"
	"github.com/txn2/mcp-data-platform/internal/platform/notifydelivery"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/user"
)

type users struct {
	people []user.User
	err    error
	asked  user.Filter
}

func (u *users) List(_ context.Context, f user.Filter) ([]user.User, int, error) {
	u.asked = f
	return u.people, len(u.people), u.err
}

func isAdmin(roles []string) bool { return len(roles) > 0 && roles[0] == "admin" }

// TestApprovers names the administrators to tell: confirmed people whose last
// reported roles make them administrators, never a person without an email.
func TestApprovers(t *testing.T) {
	u := &users{people: []user.User{
		{Email: "a@b.io", Roles: []string{"admin"}}, {Email: "j@b.io", Roles: []string{"analyst"}}, {Roles: []string{"admin"}},
	}}
	assert.Equal(t, []string{"a@b.io"}, Approvers(u, isAdmin)(context.Background()))
	assert.True(t, u.asked.ConfirmedOnly)
	assert.Equal(t, []string{"key@b.io", "A@b.io"}, Approvers(u, isAdmin, "key@b.io", "A@b.io")(context.Background()),
		"a configured address is told, and an address is told once")
	u.err = errors.New("down")
	assert.Equal(t, []string{"key@b.io"}, Approvers(u, isAdmin, "key@b.io")(context.Background()), "the configured addresses survive a failed read")
	assert.Empty(t, Approvers(u, isAdmin)(context.Background()))
	assert.Equal(t, []string{"k@b.io"}, keyAdmins([]platform.APIKeyDef{
		{Email: "k@b.io", Roles: []string{"admin"}}, {Email: "a@b.io", Roles: []string{"analyst"}}, {Roles: []string{"admin"}},
	}, isAdmin))
}

type bridge struct {
	requested []notifydelivery.AccessNotice
	to        [][]string
	decided   []notifydelivery.AccessNotice
}

func (b *bridge) NotifyApprovalRequest(_ context.Context, to []string, a notifydelivery.AccessNotice) {
	b.to, b.requested = append(b.to, to), append(b.requested, a)
}

func (b *bridge) NotifyApprovalDecision(_ context.Context, a notifydelivery.AccessNotice) {
	b.decided = append(b.decided, a)
}

func TestNotifier(t *testing.T) {
	b := &bridge{}
	n := Notifier{Bridge: b}
	r := &approval.Request{
		ID: "apr_1", ScriptName: "intake", Version: 2, RequesterEmail: "j@b.io", Reason: "launch",
		Status: approval.StatusApproved, DecidedBy: "a@b.io", DecisionComment: "looks right",
	}
	n.Requested(context.Background(), r, []string{"a@b.io"})
	n.Decided(context.Background(), r)
	assert.Equal(t, [][]string{{"a@b.io"}}, b.to)
	assert.Equal(t, "version 2 of the form intake for use without signing in", b.requested[0].ItemTitle)
	assert.Equal(t, "launch", b.requested[0].Message)
	assert.Equal(t, "j@b.io", b.decided[0].To)
	assert.Equal(t, "a@b.io approved it. looks right", b.decided[0].Message)
	r.DecisionComment = ""
	n.Decided(context.Background(), r)
	assert.Equal(t, "a@b.io approved it.", b.decided[1].Message)
}

func TestActorAndOpener(t *testing.T) {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
	assert.Nil(t, Actor(isAdmin, func([]string) string { return "" })(r))
	assert.Nil(t, Opener(isAdmin)(r))
	r = r.WithContext(portal.ContextWithUser(r.Context(), &portal.User{UserID: "u1", Email: "a@b.io", Roles: []string{"admin"}, AuthType: "oidc"}))
	a := Actor(isAdmin, func([]string) string { return "admin" })(r)
	assert.Equal(t, approval.Actor{Email: "a@b.io", Admin: true, Persona: "admin"}, *a)
	o := Opener(isAdmin)(r)
	assert.Equal(t, "u1", o.UserID)
	assert.True(t, o.Admin)
}

func TestMountWithoutEndpoints(t *testing.T) {
	p, err := platform.New(platform.WithConfig(&platform.Config{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	assert.NotPanics(t, func() { Mount(http.NewServeMux(), p, Deps{}) })
}

// TestRegister mounts both routes: the preview the script page opens and
// the Approvals queue, each behind the portal's authentication.
func TestRegister(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	mux := http.NewServeMux()
	wrapped := 0
	register(mux, Deps{IsAdmin: isAdmin, Wrap: func(h http.Handler) http.Handler { wrapped++; return h }}, routes{
		approvals: &approval.Service{}, previews: &eppreview.Previews{}, db: db, personaOf: func([]string) string { return "" },
	})
	assert.Equal(t, 5, wrapped, "one preview route and four approval routes, all behind the portal's authentication")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, apprhttp.Prefix, http.NoBody))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestWireNoticesAndPersonaName(t *testing.T) {
	svc := &approval.Service{}
	wireNotices(svc, &bridge{}, &users{}, isAdmin, nil)
	assert.NotNil(t, svc.Notifier)
	assert.NotNil(t, svc.Approvers)

	assert.Empty(t, personaName(nil)([]string{"x"}))
	reg := persona.NewRegistry()
	_ = reg.Register(&persona.Persona{Name: "analyst", Roles: []string{"analyst"}})
	assert.Equal(t, "analyst", personaName(reg)([]string{"analyst"}))
	assert.Empty(t, personaName(reg)([]string{"nobody"}))
}
