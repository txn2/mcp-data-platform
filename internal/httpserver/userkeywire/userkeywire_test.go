package userkeywire

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/platform/userdir"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// The user directory the key manager resolves bound keys through is what Wire
// finds a recorder in; a change to its signature must fail here, not leave the
// own-key route quietly unsettled (#2279).
var _ callerRecorder = (*userdir.Handle)(nil)

type recordedCaller struct {
	subject, email, authType string
	roles                    []string
}

type fakeRecorder struct{ got []recordedCaller }

func (f *fakeRecorder) Record(_ context.Context, subject, email, authType string, roles []string) error {
	f.got = append(f.got, recordedCaller{subject, email, authType, roles})
	return nil
}

// settle records the request's caller as the authenticator saw them, and
// nobody for a request with no caller.
func TestSettleRecordsTheRequestsCaller(t *testing.T) {
	rec := &fakeRecorder{}
	bare := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/portal/api-keys", http.NoBody)
	if err := settle(bare, rec); err != nil || len(rec.got) != 0 {
		t.Fatalf("a request with no caller: err %v, recorded %v", err, rec.got)
	}

	ctx := portal.ContextWithUser(context.Background(), &portal.User{
		UserID: "sub-ana", Email: "ana@example.com", AuthType: "oidc", Roles: []string{"analyst"},
	})
	signed := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/portal/api-keys", http.NoBody)
	if err := settle(signed, rec); err != nil {
		t.Fatal(err)
	}
	if len(rec.got) != 1 || rec.got[0].subject != "sub-ana" || rec.got[0].email != "ana@example.com" || rec.got[0].authType != "oidc" {
		t.Errorf("recorded %+v; want the caller sub-ana / ana@example.com / oidc", rec.got)
	}
}
