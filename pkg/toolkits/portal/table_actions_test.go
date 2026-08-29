package portal

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// manage_table is keyed by the reference a caller already holds, so what is
// asserted here is the tool's half of that contract: the reference reaches the
// registrar untouched, an anonymous call is refused before anything is
// resolved, a deployment with nowhere to register says so, and the registrar's
// own refusals are passed through as written. Who may register which file is
// the registrar seam's rule and is asserted there.

// assetReference and resourceReference are the two forms one action serves.
const (
	assetReference    = "mcp:asset:asset_1"
	resourceReference = "mcp:resource:res_1"
)

// fakeTableRegistrar records what the tool asked for and returns what a real
// registrar would.
type fakeTableRegistrar struct {
	registered []TableRegistration
	dropped    []string
	droppedAll []string
	lastRef    string
	lastConn   string
	lastName   string
	lastRepair bool
	lastFollow bool
	repaired   string
	// followed is what the fake answers a content write with, and followedFor
	// records which files it was asked about, as kind:id:version.
	followed      []string
	followedFor   []string
	registerErr   error
	unregisterErr error
	listErr       error
}

func (f *fakeTableRegistrar) Register(
	_ context.Context, reference, connection, tableName string, opts RegisterOptions,
) (*TableRegistration, error) {
	f.lastRef, f.lastConn, f.lastName, f.lastRepair, f.lastFollow = reference, connection, tableName, opts.Repair, opts.Follow
	if f.registerErr != nil {
		return nil, f.registerErr
	}
	reg := TableRegistration{
		RegistrationID: "reg_1",
		Connection:     connection,
		QueryTable:     "scratch.uploads.analyst_vendor_keys",
		Columns:        []string{"store_id"},
		SampleSQL:      "SELECT CAST(store_id AS BIGINT) FROM scratch.uploads.analyst_vendor_keys",
		RegisteredBy:   ownerEmail,
		Repaired:       f.repaired,
		Follow:         opts.Follow,
	}
	f.registered = append(f.registered, reg)
	return &reg, nil
}

func (f *fakeTableRegistrar) Unregister(_ context.Context, registrationID string) error {
	if f.unregisterErr != nil {
		return f.unregisterErr
	}
	f.dropped = append(f.dropped, registrationID)
	return nil
}

func (f *fakeTableRegistrar) Tables(_ context.Context, reference string) ([]TableRegistration, error) {
	f.lastRef = reference
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.registered, nil
}

func (f *fakeTableRegistrar) DropAssetTables(_ context.Context, assetID string) {
	f.droppedAll = append(f.droppedAll, assetID)
}

func (f *fakeTableRegistrar) FollowAssetTables(_ context.Context, assetID string, version int) []string {
	f.followedFor = append(f.followedFor, fmt.Sprintf("asset:%s:%d", assetID, version))
	return f.followed
}

func (f *fakeTableRegistrar) FollowResourceTables(_ context.Context, resourceID string, version int) []string {
	f.followedFor = append(f.followedFor, fmt.Sprintf("resource:%s:%d", resourceID, version))
	return f.followed
}

// tableToolkit builds a toolkit owning one asset, with a registrar bound.
func tableToolkit(t *testing.T, reg TableRegistrar) *Toolkit {
	t.Helper()
	assets := newInMemoryAssetStore()
	require.NoError(t, assets.Insert(context.Background(), portal.Asset{
		ID: shareAssetID, OwnerID: ownerID, OwnerEmail: ownerEmail,
		Name: "Vendor keys", ContentType: "text/csv",
		S3Bucket: "b", S3Key: "artifacts/u1/asset_1/content.csv",
	}))
	tk := New(Config{Name: "test", S3Bucket: "b", AssetStore: assets})
	if reg != nil {
		tk.SetTableRegistrar(reg)
	}
	return tk
}

// callTable drives one manage_table call.
func callTable(ctx context.Context, t *testing.T, tk *Toolkit, input manageTableInput) *mcp.CallToolResult {
	t.Helper()
	res, _, err := tk.handleManageTable(ctx, nil, input)
	require.NoError(t, err)
	require.NotNil(t, res)
	return res
}

func TestRegisterTable_ReportsTheTableAndTheCast(t *testing.T) {
	reg := &fakeTableRegistrar{}
	tk := tableToolkit(t, reg)

	res := callTable(ownerCtx(), t, tk, manageTableInput{
		Action: tableActionRegister, Reference: assetReference,
		Connection: "scratch", TableName: "vendor_keys",
	})
	require.False(t, res.IsError, resultText(t, res))

	body := decodeResult(t, res)
	assert.Equal(t, "scratch.uploads.analyst_vendor_keys", body["query_table"])
	assert.Equal(t, "scratch", body["connection"])
	assert.Equal(t, assetReference, body["reference"])
	assert.Contains(t, body["message"], "CAST",
		"every column is VARCHAR, so the reader is told what a join needs")

	assert.Equal(t, assetReference, reg.lastRef)
	assert.Equal(t, "scratch", reg.lastConn)
	assert.Equal(t, "vendor_keys", reg.lastName)
}

// TestRegisterTable_ServesEitherKindThroughOneAction is the point of the tool:
// a resource reference takes the same action with no argument naming its kind,
// which is what lets an agent register a file somebody uploaded without
// leaving the conversation.
func TestRegisterTable_ServesEitherKindThroughOneAction(t *testing.T) {
	reg := &fakeTableRegistrar{}
	tk := tableToolkit(t, reg)

	for _, reference := range []string{assetReference, resourceReference} {
		res := callTable(ownerCtx(), t, tk, manageTableInput{
			Action: tableActionRegister, Reference: reference, Connection: "scratch",
		})
		require.False(t, res.IsError, resultText(t, res))
		assert.Equal(t, reference, reg.lastRef,
			"the reference reaches the registrar as written; the tool does not parse it")
	}
}

// TestRegisterTable_ConnectionIsRequired: the table has to go somewhere, and
// the refusal points at how to find out where.
func TestRegisterTable_ConnectionIsRequired(t *testing.T) {
	tk := tableToolkit(t, &fakeTableRegistrar{})

	res := callTable(ownerCtx(), t, tk, manageTableInput{
		Action: tableActionRegister, Reference: assetReference,
	})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "connection is required")
	assert.Contains(t, resultText(t, res), "list_connections")
}

// TestTableActions_ReferenceIsRequired: an action naming no file says where a
// reference comes from rather than restating the schema.
func TestTableActions_ReferenceIsRequired(t *testing.T) {
	tk := tableToolkit(t, &fakeTableRegistrar{})

	for _, action := range []string{tableActionRegister, tableActionList} {
		res := callTable(ownerCtx(), t, tk, manageTableInput{Action: action, Connection: "scratch"})
		assert.True(t, res.IsError, action)
		assert.Contains(t, resultText(t, res), "reference is required", action)
		assert.Contains(t, resultText(t, res), "mcp:resource:", action)
	}
}

// TestTableActions_RefuseAnAnonymousCall: a registration records who made it
// and decides replacement on that, so there has to be somebody to record.
func TestTableActions_RefuseAnAnonymousCall(t *testing.T) {
	reg := &fakeTableRegistrar{}
	tk := tableToolkit(t, reg)

	res := callTable(context.Background(), t, tk, manageTableInput{
		Action: tableActionRegister, Reference: assetReference, Connection: "scratch",
	})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "signed-in identity")
	assert.Empty(t, reg.registered, "nothing reached the registrar")
}

// TestTableActions_WithoutARegistrarNameTheMissingPiece rather than reporting
// a generic failure the reader can do nothing about.
func TestTableActions_WithoutARegistrar(t *testing.T) {
	tk := tableToolkit(t, nil)

	for _, action := range []string{tableActionRegister, tableActionUnregister, tableActionList} {
		res := callTable(ownerCtx(), t, tk, manageTableInput{
			Action: action, Reference: assetReference, Connection: "scratch", RegistrationID: "reg_1",
		})
		assert.True(t, res.IsError, action)
		assert.Contains(t, resultText(t, res), "scratch catalog and schema", action)
	}
}

// TestManageTable_InvalidActionNamesTheThree so an agent that guesses learns
// what the tool takes.
func TestManageTable_InvalidAction(t *testing.T) {
	tk := tableToolkit(t, &fakeTableRegistrar{})

	res := callTable(ownerCtx(), t, tk, manageTableInput{Action: "register_table"})
	require.True(t, res.IsError)
	for _, action := range []string{"register", "list", "unregister"} {
		assert.Contains(t, resultText(t, res), action)
	}
}

func TestUnregisterTable(t *testing.T) {
	reg := &fakeTableRegistrar{}
	tk := tableToolkit(t, reg)

	res := callTable(ownerCtx(), t, tk, manageTableInput{
		Action: tableActionUnregister, RegistrationID: "reg_1",
	})
	require.False(t, res.IsError, resultText(t, res))
	assert.Equal(t, []string{"reg_1"}, reg.dropped)
	assert.Contains(t, decodeResult(t, res)["message"], "file itself is unchanged")
}

// TestUnregisterTable_CarriesTheRegistrarsRefusal: dropping somebody else's
// table is refused by the registrar, and the tool says what it said.
func TestUnregisterTable_CarriesTheRegistrarsRefusal(t *testing.T) {
	reg := &fakeTableRegistrar{unregisterErr: errors.New("that table was registered by bob@example.com")}
	tk := tableToolkit(t, reg)

	res := callTable(ownerCtx(), t, tk, manageTableInput{
		Action: tableActionUnregister, RegistrationID: "reg_1",
	})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "bob@example.com")
}

func TestUnregisterTable_RegistrationIDIsRequired(t *testing.T) {
	tk := tableToolkit(t, &fakeTableRegistrar{})

	res := callTable(ownerCtx(), t, tk, manageTableInput{Action: tableActionUnregister})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "registration_id is required")
	assert.Contains(t, resultText(t, res), "action=list")
}

func TestListTables(t *testing.T) {
	reg := &fakeTableRegistrar{}
	tk := tableToolkit(t, reg)

	// Nothing registered yet is an empty list, not a missing field.
	res := callTable(ownerCtx(), t, tk, manageTableInput{
		Action: tableActionList, Reference: assetReference,
	})
	require.False(t, res.IsError, resultText(t, res))
	body := decodeResult(t, res)
	assert.Equal(t, float64(0), body["total"])
	assert.NotNil(t, body["registrations"])

	require.False(t, callTable(ownerCtx(), t, tk, manageTableInput{
		Action: tableActionRegister, Reference: assetReference, Connection: "scratch",
	}).IsError)

	res = callTable(ownerCtx(), t, tk, manageTableInput{
		Action: tableActionList, Reference: assetReference,
	})
	require.False(t, res.IsError, resultText(t, res))
	assert.Equal(t, float64(1), decodeResult(t, res)["total"])
}

// TestTableActions_CarryTheRegistrarsRefusal: the platform's refusals name
// what to do next, so the tool passes them through as written.
func TestTableActions_CarryTheRegistrarsRefusal(t *testing.T) {
	reg := &fakeTableRegistrar{
		registerErr: errors.New(
			"a table reads every file in this file's directory, and notes.txt sits beside it"),
	}
	tk := tableToolkit(t, reg)

	res := callTable(ownerCtx(), t, tk, manageTableInput{
		Action: tableActionRegister, Reference: assetReference, Connection: "scratch",
	})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "notes.txt")
}

func TestListTables_StoreFailureIsReported(t *testing.T) {
	tk := tableToolkit(t, &fakeTableRegistrar{listErr: errors.New("connection refused")})

	res := callTable(ownerCtx(), t, tk, manageTableInput{
		Action: tableActionList, Reference: assetReference,
	})
	assert.True(t, res.IsError)
}

// TestDelete_DropsTheAssetsTables. An asset delete is soft, so the object
// survives it; a table still pointing at that object would keep serving its
// rows out of a schema the owner can no longer see.
func TestDelete_DropsTheAssetsTables(t *testing.T) {
	reg := &fakeTableRegistrar{}
	tk := tableToolkit(t, reg)

	res := callManage(ownerCtx(), t, tk, manageAssetInput{
		Action: actionDelete, AssetID: shareAssetID,
	})
	require.False(t, res.IsError, resultText(t, res))
	assert.Equal(t, []string{shareAssetID}, reg.droppedAll)
}

// TestDelete_WithoutARegistrarStillDeletes, so a deployment that cannot
// register tables is not one that cannot delete assets.
func TestDelete_WithoutARegistrarStillDeletes(t *testing.T) {
	tk := tableToolkit(t, nil)

	res := callManage(ownerCtx(), t, tk, manageAssetInput{
		Action: actionDelete, AssetID: shareAssetID,
	})
	assert.False(t, res.IsError, resultText(t, res))
}

// TestManageAsset_NoLongerCarriesTheTableActions is the hard cut (#1428): the
// actions moved to manage_table and no alias for them stayed behind, so an
// agent holding the old call learns the action does not exist rather than
// getting a silent no-op.
func TestManageAsset_NoLongerCarriesTheTableActions(t *testing.T) {
	tk := tableToolkit(t, &fakeTableRegistrar{})

	for _, action := range []string{"register_table", "unregister_table", "list_tables"} {
		res := callManage(ownerCtx(), t, tk, manageAssetInput{Action: action, AssetID: shareAssetID})
		require.True(t, res.IsError, action)
		assert.Contains(t, resultText(t, res), "invalid action", action)
	}
}

// TestTableActionsOverMCPSession drives the three actions over a real client
// session, so the arguments asserted here are the ones the schema actually
// advertises. The schema forbids properties it does not declare, so a field
// the handler reads but the schema omits never reaches the handler at all --
// which is what makes this a check of the schema and not just of the dispatch.
func TestTableActionsOverMCPSession(t *testing.T) {
	ctx := ownerCtx()
	reg := &fakeTableRegistrar{}
	tk := tableToolkit(t, reg)

	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
	tk.RegisterTools(server)
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSess, err := server.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	defer func() { _ = serverSess.Close() }()
	clientSess, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0"}, nil).
		Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	defer func() { _ = clientSess.Close() }()

	call := func(args map[string]any) map[string]any {
		t.Helper()
		res, callErr := clientSess.CallTool(ctx, &mcp.CallToolParams{Name: ManageTableToolName, Arguments: args})
		require.NoError(t, callErr)
		require.False(t, res.IsError, resultText(t, res))
		return decodeResult(t, res)
	}

	// The headline case: a file somebody uploaded, named by the reference a
	// search hit carries, registered without a portal step.
	created := call(map[string]any{
		"action": tableActionRegister, "reference": resourceReference,
		"connection": "scratch", "table_name": "vendor_keys",
	})
	assert.Equal(t, "scratch.uploads.analyst_vendor_keys", created["query_table"])
	registrationID, ok := created["registration_id"].(string)
	require.True(t, ok)

	listed := call(map[string]any{"action": tableActionList, "reference": resourceReference})
	assert.Equal(t, float64(1), listed[fieldTotal])

	call(map[string]any{"action": tableActionUnregister, "registration_id": registrationID})
	assert.Equal(t, []string{registrationID}, reg.dropped)

	// An argument the schema does not declare is refused before the handler,
	// as is the asset_id the action used to take.
	for _, bad := range []string{"catalog", "asset_id"} {
		refused, refusedErr := clientSess.CallTool(ctx, &mcp.CallToolParams{
			Name: ManageTableToolName,
			Arguments: map[string]any{
				"action": tableActionRegister, "reference": assetReference, bad: "scratch",
			},
		})
		require.NoError(t, refusedErr)
		require.True(t, refused.IsError, bad)
		assert.Contains(t, resultText(t, refused), "additional properties", bad)
	}

	// The action enum is advertised, so a call carrying the old action name is
	// refused by the schema rather than reaching the dispatch.
	refused, err := clientSess.CallTool(ctx, &mcp.CallToolParams{
		Name:      ManageTableToolName,
		Arguments: map[string]any{"action": "register_table", "reference": assetReference},
	})
	require.NoError(t, err)
	assert.True(t, refused.IsError)
}

// TestRegisterTable_CarriesTheRepairAskAndReportsWhatChanged: a CSV whose cells
// carry line breaks cannot be read as a table the way it is stored, and the
// tool's part in the answer is to carry the ask through and to put what changed
// in front of the registration it produced -- the file itself has a new
// version, which is the more consequential half (#1441).
func TestRegisterTable_CarriesTheRepairAskAndReportsWhatChanged(t *testing.T) {
	reg := &fakeTableRegistrar{repaired: "Saved version 2 of this file, which put 2 rows back onto one line."}
	tk := tableToolkit(t, reg)

	res := callTable(ownerCtx(), t, tk, manageTableInput{
		Action: tableActionRegister, Reference: assetReference,
		Connection: "scratch", Repair: true,
	})
	require.False(t, res.IsError, resultText(t, res))
	assert.True(t, reg.lastRepair, "the ask reaches the registrar")

	body := decodeResult(t, res)
	assert.Equal(t, reg.repaired, body["repaired"])
	assert.Contains(t, body["message"], "Saved version 2 of this file")
	assert.Contains(t, body["message"], "CAST", "and the registration is still described")
}

// TestRegisterTable_WithoutTheRepairAsk is the default: the platform does not
// rewrite somebody's file on the way to something else they asked for.
func TestRegisterTable_WithoutTheRepairAsk(t *testing.T) {
	reg := &fakeTableRegistrar{}
	tk := tableToolkit(t, reg)

	res := callTable(ownerCtx(), t, tk, manageTableInput{
		Action: tableActionRegister, Reference: assetReference, Connection: "scratch",
	})
	require.False(t, res.IsError, resultText(t, res))
	assert.False(t, reg.lastRepair)
	assert.NotContains(t, decodeResult(t, res), "repaired")
}
