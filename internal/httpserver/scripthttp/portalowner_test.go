package scripthttp

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// The owner route is the one script write that belongs to an administrator
// rather than to the script's owner (#1404).

// ownerPath is the route under test, on the script carol owns.
const ownerPath = "/api/v1/portal/scripts/script_2/owner"

// recordingAudit captures what the transfer recorded, which is the half of the
// action a reader of the audit log sees.
type recordingAudit struct {
	events []audit.Event
	err    error
}

func (r *recordingAudit) Log(_ context.Context, e audit.Event) error {
	r.events = append(r.events, e)
	return r.err
}

// ownerDeps assembles the transfer route's dependencies for one caller.
func ownerDeps(store *stubStore, user *PortalIdentity, log *recordingAudit) Deps {
	deps := portalDeps(store, nil, nil, user)
	if log != nil {
		deps.Audit = log
	}
	return deps
}

// TestPortalTransferOwner_MovesTheScriptAndRecordsIt is the load-bearing case:
// an administrator moves a script to themselves, the live row carries the new
// owner, the version advances (which is what carries the authority a run
// presents), and the move is in the audit log with both ends of it.
func TestPortalTransferOwner_MovesTheScriptAndRecordsIt(t *testing.T) {
	store := portalStore()
	log := &recordingAudit{}

	rec := servePortalRequest(t, ownerDeps(store, admin, log), http.MethodPut, ownerPath,
		`{"owner_email":"Admin@Example.com"}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body ownerResponse
	decodeInto(t, rec, &body)
	assert.Equal(t, "admin@example.com", body.OwnerEmail)
	assert.Equal(t, 1, body.Version)
	assert.Contains(t, body.Message, "belongs to you")
	assert.Equal(t, "admin@example.com", store.scripts[1].OwnerEmail)
	assert.Equal(t, script.Author{Email: "admin@example.com", Roles: []string{}}, store.transferredBy,
		"the version records the administrator, whose roles a run now presents")

	require.Len(t, log.events, 1)
	ev := log.events[0]
	assert.Equal(t, audit.EventTypeAdmin, ev.EventKind)
	assert.Equal(t, auditToolTransfer, ev.ToolName)
	assert.Equal(t, "admin@example.com", ev.UserEmail)
	assert.True(t, ev.Success)
	assert.Equal(t, "carol@example.com", ev.Parameters["from_owner"])
	assert.Equal(t, "Admin@Example.com", ev.Parameters["to_owner"])
	assert.Equal(t, "carols-report", ev.Parameters["script"])
}

// TestPortalTransferOwner_NamesTheNewOwnerWhenItIsNotTheAdmin proves the
// message states what the transfer means for the next run either way: the
// script runs with the authority captured now, whoever it went to.
func TestPortalTransferOwner_NamesTheNewOwnerWhenItIsNotTheAdmin(t *testing.T) {
	store := portalStore()

	rec := servePortalRequest(t, ownerDeps(store, admin, nil), http.MethodPut, ownerPath,
		`{"owner_email":"jane@example.com"}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body ownerResponse
	decodeInto(t, rec, &body)
	assert.Contains(t, body.Message, "jane@example.com")
	assert.Contains(t, body.Message, "access you hold")
}

// TestPortalTransferOwner_RefusedForEverybodyButAnAdministrator pins the
// authority: the owner of the script cannot give it away, and somebody with no
// claim on it is not told it exists.
func TestPortalTransferOwner_RefusedForEverybodyButAnAdministrator(t *testing.T) {
	cases := []struct {
		name string
		user *PortalIdentity
		want int
	}{
		{"its owner", carol, http.StatusForbidden},
		{"a stranger", stranger, http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := portalStore()

			rec := servePortalRequest(t, ownerDeps(store, tc.user, nil), http.MethodPut, ownerPath,
				`{"owner_email":"jane@example.com"}`)

			assert.Equal(t, tc.want, rec.Code)
			assert.Equal(t, "carol@example.com", store.scripts[1].OwnerEmail, "nothing moved")
		})
	}
}

// TestPortalTransferOwner_ConflictWhenTheNameIsTaken proves the receiving
// side's refusal reaches the caller as something they can act on rather than as
// a failure they cannot read.
func TestPortalTransferOwner_ConflictWhenTheNameIsTaken(t *testing.T) {
	for _, conflict := range []error{script.ErrNameTaken, script.ErrVersionConflict} {
		t.Run(conflict.Error(), func(t *testing.T) {
			store := portalStore()
			store.transferErr = conflict
			log := &recordingAudit{}

			rec := servePortalRequest(t, ownerDeps(store, admin, log), http.MethodPut, ownerPath,
				`{"owner_email":"jane@example.com"}`)

			assert.Equal(t, http.StatusConflict, rec.Code)
			require.Len(t, log.events, 1)
			assert.False(t, log.events[0].Success,
				"a refused attempt to move somebody else's automation is still an administrative act")
		})
	}
}

func TestPortalTransferOwner_Refusals(t *testing.T) {
	cases := []struct {
		name string
		path string
		body string
		want int
	}{
		{"no address", ownerPath, `{"owner_email":""}`, http.StatusBadRequest},
		{"not an address", ownerPath, `{"owner_email":"nope"}`, http.StatusBadRequest},
		{"the owner it already has", ownerPath, `{"owner_email":"carol@example.com"}`, http.StatusBadRequest},
		{"malformed body", ownerPath, `{`, http.StatusBadRequest},
		{"no such script", "/api/v1/portal/scripts/nope/owner", `{"owner_email":"jane@example.com"}`, http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := servePortalRequest(t, ownerDeps(portalStore(), admin, nil), http.MethodPut, tc.path, tc.body)
			assert.Equal(t, tc.want, rec.Code, rec.Body.String())
		})
	}
}

// blindAfterWrite answers the read that authorizes the transfer and fails the
// read-back after it, which is the only way to reach the arm where the write
// landed but the record cannot be read again.
type blindAfterWrite struct {
	*stubStore
	reads int
}

func (b *blindAfterWrite) GetByID(ctx context.Context, id string) (*script.Script, error) {
	b.reads++
	if b.reads > 1 {
		return nil, errors.New("down")
	}
	return b.stubStore.GetByID(ctx, id)
}

// TestPortalTransferOwner_ReportsTheAskedForAddressWhenTheReadBackFails keeps a
// write that landed from being reported as a failure.
func TestPortalTransferOwner_ReportsTheAskedForAddressWhenTheReadBackFails(t *testing.T) {
	store := &blindAfterWrite{stubStore: portalStore()}
	deps := ownerDeps(store.stubStore, admin, nil)
	deps.Scripts = store

	rec := servePortalRequest(t, deps, http.MethodPut, ownerPath, `{"owner_email":"jane@example.com"}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body ownerResponse
	decodeInto(t, rec, &body)
	assert.Equal(t, "jane@example.com", body.OwnerEmail)
	assert.Zero(t, body.Version, "an unread version is reported as unknown rather than guessed")
}

// TestPortalTransferOwner_ReadFailureIsNotFoundBeforeTheWrite covers the arm
// before it: a store that cannot answer at all refuses the transfer.
func TestPortalTransferOwner_ReadFailureIsNotFoundBeforeTheWrite(t *testing.T) {
	store := portalStore()
	store.getErr = errors.New("down")

	rec := servePortalRequest(t, ownerDeps(store, admin, nil), http.MethodPut, ownerPath,
		`{"owner_email":"jane@example.com"}`)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Equal(t, "carol@example.com", store.scripts[1].OwnerEmail, "nothing moved")
}

// TestPortalTransferOwner_AuditIsBestEffort proves a logging failure never
// fails a transfer that already happened.
func TestPortalTransferOwner_AuditIsBestEffort(t *testing.T) {
	store := portalStore()
	log := &recordingAudit{err: errors.New("audit down")}

	rec := servePortalRequest(t, ownerDeps(store, admin, log), http.MethodPut, ownerPath,
		`{"owner_email":"jane@example.com"}`)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "jane@example.com", store.scripts[1].OwnerEmail)
}
