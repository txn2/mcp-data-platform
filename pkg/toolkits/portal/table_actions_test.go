package portal

import (
	"context"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// The table actions are owner authority, like sharing: registering puts an
// asset's contents in a schema everyone granted the connection can read. What
// is asserted here is who may call them, what they report, and that a
// deployment with nowhere to register says so instead of failing.

// fakeTableRegistrar records what the tool asked for and returns what a real
// registrar would.
type fakeTableRegistrar struct {
	registered  []TableRegistration
	dropped     []string
	droppedAll  []string
	lastConn    string
	lastName    string
	registerErr error
	listErr     error
}

func (f *fakeTableRegistrar) RegisterAsset(
	_ context.Context, asset portal.Asset, connection, tableName string,
) (*TableRegistration, error) {
	f.lastConn, f.lastName = connection, tableName
	if f.registerErr != nil {
		return nil, f.registerErr
	}
	reg := TableRegistration{
		RegistrationID: "reg_1",
		Connection:     connection,
		QueryTable:     "scratch.uploads.analyst_" + asset.ID,
		Columns:        []string{"store_id"},
		SampleSQL:      "SELECT * FROM scratch.uploads.analyst_" + asset.ID,
		RegisteredBy:   ownerEmail,
	}
	f.registered = append(f.registered, reg)
	return &reg, nil
}

func (f *fakeTableRegistrar) UnregisterAsset(_ context.Context, registrationID string) error {
	f.dropped = append(f.dropped, registrationID)
	return nil
}

func (f *fakeTableRegistrar) AssetTables(_ context.Context, _ portal.Asset) ([]TableRegistration, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.registered, nil
}

func (f *fakeTableRegistrar) DropAssetTables(_ context.Context, assetID string) {
	f.droppedAll = append(f.droppedAll, assetID)
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

func TestRegisterTable_ReportsTheTableAndTheCast(t *testing.T) {
	reg := &fakeTableRegistrar{}
	tk := tableToolkit(t, reg)

	res := callManage(ownerCtx(), t, tk, manageAssetInput{
		Action: actionRegisterTable, AssetID: shareAssetID,
		Connection: "scratch", TableName: "vendor_keys",
	})
	require.False(t, res.IsError, resultText(t, res))

	body := decodeResult(t, res)
	assert.Equal(t, "scratch.uploads.analyst_asset_1", body["query_table"])
	assert.Equal(t, "scratch", body["connection"])
	assert.Contains(t, body["message"], "CAST",
		"every column is VARCHAR, so the reader is told what a join needs")

	assert.Equal(t, "scratch", reg.lastConn)
	assert.Equal(t, "vendor_keys", reg.lastName)
}

// TestRegisterTable_ConnectionIsRequired: the table has to go somewhere, and
// the refusal points at how to find out where.
func TestRegisterTable_ConnectionIsRequired(t *testing.T) {
	tk := tableToolkit(t, &fakeTableRegistrar{})

	res := callManage(ownerCtx(), t, tk, manageAssetInput{
		Action: actionRegisterTable, AssetID: shareAssetID,
	})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "connection is required")
	assert.Contains(t, resultText(t, res), "list_connections")
}

// TestTableActions_RefuseNonOwner: registering publishes the file's contents,
// so it is the owner's call, and an editor share does not carry it.
func TestTableActions_RefuseNonOwner(t *testing.T) {
	tk := tableToolkit(t, &fakeTableRegistrar{})

	for _, action := range []string{actionRegisterTable, actionUnregisterTable, actionListTables} {
		res := callManage(strangerCtx(), t, tk, manageAssetInput{
			Action: action, AssetID: shareAssetID, Connection: "scratch", RegistrationID: "reg_1",
		})
		assert.True(t, res.IsError, action)
		assert.Contains(t, resultText(t, res), "only the owner can", action)
	}
}

// TestTableActions_WithoutARegistrarNameTheMissingPiece rather than reporting
// a generic failure the reader can do nothing about.
func TestTableActions_WithoutARegistrar(t *testing.T) {
	tk := tableToolkit(t, nil)

	for _, action := range []string{actionRegisterTable, actionUnregisterTable, actionListTables} {
		res := callManage(ownerCtx(), t, tk, manageAssetInput{
			Action: action, AssetID: shareAssetID, Connection: "scratch", RegistrationID: "reg_1",
		})
		assert.True(t, res.IsError, action)
		assert.Contains(t, resultText(t, res), "scratch catalog and schema", action)
	}
}

func TestUnregisterTable(t *testing.T) {
	reg := &fakeTableRegistrar{}
	tk := tableToolkit(t, reg)

	res := callManage(ownerCtx(), t, tk, manageAssetInput{
		Action: actionUnregisterTable, AssetID: shareAssetID, RegistrationID: "reg_1",
	})
	require.False(t, res.IsError, resultText(t, res))
	assert.Equal(t, []string{"reg_1"}, reg.dropped)
	assert.Contains(t, decodeResult(t, res)["message"], "file is unchanged")
}

func TestUnregisterTable_RegistrationIDIsRequired(t *testing.T) {
	tk := tableToolkit(t, &fakeTableRegistrar{})

	res := callManage(ownerCtx(), t, tk, manageAssetInput{
		Action: actionUnregisterTable, AssetID: shareAssetID,
	})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "registration_id is required")
	assert.Contains(t, resultText(t, res), "list_tables")
}

func TestListTables(t *testing.T) {
	reg := &fakeTableRegistrar{}
	tk := tableToolkit(t, reg)

	// Nothing registered yet is an empty list, not a missing field.
	res := callManage(ownerCtx(), t, tk, manageAssetInput{
		Action: actionListTables, AssetID: shareAssetID,
	})
	require.False(t, res.IsError, resultText(t, res))
	body := decodeResult(t, res)
	assert.Equal(t, float64(0), body["total"])
	assert.NotNil(t, body["registrations"])

	require.False(t, callManage(ownerCtx(), t, tk, manageAssetInput{
		Action: actionRegisterTable, AssetID: shareAssetID, Connection: "scratch",
	}).IsError)

	res = callManage(ownerCtx(), t, tk, manageAssetInput{
		Action: actionListTables, AssetID: shareAssetID,
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

	res := callManage(ownerCtx(), t, tk, manageAssetInput{
		Action: actionRegisterTable, AssetID: shareAssetID, Connection: "scratch",
	})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "notes.txt")
}

func TestListTables_StoreFailureIsReported(t *testing.T) {
	tk := tableToolkit(t, &fakeTableRegistrar{listErr: errors.New("connection refused")})

	res := callManage(ownerCtx(), t, tk, manageAssetInput{
		Action: actionListTables, AssetID: shareAssetID,
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

// TestManageAsset_ListsTheTableActions so an agent that sends a bad action
// learns the table actions exist.
func TestManageAsset_InvalidActionNamesTheTableActions(t *testing.T) {
	tk := tableToolkit(t, &fakeTableRegistrar{})

	res := callManage(ownerCtx(), t, tk, manageAssetInput{Action: "sideways"})
	require.True(t, res.IsError)
	for _, action := range []string{"register_table", "unregister_table", "list_tables"} {
		assert.Contains(t, resultText(t, res), action)
	}
}

// TestTableActionsOverMCPSession drives the three table actions over a real
// client session, so the arguments asserted here are the ones the schema
// actually advertises. The schema forbids properties it does not declare, so
// a field the handler reads but the schema omits never reaches the handler at
// all -- which is what makes this a check of the schema and not just of the
// dispatch.
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
		res, callErr := clientSess.CallTool(ctx, &mcp.CallToolParams{Name: ManageToolName, Arguments: args})
		require.NoError(t, callErr)
		require.False(t, res.IsError, resultText(t, res))
		return decodeResult(t, res)
	}

	created := call(map[string]any{
		"action": actionRegisterTable, "asset_id": shareAssetID,
		"connection": "scratch", "table_name": "vendor_keys",
	})
	assert.Equal(t, "scratch.uploads.analyst_asset_1", created["query_table"])
	registrationID, ok := created["registration_id"].(string)
	require.True(t, ok)

	listed := call(map[string]any{"action": actionListTables, "asset_id": shareAssetID})
	assert.Equal(t, float64(1), listed[fieldTotal])

	call(map[string]any{
		"action": actionUnregisterTable, "asset_id": shareAssetID,
		"registration_id": registrationID,
	})
	assert.Equal(t, []string{registrationID}, reg.dropped)

	// An argument the schema does not declare is refused before the handler.
	refused, err := clientSess.CallTool(ctx, &mcp.CallToolParams{Name: ManageToolName, Arguments: map[string]any{
		"action": actionRegisterTable, "asset_id": shareAssetID, "catalog": "scratch",
	}})
	require.NoError(t, err)
	require.True(t, refused.IsError)
	assert.Contains(t, resultText(t, refused), "additional properties")
}
