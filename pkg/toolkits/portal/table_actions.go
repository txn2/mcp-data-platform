package portal

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// TableRegistration is one registration as the tool reports it.
type TableRegistration struct {
	RegistrationID string   `json:"registration_id"`
	Connection     string   `json:"connection"`
	QueryTable     string   `json:"query_table"`
	Columns        []string `json:"columns,omitempty"`
	SampleSQL      string   `json:"sample_sql,omitempty"`
	RegisteredBy   string   `json:"registered_by,omitempty"`
	// Stale means the asset has a newer version than the one the table points
	// at, so the rows are the version that was current when it was registered.
	Stale bool `json:"stale"`
}

// TableRegistrar makes an asset's stored CSV readable as a query-engine table
// (#1327). The registrar seam satisfies it; the capability is declared here so
// the toolkit does not depend on the registrar.
//
// The acting caller is not a parameter. Every method resolves identity from
// the context exactly as this toolkit does, so the tool cannot present an
// identity the rest of the platform would refuse, and the connection boundary
// a tool call meets is the one a registration meets.
type TableRegistrar interface {
	RegisterAsset(ctx context.Context, asset portal.Asset, connection, tableName string) (*TableRegistration, error)
	UnregisterAsset(ctx context.Context, registrationID string) error
	AssetTables(ctx context.Context, asset portal.Asset) ([]TableRegistration, error)
	// DropAssetTables removes every table registered over an asset. It is what
	// a delete calls: the asset is going, and a table over where its file used
	// to be would answer queries from a schema its owner can no longer see.
	// Best-effort by contract -- a delete must not fail because a scratch table
	// could not be dropped.
	DropAssetTables(ctx context.Context, assetID string)
}

// SetTableRegistrar binds the registrar behind register_table and
// unregister_table. Called by the composition root once the Trino toolkit and
// the registration store exist; without it those two actions report that the
// deployment cannot register tables, which is what a deployment with no Trino
// scratch connection can do.
func (t *Toolkit) SetTableRegistrar(reg TableRegistrar) {
	t.tables = reg
}

// tableRegistrationOutput is the result of register_table.
type tableRegistrationOutput struct {
	AssetID string `json:"asset_id"`
	TableRegistration
	Message string `json:"message"`
}

// tableListOutput is the result of list_tables.
type tableListOutput struct {
	AssetID       string              `json:"asset_id"`
	Registrations []TableRegistration `json:"registrations"`
	Total         int                 `json:"total"`
}

// handleRegisterTable makes the asset's stored CSV queryable.
//
// Nothing is copied: the table points at the directory the asset's current
// content already sits in, so a new version of the asset means re-registering
// rather than re-loading. Registration is owner authority, like sharing: it
// puts the file's contents in a schema everyone granted the connection can
// read.
func (t *Toolkit) handleRegisterTable(ctx context.Context, input manageAssetInput) (*mcp.CallToolResult, any, error) {
	asset, denial := t.loadOwnedAsset(ctx, input.AssetID, actionRegisterTable, "register this asset as a table")
	if denial != nil {
		return denial, nil, nil
	}
	if t.tables == nil {
		return toolkit.ErrorResult(tableRegistrationUnavailable), nil, nil
	}
	if strings.TrimSpace(input.Connection) == "" {
		return toolkit.ErrorResult(
			"connection is required for register_table: name the Trino connection whose scratch schema the table goes in. " +
				"Call list_connections to see the connections you can reach."), nil, nil
	}

	reg, err := t.tables.RegisterAsset(ctx, *asset, input.Connection, input.TableName)
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}

	return toolkit.JSONResultTyped(tableRegistrationOutput{
		AssetID:           asset.ID,
		TableRegistration: *reg,
		Message: "Registered as " + reg.QueryTable + " on connection " + reg.Connection +
			". Every column is VARCHAR, so a join to a typed column needs a CAST.",
	})
}

// handleUnregisterTable drops a registered table.
//
// The asset's file is untouched: dropping an external table removes the
// catalog entry and nothing else.
func (t *Toolkit) handleUnregisterTable(ctx context.Context, input manageAssetInput) (*mcp.CallToolResult, any, error) {
	asset, denial := t.loadOwnedAsset(ctx, input.AssetID, actionUnregisterTable, "unregister this asset's table")
	if denial != nil {
		return denial, nil, nil
	}
	if t.tables == nil {
		return toolkit.ErrorResult(tableRegistrationUnavailable), nil, nil
	}
	if strings.TrimSpace(input.RegistrationID) == "" {
		return toolkit.ErrorResult(
			"registration_id is required for unregister_table: call manage_asset action=list_tables to see them."), nil, nil
	}

	if err := t.tables.UnregisterAsset(ctx, input.RegistrationID); err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	return toolkit.JSONResultTyped(map[string]any{
		fieldAssetID: asset.ID,
		fieldMessage: "The table was dropped. The asset's file is unchanged.",
	})
}

// handleListTables reports the tables registered over an asset.
func (t *Toolkit) handleListTables(ctx context.Context, input manageAssetInput) (*mcp.CallToolResult, any, error) {
	asset, denial := t.loadOwnedAsset(ctx, input.AssetID, actionListTables, "see this asset's tables")
	if denial != nil {
		return denial, nil, nil
	}
	if t.tables == nil {
		return toolkit.ErrorResult(tableRegistrationUnavailable), nil, nil
	}

	regs, err := t.tables.AssetTables(ctx, *asset)
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	if regs == nil {
		regs = []TableRegistration{}
	}
	return toolkit.JSONResultTyped(tableListOutput{
		AssetID:       asset.ID,
		Registrations: regs,
		Total:         len(regs),
	})
}

// dropTablesFor removes the tables registered over an asset being deleted.
// A deployment with no registrar has nothing to drop.
func (t *Toolkit) dropTablesFor(ctx context.Context, assetID string) {
	if t.tables == nil {
		return
	}
	t.tables.DropAssetTables(ctx, assetID)
}

// tableRegistrationUnavailable is what every table action says on a deployment
// with nothing to register onto. It names the missing piece rather than
// reporting a generic failure, because the reader can do something about it.
const tableRegistrationUnavailable = "This deployment cannot register tables: it needs a Trino connection with a " +
	"scratch catalog and schema configured. Ask an administrator to configure one."
