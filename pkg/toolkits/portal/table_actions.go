package portal

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

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
	// Stale means the file has a newer revision or version than the one the
	// table points at, so the rows are the content that was current when it
	// was registered.
	Stale bool `json:"stale"`
	// Repaired says what a correction of the file changed before it could be
	// registered, and is empty when none was needed (#1441). The file itself
	// changed, so the person who asked for the registration is told so.
	Repaired string `json:"repaired,omitempty"`
}

// TableRegistrar makes a stored CSV readable as a query-engine table (#1327),
// whichever kind of stored file it is (#1428). The registrar seam satisfies
// it; the capability is declared here so the toolkit does not depend on the
// registrar, and does not learn what a managed resource is.
//
// Every method is keyed by the canonical reference a caller already holds --
// the string `search` emits and `fetch` dereferences -- rather than by the id
// of one kind of record. That is what lets one tool serve every kind: the kind
// travels inside the reference, so there is no per-kind argument and no second
// tool.
//
// The acting caller is not a parameter. Every method resolves identity from
// the context exactly as this toolkit does, so the tool cannot present an
// identity the rest of the platform would refuse, and the connection boundary
// a tool call meets is the one a registration meets.
type TableRegistrar interface {
	// Register makes the referenced file queryable. repair asks for a
	// corrected version of the file to be saved and registered when the file
	// cannot be read as a table the way it is stored; without it such a file
	// is refused and the refusal says what is wrong with it.
	Register(ctx context.Context, reference, connection, tableName string, repair bool) (*TableRegistration, error)
	Unregister(ctx context.Context, registrationID string) error
	Tables(ctx context.Context, reference string) ([]TableRegistration, error)
	// DropAssetTables removes every table registered over an asset. It is what
	// a delete calls: the asset is going, and a table over where its file used
	// to be would answer queries from a schema its owner can no longer see.
	// Best-effort by contract -- a delete must not fail because a scratch table
	// could not be dropped.
	DropAssetTables(ctx context.Context, assetID string)
}

// SetTableRegistrar binds the registrar behind manage_table. Called by the
// composition root once the Trino toolkit and the registration store exist;
// without it the tool reports that the deployment cannot register tables,
// which is what a deployment with no Trino scratch connection can do.
func (t *Toolkit) SetTableRegistrar(reg TableRegistrar) {
	t.tables = reg
}

// manageTableInput defines the input for manage_table.
type manageTableInput struct {
	Action string `json:"action"`
	// Reference names the stored file, in the same vocabulary every other tool
	// uses: mcp:resource:<id> for uploaded reference material, mcp:asset:<id>
	// for a saved asset.
	Reference string `json:"reference,omitempty"`
	// Connection names the Trino connection whose scratch schema holds the
	// table; TableName is optional and defaults to a slug of the filename.
	Connection string `json:"connection,omitempty"`
	TableName  string `json:"table_name,omitempty"`
	// RegistrationID selects the registration unregister drops.
	RegistrationID string `json:"registration_id,omitempty"`
	// Repair asks for a corrected version of the file to be saved and
	// registered when the file cannot be read as a table the way it is stored
	// (#1441).
	Repair bool `json:"repair,omitempty"`
}

// tableRegistrationOutput is the result of the register action.
type tableRegistrationOutput struct {
	Reference string `json:"reference"`
	TableRegistration
	Message string `json:"message"`
}

// tableListOutput is the result of the list action.
type tableListOutput struct {
	Reference     string              `json:"reference"`
	Registrations []TableRegistration `json:"registrations"`
	Total         int                 `json:"total"`
}

// handleManageTable dispatches a manage_table call.
func (t *Toolkit) handleManageTable(
	ctx context.Context, _ *mcp.CallToolRequest, input manageTableInput,
) (*mcp.CallToolResult, any, error) {
	if t.tables == nil {
		return toolkit.ErrorResult(tableRegistrationUnavailable), nil, nil
	}
	if resolveOwnerID(ctx) == anonymousUserName {
		return toolkit.ErrorResult(tableIdentityRequired), nil, nil
	}
	switch input.Action {
	case tableActionRegister:
		return t.handleRegisterTable(ctx, input)
	case tableActionUnregister:
		return t.handleUnregisterTable(ctx, input)
	case tableActionList:
		return t.handleListTables(ctx, input)
	default:
		return toolkit.ErrorResult(fmt.Sprintf(
			"invalid action %q: must be one of: register, list, unregister", input.Action)), nil, nil
	}
}

// handleRegisterTable makes the referenced file's stored CSV queryable.
//
// Nothing is copied: the table points at the directory the file already sits
// in, so a new revision or version means re-registering rather than
// re-loading. Registration is the authority to change the file, not the
// authority to read it: it puts the contents in a schema everyone granted the
// connection can read.
func (t *Toolkit) handleRegisterTable(
	ctx context.Context, input manageTableInput,
) (*mcp.CallToolResult, any, error) {
	if denial := requireReference(input.Reference, tableActionRegister); denial != nil {
		return denial, nil, nil
	}
	if strings.TrimSpace(input.Connection) == "" {
		return toolkit.ErrorResult(
			"connection is required for register: name the Trino connection whose scratch schema the table goes in. " +
				"Call list_connections to see the connections you can reach."), nil, nil
	}

	reg, err := t.tables.Register(ctx, input.Reference, input.Connection, input.TableName, input.Repair)
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}

	message := "Registered as " + reg.QueryTable + " on connection " + reg.Connection +
		". Every column is VARCHAR, so a join to a typed column needs a CAST."
	// A registration that corrected the file says so first: the file changed,
	// and that is the more consequential half of what just happened.
	if reg.Repaired != "" {
		message = reg.Repaired + " " + message
	}
	return toolkit.JSONResultTyped(tableRegistrationOutput{
		Reference:         input.Reference,
		TableRegistration: *reg,
		Message:           message,
	})
}

// handleUnregisterTable drops a registered table.
//
// The file is untouched: dropping an external table removes the catalog entry
// and nothing else.
func (t *Toolkit) handleUnregisterTable(
	ctx context.Context, input manageTableInput,
) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(input.RegistrationID) == "" {
		return toolkit.ErrorResult(
			"registration_id is required for unregister: call manage_table action=list to see them."), nil, nil
	}

	if err := t.tables.Unregister(ctx, input.RegistrationID); err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	return toolkit.JSONResultTyped(map[string]any{
		fieldMessage: "The table was dropped. The file itself is unchanged.",
	})
}

// handleListTables reports the tables registered over a stored file.
func (t *Toolkit) handleListTables(
	ctx context.Context, input manageTableInput,
) (*mcp.CallToolResult, any, error) {
	if denial := requireReference(input.Reference, tableActionList); denial != nil {
		return denial, nil, nil
	}

	regs, err := t.tables.Tables(ctx, input.Reference)
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	if regs == nil {
		regs = []TableRegistration{}
	}
	return toolkit.JSONResultTyped(tableListOutput{
		Reference:     input.Reference,
		Registrations: regs,
		Total:         len(regs),
	})
}

// requireReference refuses an action that names no file, pointing at where a
// reference comes from rather than restating the schema.
func requireReference(reference, action string) *mcp.CallToolResult {
	if strings.TrimSpace(reference) != "" {
		return nil
	}
	return toolkit.ErrorResult(
		"reference is required for " + action + ": pass the mcp:resource: or mcp:asset: reference from a " +
			"search hit, verbatim.")
}

// dropTablesFor removes the tables registered over an asset being deleted.
// A deployment with no registrar has nothing to drop.
func (t *Toolkit) dropTablesFor(ctx context.Context, assetID string) {
	if t.tables == nil {
		return
	}
	t.tables.DropAssetTables(ctx, assetID)
}

// tableRegistrationUnavailable is what manage_table says on a deployment with
// nothing to register onto. It names the missing piece rather than reporting a
// generic failure, because the reader can do something about it.
const tableRegistrationUnavailable = "This deployment cannot register tables: it needs a Trino connection with a " +
	"scratch catalog and schema configured. Ask an administrator to configure one."

// tableIdentityRequired is what an unauthenticated call is told. A
// registration records who made it and decides replacement on that, so there
// is nobody to register under.
const tableIdentityRequired = "Registering a table needs a signed-in identity. This session has none, so there is " +
	"no owner to record the registration under."
