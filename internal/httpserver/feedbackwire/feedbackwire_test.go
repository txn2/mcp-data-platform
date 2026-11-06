package feedbackwire

import (
	"context"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/portal/mention"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/user"
)

type fakeDirectory struct{ user.Store }

func (fakeDirectory) Get(_ context.Context, email string) (*user.User, error) {
	return &user.User{Email: email, Roles: []string{"analyst"}}, nil
}

type fakeAudit struct{}

func (fakeAudit) Log(context.Context, middleware.AuditEvent) error { return nil }

type fakePrompts struct{ prompt.Store }

func (fakePrompts) GetByID(context.Context, string) (*prompt.Prompt, error) {
	return &prompt.Prompt{ID: "p", Scope: prompt.ScopeGlobal}, nil
}

func TestWire(t *testing.T) {
	deps := portal.Deps{
		PromptStore: fakePrompts{},
		PersonaResolver: func(roles []string) *portal.PersonaInfo {
			if len(roles) > 0 {
				return &portal.PersonaInfo{Name: "analyst"}
			}
			return nil
		},
	}
	reach := Wire(&deps, Sources{Directory: fakeDirectory{}, Audit: fakeAudit{}, HasDatabase: true})
	if reach == nil {
		t.Fatal("no reach built")
	}
	if _, ok := deps.MentionResolver.(*mention.Service); !ok {
		t.Errorf("the mention resolver is %T; want the reach-backed service", deps.MentionResolver)
	}
	if deps.AuditLogger == nil {
		t.Error("the audit logger was not handed to the portal")
	}
	if !reach.CanOpen(context.Background(), "b@x.io", "prompt", "p") {
		t.Error("a known person opens a global prompt")
	}

	bare := portal.Deps{}
	if Wire(&bare, Sources{}) == nil || bare.MentionResolver != nil || bare.AuditLogger != nil {
		t.Errorf("with no database, directory or audit only the decision is built: %+v", bare.MentionResolver)
	}
}
