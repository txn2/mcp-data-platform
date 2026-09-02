package scripthttp

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/producedview"
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

// What follows is #1588: a transfer moves the automation, and the files its
// runs wrote either go with it or are named as staying behind.

// outputsFixture is what carol's script has written: two assets and a
// collection it created, one of the assets already jane's; an asset it only
// modified; a created asset since deleted; and a resource. Only the first
// three are a transfer's concern.
func outputsFixture() *stubProduced {
	at := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	return &stubProduced{items: []producedview.Item{
		{
			TargetKind: "asset", TargetID: "asset-1", Name: "Daily sales", OwnerEmail: "carol@example.com",
			Created: true, FirstWriteAt: at, LastWriteAt: at, WriteCount: 12, LastVersion: 12,
		},
		{
			TargetKind: "asset", TargetID: "asset-2", Name: "Weekly sales", OwnerEmail: "Jane@Example.com",
			Created: true, FirstWriteAt: at, LastWriteAt: at, WriteCount: 2, LastVersion: 2,
		},
		{
			TargetKind: "collection", TargetID: "col-1", Name: "Sales pack", OwnerEmail: "carol@example.com",
			Created: true, FirstWriteAt: at, LastWriteAt: at, WriteCount: 1,
		},
		{
			TargetKind: "asset", TargetID: "asset-3", Name: "Somebody else's", OwnerEmail: "bob@example.com",
			Created: false, FirstWriteAt: at, LastWriteAt: at, WriteCount: 1, LastVersion: 4,
		},
		{
			TargetKind: "asset", TargetID: "asset-gone", Created: true, Deleted: true,
			FirstWriteAt: at, LastWriteAt: at, WriteCount: 3, LastVersion: 3,
		},
		{
			TargetKind: "resource", TargetID: "res-1", Name: "Region map",
			Created: true, FirstWriteAt: at, LastWriteAt: at, WriteCount: 1,
		},
	}}
}

// outputsDeps is the transfer route with the produced reader wired.
func outputsDeps(store *stubStore, produced ProducedReader, log *recordingAudit) Deps {
	deps := ownerDeps(store, admin, log)
	deps.Produced = produced
	return deps
}

// TestPortalTransferOwner_RefusesToMoveBlindOverOutputs is criterion 1 at its
// sharpest: a script that has created files is not moved until the caller says
// what happens to them, and the refusal names what there is to decide about.
func TestPortalTransferOwner_RefusesToMoveBlindOverOutputs(t *testing.T) {
	store := portalStore()
	log := &recordingAudit{}

	rec := servePortalRequest(t, outputsDeps(store, outputsFixture(), log), http.MethodPut, ownerPath,
		`{"owner_email":"jane@example.com"}`)

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "2 assets and 1 collection")
	assert.Contains(t, rec.Body.String(), `\"outputs\": \"move\"`)
	assert.Contains(t, rec.Body.String(), `\"outputs\": \"keep\"`)
	assert.Contains(t, rec.Body.String(), "their current owners",
		"one of the outputs is already somebody else's, so the refusal does not claim they all stay with carol")
	assert.Equal(t, "carol@example.com", store.scripts[1].OwnerEmail, "nothing moved")
	assert.Empty(t, log.events, "a refusal before the act is not an act")

	// When every output is the script owner's, the refusal says so by name.
	carols := outputsFixture()
	carols.items = carols.items[:1]
	rec = servePortalRequest(t, outputsDeps(portalStore(), carols, nil), http.MethodPut, ownerPath,
		`{"owner_email":"jane@example.com"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "1 asset. Say whether")
	assert.Contains(t, rec.Body.String(), "stay with carol@example.com")
}

// TestPortalTransferOwner_KeepsTheOutputsAndNamesWhatTheNewOwnerCannotReach is
// criterion 3: kept outputs are counted, and the ones whose row names somebody
// other than the new owner are listed, so a caller with no dialog in front of
// them learns exactly which files the new owner cannot open.
func TestPortalTransferOwner_KeepsTheOutputsAndNamesWhatTheNewOwnerCannotReach(t *testing.T) {
	store := portalStore()
	log := &recordingAudit{}

	rec := servePortalRequest(t, outputsDeps(store, outputsFixture(), log), http.MethodPut, ownerPath,
		`{"owner_email":"jane@example.com","outputs":"keep"}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body ownerResponse
	decodeInto(t, rec, &body)
	require.NotNil(t, body.Outputs)
	assert.Equal(t, "keep", body.Outputs.Disposition)
	assert.Equal(t, 2, body.Outputs.Assets)
	assert.Equal(t, 1, body.Outputs.Collections)
	require.Len(t, body.Outputs.Kept, 2, "the asset already jane's is not something jane cannot reach")
	assert.Equal(t, ownerOutput{TargetKind: "asset", TargetID: "asset-1", Name: "Daily sales", OwnerEmail: "carol@example.com"}, body.Outputs.Kept[0])
	assert.Equal(t, ownerOutput{TargetKind: "collection", TargetID: "col-1", Name: "Sales pack", OwnerEmail: "carol@example.com"}, body.Outputs.Kept[1])
	assert.Contains(t, body.Message, "carols-report now belongs to jane@example.com")
	assert.Contains(t, body.Message, "The 2 assets and 1 collection its runs wrote stay with carol@example.com.")
	assert.Contains(t, body.Message, "jane@example.com cannot open, share or delete them")
	assert.Equal(t, script.OutputsKeep, store.transferAsked.Outputs)

	require.Len(t, log.events, 1)
	assert.Equal(t, "keep", log.events[0].Parameters["outputs"])
	assert.NotContains(t, log.events[0].Parameters, "assets_moved")
}

// TestPortalTransferOwner_MovesTheOutputsWithTheScript is criterion 2 at this
// surface: the store is asked to move them, the counts it reports are what the
// response and the audit row carry, and nothing is listed as out of reach.
func TestPortalTransferOwner_MovesTheOutputsWithTheScript(t *testing.T) {
	store := portalStore()
	store.transferMoved = script.Transferred{AssetsMoved: 2, CollectionsMoved: 1}
	log := &recordingAudit{}

	rec := servePortalRequest(t, outputsDeps(store, outputsFixture(), log), http.MethodPut, ownerPath,
		`{"owner_email":"jane@example.com","outputs":"MOVE"}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body ownerResponse
	decodeInto(t, rec, &body)
	require.NotNil(t, body.Outputs)
	assert.Equal(t, ownerOutputs{Assets: 2, Collections: 1, Disposition: "move"}, *body.Outputs)
	assert.Contains(t, body.Message, "The 2 assets and 1 collection its runs wrote now belong to jane@example.com too.")
	assert.Equal(t, script.OutputsMove, store.transferAsked.Outputs, "the disposition is read case-insensitively")

	require.Len(t, log.events, 1)
	assert.Equal(t, "move", log.events[0].Parameters["outputs"])
	assert.Equal(t, 2, log.events[0].Parameters["assets_moved"])
	assert.Equal(t, 1, log.events[0].Parameters["collections_moved"])
}

// TestPortalTransferOwner_AScriptWithNoOutputsMovesAsItAlwaysHas is criterion
// 5. A script whose runs created nothing that still exists -- a file it only
// modified, one it created that is gone, a resource -- needs no disposition,
// accepts one anyway, and answers without an outputs account either way.
func TestPortalTransferOwner_AScriptWithNoOutputsMovesAsItAlwaysHas(t *testing.T) {
	nothing := outputsFixture()
	nothing.items = nothing.items[3:]
	for _, body := range []string{
		`{"owner_email":"jane@example.com"}`,
		`{"owner_email":"jane@example.com","outputs":"move"}`,
		`{"owner_email":"jane@example.com","outputs":"keep"}`,
	} {
		t.Run(body, func(t *testing.T) {
			store := portalStore()
			rec := servePortalRequest(t, outputsDeps(store, nothing, nil), http.MethodPut, ownerPath, body)

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.NotContains(t, rec.Body.String(), `"outputs"`)
			var resp ownerResponse
			decodeInto(t, rec, &resp)
			assert.Equal(t, "carols-report now belongs to jane@example.com and runs with the access you hold, captured now.", resp.Message)
			assert.Equal(t, "jane@example.com", store.scripts[1].OwnerEmail)
		})
	}
}

// TestPortalTransferOwner_KeptOutputsAlreadyTheNewOwners covers a script moved
// back to the person its outputs already name: kept, nothing out of reach, and
// the message says so rather than warning about files jane can open.
func TestPortalTransferOwner_KeptOutputsAlreadyTheNewOwners(t *testing.T) {
	theirs := outputsFixture()
	theirs.items = theirs.items[1:2]

	rec := servePortalRequest(t, outputsDeps(portalStore(), theirs, nil), http.MethodPut, ownerPath,
		`{"owner_email":"jane@example.com","outputs":"keep"}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body ownerResponse
	decodeInto(t, rec, &body)
	require.NotNil(t, body.Outputs)
	assert.Empty(t, body.Outputs.Kept)
	assert.Contains(t, body.Message, "The 1 asset its runs wrote already belong to jane@example.com.")
}

// TestPortalTransferOwner_OutputRefusals: a disposition that is neither of the
// two, and outputs that cannot be read, both stop the move before it happens.
func TestPortalTransferOwner_OutputRefusals(t *testing.T) {
	cases := []struct {
		name     string
		produced *stubProduced
		body     string
		want     int
	}{
		{"unknown disposition", outputsFixture(), `{"owner_email":"jane@example.com","outputs":"burn"}`, http.StatusBadRequest},
		{"outputs unreadable", &stubProduced{err: errors.New("down")}, `{"owner_email":"jane@example.com","outputs":"move"}`, http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := portalStore()
			rec := servePortalRequest(t, outputsDeps(store, tc.produced, nil), http.MethodPut, ownerPath, tc.body)

			assert.Equal(t, tc.want, rec.Code, rec.Body.String())
			assert.Equal(t, "carol@example.com", store.scripts[1].OwnerEmail, "nothing moved")
		})
	}
}

// TestPortalTransferOwner_WithoutAProducerRecordMovesAsBefore pins the
// deployment that records no producers: there is nothing to ask about, so the
// route behaves exactly as it did, whatever the body says about outputs.
func TestPortalTransferOwner_WithoutAProducerRecordMovesAsBefore(t *testing.T) {
	store := portalStore()
	rec := servePortalRequest(t, ownerDeps(store, admin, nil), http.MethodPut, ownerPath,
		`{"owner_email":"jane@example.com","outputs":"keep"}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), `"outputs"`)
	assert.Equal(t, script.OutputsKeep, store.transferAsked.Outputs, "the stated disposition still reaches the store")
}

// TestOutputsSentence pins the prose for each shape the account takes,
// including the ones the route tests above do not reach: kept outputs spread
// over several owners, and an output whose row names nobody.
func TestOutputsSentence(t *testing.T) {
	cases := []struct {
		name string
		acct *ownerOutputs
		want string
	}{
		{"no account", nil, ""},
		{
			"moved", &ownerOutputs{Assets: 1, Disposition: "move"},
			" The 1 asset its runs wrote now belong to jane@example.com too.",
		},
		{
			"kept, one owner", &ownerOutputs{
				Collections: 3, Disposition: "keep",
				Kept: []ownerOutput{{OwnerEmail: "carol@example.com"}, {OwnerEmail: "Carol@Example.com"}},
			},
			" The 3 collections its runs wrote stay with carol@example.com. jane@example.com cannot open, share or delete them, and each run goes on writing a new version into them.",
		},
		{
			"kept, several owners", &ownerOutputs{
				Assets: 2, Disposition: "keep",
				Kept: []ownerOutput{{OwnerEmail: "carol@example.com"}, {OwnerEmail: "bob@example.com"}},
			},
			" The 2 assets its runs wrote stay with their current owners. jane@example.com cannot open, share or delete them, and each run goes on writing a new version into them.",
		},
		{
			"kept, unattributed", &ownerOutputs{Assets: 1, Disposition: "keep", Kept: []ownerOutput{{}}},
			" The 1 asset its runs wrote stay with nobody. jane@example.com cannot open, share or delete them, and each run goes on writing a new version into them.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, outputsSentence(tc.acct, "jane@example.com"))
		})
	}
}
