// Package apptool is the manage_application MCP tool (#2411, #2412): an
// application's record, parts, releases, rollbacks, and its export and
// import between deployments, over the one service the portal routes use.
package apptool

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/application/appsvc"
	"github.com/txn2/mcp-data-platform/internal/wirejson"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// ToolName is the tool's name.
const ToolName = "manage_application"

// The commands.
const (
	CmdCreate     = "create"
	CmdGet        = "get"
	CmdList       = "list"
	CmdUpdate     = "update"
	CmdDelete     = "delete"
	CmdAddPart    = "add_part"
	CmdRemovePart = "remove_part"
	CmdSetPart    = "set_part"
	CmdRelease    = "release"
	CmdRollback   = "rollback"
	CmdReleases   = "releases"
	CmdExport     = "export"
	CmdImport     = "import"
	// Environments and variables (#2696).
	CmdPromote           = "promote"
	CmdAddEnvironment    = "add_environment"
	CmdRemoveEnvironment = "remove_environment"
	CmdSetEnvironment    = "set_environment"
	CmdVariables         = "variables"
	CmdSetVariable       = "set_variable"
	CmdDeleteVariable    = "delete_variable"
)

// Commands is every command, in the order the schema lists them.
var Commands = []string{
	CmdCreate, CmdGet, CmdList, CmdUpdate, CmdDelete, CmdAddPart, CmdRemovePart, CmdSetPart,
	CmdRelease, CmdRollback, CmdReleases, CmdExport, CmdImport,
	CmdPromote, CmdAddEnvironment, CmdRemoveEnvironment, CmdSetEnvironment, CmdVariables, CmdSetVariable, CmdDeleteVariable,
}

// Reads is the commands that change nothing.
var Reads = []string{CmdGet, CmdList, CmdReleases}

// Input is the tool's arguments.
type Input struct {
	Command     string `json:"command"`
	Application string `json:"application,omitempty"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Connection  string `json:"connection,omitempty"`
	Schema      string `json:"schema,omitempty"`
	OwnerEmail  string `json:"owner_email,omitempty"`
	Kind        string `json:"kind,omitempty"`
	PartID      string `json:"part_id,omitempty"`
	Note        string `json:"note,omitempty"`
	ToRelease   int    `json:"to_release,omitempty"`
	Release     int    `json:"release,omitempty"`
	// Bundle is the managed resource an import reads, by id or URI.
	Bundle string `json:"bundle,omitempty"`
	// Map names, for each connection and secret the bundle names, the one on
	// this deployment it becomes.
	Map map[string]string `json:"map,omitempty"`
	// ReleaseMode is a site part's: linked or coupled (#2679).
	ReleaseMode string `json:"release_mode,omitempty"`
	// ContractVersion is what update sets the application's contract to.
	ContractVersion *int `json:"contract_version,omitempty"`
	// Contract is the contract version a release declares.
	Contract *int `json:"contract,omitempty"`
	// MoveSites are the site parts a release moves besides the coupled ones.
	MoveSites []string `json:"move_sites,omitempty"`
	// Force releases over a site part that does not accept the contract.
	Force bool `json:"force,omitempty"`
	// Environment is the environment promote moves from, rollback moves
	// back, and a variable belongs to ("" its default); After is where
	// add_environment puts one; RequiresApproval is whether an agent's
	// promote or rollback into it is refused for a person to carry out
	// (#2696).
	Environment      string `json:"environment,omitempty"`
	After            string `json:"after,omitempty"`
	RequiresApproval *bool  `json:"requires_approval,omitempty"`
	// Variable and Value are set_variable's NAME=value.
	Variable string `json:"variable,omitempty"`
	Value    string `json:"value,omitempty"`
}

// Bundles exports and imports applications; nil on a deployment that cannot.
type Bundles interface {
	Export(ctx context.Context, actor application.Actor, ref string, release int) (any, error)
	Import(ctx context.Context, actor application.Actor, bundle string, mapping map[string]string) (any, error)
}

// Tool is the registered tool.
type Tool struct {
	Service *appsvc.Service
	Bundles Bundles
}

// Register adds manage_application to the server.
func (t *Tool) Register(server *mcp.Server) {
	if t == nil || t.Service == nil || server == nil {
		return
	}
	toolkit.AddTool(server, &mcp.Tool{
		Name:        ToolName,
		Title:       "Manage Applications",
		Description: description,
		InputSchema: schema(),
		Annotations: toolkit.WriteAnnotations(true),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in Input) (*mcp.CallToolResult, any, error) {
		return t.Handle(ctx, in)
	})
}

const description = "Applications: one record naming what a system built on the platform is made of — an " +
	"application database schema with its migrations, managed scripts (an endpoint or a form is its script's " +
	"binding), webhook sources and portal assets — released together as a pinned set. A release applies the " +
	"pending migrations and pins each script's and asset's newest version: from then on endpoints, schedules, " +
	"events and runs execute the pinned script version and the portal serves the pinned asset version, so saving " +
	"a script or an asset changes nothing live until the next release. A release whose migration fails changes " +
	"nothing. rollback restores an earlier release's pins and reverts the migrations since when each has a down " +
	"migration. A part belongs to one application. The application database schema is a part too (kind schema), " +
	"added at any time, also to an application that grew from scratch tables or files: add_part kind schema " +
	"part_id \"<connection>/<schema>\" on an existing appdb connection you reach. A schema is migrated only " +
	"inside the application holding it (manage_schema refuses one no application holds), migrations applied " +
	"before it was added are adopted, and its scripts' statements run as a database role the platform creates " +
	"for the application, so only the application's own scripts read and write its schema. Everyone signed in " +
	"reads an application; its owner and " +
	"administrators edit it and add parts they own; administrators release, roll back, export and import, and " +
	"alone take out a part the current release pins or delete a released application. " +
	"export writes a release as a bundle (a managed resource) carrying no secret value, credential, data row or " +
	"run history, and names the connections and secrets it uses; import reads a bundle, maps each name to one on " +
	"this deployment (map), and creates or releases the application. Read the application-building knowledge " +
	"page (fetch mcp:knowledge_page:platform-applications) before building one. " +
	"SITE PARTS (#2679): a site environment part (kind site, part_id \"<site>/<environment>\") is linked or coupled (release_mode). " +
	"Both get the application's /endpoints/, cookies and viewer on the site's own address. A linked part moves only by manage_site " +
	"promote and rollback; a release moves it only when move_sites names it, and a rollback returns it only when the rolled-back " +
	"release moved it. A coupled part moves one step with every release and returns with every rollback. New parts are linked. " +
	"CONTRACT: the application declares contract_version (update, or contract on release), raised when a release breaks what its " +
	"front ends call; a site release's _application file declares the range it works with ({\"application\": \"store\", " +
	"\"contract\": \">=2 <4\"}). A release whose contract a site part's release excludes is refused naming it unless force is set " +
	"(administrator, recorded), and manage_site promote refuses a release that excludes the application's contract. " +
	"ENVIRONMENTS (#2696): an application runs in ordered environments, one (prod) when created; add_environment environment=dev " +
	"with no after puts dev first, where releases land. release lands in the first; promote environment=<name> copies its release to the next; rollback " +
	"environment=<name> moves one back (the last when omitted). The last is what /endpoints/<name> and handlers with no environment run; " +
	"another answers at /environments/<env>/endpoints/<name>, and a schedule or webhook handler names one with manage_script " +
	"handler_environment. Every environment uses the application's schema, and its migrations move with the first environment. " +
	"An environment with requires_approval refuses an agent's promote or rollback into it: a person does it on the application's page. " +
	"VARIABLES: set_variable variable=NAME value=... environment=<name> (omit for the application-wide default); scripts read " +
	"platform.env(\"NAME\") and platform.environment(). A value is plain text; to use a stored secret write {{secret:<name>}}, " +
	"filled only when a request carrying it is sent through a connection the secret allows. variables lists them (owner and administrators)."

// The argument and result keys environments and variables are named by.
const (
	keyApplication = "application"
	keyEnvironment = "environment"
	keyVariable    = "variable"
)

// JSON Schema words the input schema is written in.
const (
	keyType   = "type"
	keyDesc   = "description"
	typString = "string"
)

func schema() map[string]any {
	str := func(desc string) map[string]any { return map[string]any{keyType: typString, keyDesc: desc} }
	num := func(desc string) map[string]any { return map[string]any{keyType: "integer", keyDesc: desc} }
	return map[string]any{
		keyType:    "object",
		"required": []string{"command"},
		"properties": map[string]any{
			"command":     map[string]any{keyType: typString, "enum": Commands, keyDesc: "The operation."},
			"application": str("The application, by name or id (every command but create, list and import)."),
			"name":        str("create: the application's name, a lowercase slug."),
			keyDesc:       str("create, update: what the application is."),
			"connection":  str("create: the application database connection its schema is on (with schema). Same as add_part kind schema afterwards."),
			"schema":      str("create: the schema whose migrations a release applies (with connection). Same as add_part kind schema afterwards."),
			"owner_email": str("update: move the application to another owner (administrators)."),
			"kind":        map[string]any{keyType: typString, "enum": application.Kinds, keyDesc: "add_part, remove_part: the kind of part."},
			"part_id":     str("add_part, remove_part, set_part: the script's id, the asset's id, the webhook source's name, or for kind schema \"<connection>/<schema>\" (the schema alone when you reach one application database connection)."),
			"note":        str("release: what the release is for."),
			"release_mode": map[string]any{
				keyType: typString, "enum": []string{application.ModeLinked, application.ModeCoupled},
				keyDesc: "add_part (kind site, default linked) and set_part: linked serves the site environment as a front end of the application " +
					"(its /endpoints/, cookies and viewer) and leaves it to the site's own promote and rollback; coupled also moves it one step " +
					"with every release and returns it with every rollback. Parts added before v1.163.0 are coupled.",
			},
			"contract_version": num("update: the contract version the application declares, a whole number raised by hand when a release breaks what " +
				"its front ends call; 0 declares none."),
			"contract": num("release: the contract version this release declares, saved once the release is recorded."),
			"move_sites": map[string]any{
				keyType: "array", "items": map[string]any{keyType: typString},
				keyDesc: "release: site parts (\"<site>/<environment>\") to move one step in this release, linked or not; a rollback of the release returns them.",
			},
			"force":      map[string]any{keyType: "boolean", keyDesc: "release: release over a site part whose release does not accept the contract (administrator); the release records each one."},
			"to_release": num("rollback: the release whose pins to restore; the one before the current release when omitted."),
			keyEnvironment: str("promote: the environment whose release moves to the next. rollback: the environment moved back (the last when omitted). " +
				"add_environment, remove_environment, set_environment: the environment. set_variable, delete_variable: the environment the variable " +
				"belongs to, omitted for the application-wide default."),
			"after":             str("add_environment: the environment the new one follows; omitted puts it first. It cannot follow the last."),
			"requires_approval": map[string]any{keyType: "boolean", keyDesc: "add_environment, set_environment: an agent's promote or rollback into it is refused; a person carries it out in the portal."},
			keyVariable:         str("set_variable, delete_variable: the variable's NAME (uppercase letters, digits and underscores)."),
			"value":             str("set_variable: the value, plain text up to 8 KiB; {{secret:<name>}} references a stored secret."),
			"release":           num("export: the release to export; the current release when omitted."),
			"bundle":            str("import: the managed resource holding the bundle, by id or mcp:// URI."),
			"map": map[string]any{
				keyType: "object", "additionalProperties": map[string]any{keyType: typString},
				keyDesc: "import: for each connection and secret name the bundle names, the name on this deployment it becomes.",
			},
		},
	}
}

// actorOf is who is calling.
func actorOf(ctx context.Context) application.Actor {
	pc := middleware.GetPlatformContext(ctx)
	if pc == nil {
		return application.Actor{}
	}
	return application.Actor{Email: pc.UserEmail, Admin: pc.IsAdmin, Subject: pc.UserID, Roles: pc.Roles}
}

// insideRun reports whether a managed-script run made the call.
func insideRun(ctx context.Context) bool {
	pc := middleware.GetPlatformContext(ctx)
	return pc != nil && pc.Source == middleware.SourceScript
}

func isRead(cmd string) bool { return slices.Contains(Reads, cmd) }

// Handle runs one command.
func (t *Tool) Handle(ctx context.Context, in Input) (*mcp.CallToolResult, any, error) {
	if !isRead(in.Command) && insideRun(ctx) {
		return errorResult(ToolName + " " + in.Command + " cannot be called from inside a script run: a run never " +
			"changes what an application is or what it serves. Make the change from your own session.")
	}
	v, err := t.run(ctx, actorOf(ctx), in)
	if err != nil {
		return errorResult(message(err))
	}
	return jsonResult(v)
}

// handler carries out one command.
type handler func(ctx context.Context, actor application.Actor, in Input) (any, error)

// handlers is the dispatch table. The service's errors are the caller's
// message (message reads them), so they are returned as they are.
func (t *Tool) handlers() map[string]handler {
	s := t.Service
	return map[string]handler{
		CmdCreate: func(ctx context.Context, actor application.Actor, in Input) (any, error) {
			return s.Create(ctx, actor, application.Application{ //nolint:wrapcheck // the caller's message
				Name: in.Name, Description: in.Description, Connection: in.Connection, Schema: in.Schema,
			})
		},
		CmdGet: func(ctx context.Context, _ application.Actor, in Input) (any, error) {
			return s.StatusOf(ctx, in.Application) //nolint:wrapcheck // the caller's message
		},
		CmdList: func(ctx context.Context, _ application.Actor, _ Input) (any, error) {
			return listResult(s.Store.List(ctx))
		},
		CmdUpdate: func(ctx context.Context, actor application.Actor, in Input) (any, error) {
			a, err := s.Update(ctx, actor, in.Application, in.Description, in.OwnerEmail)
			if err != nil || in.ContractVersion == nil {
				return a, err //nolint:wrapcheck // the caller's message
			}
			return s.SetContract(ctx, actor, in.Application, *in.ContractVersion) //nolint:wrapcheck // the caller's message
		},
		CmdDelete: func(ctx context.Context, actor application.Actor, in Input) (any, error) {
			if err := s.Delete(ctx, actor, in.Application); err != nil {
				return nil, err //nolint:wrapcheck // the caller's message
			}
			return map[string]any{"deleted": in.Application}, nil
		},
		CmdAddPart: func(ctx context.Context, actor application.Actor, in Input) (any, error) {
			return s.AddPartMode(ctx, actor, in.Application, application.Part{Kind: in.Kind, ID: in.PartID, ReleaseMode: in.ReleaseMode}) //nolint:wrapcheck // the caller's message
		},
		CmdRemovePart: func(ctx context.Context, actor application.Actor, in Input) (any, error) {
			return s.RemovePart(ctx, actor, in.Application, in.Kind, in.PartID) //nolint:wrapcheck // the caller's message
		},
		CmdSetPart: func(ctx context.Context, actor application.Actor, in Input) (any, error) {
			return s.SetPartMode(ctx, actor, in.Application, in.PartID, in.ReleaseMode) //nolint:wrapcheck // the caller's message
		},
		CmdRelease: func(ctx context.Context, actor application.Actor, in Input) (any, error) {
			return s.ReleaseWith(ctx, actor, in.Application, appsvc.ReleaseOptions{ //nolint:wrapcheck // the caller's message
				Note: in.Note, MoveSites: in.MoveSites, Contract: in.Contract, Force: in.Force,
			})
		},
		CmdRollback: func(ctx context.Context, actor application.Actor, in Input) (any, error) {
			if err := t.agentMove(ctx, in.Application, in.Environment, false); err != nil {
				return nil, err
			}
			return s.RollbackIn(ctx, actor, in.Application, in.Environment, in.ToRelease) //nolint:wrapcheck // the caller's message
		},
		CmdPromote:           t.promote,
		CmdAddEnvironment:    t.addEnvironment,
		CmdRemoveEnvironment: t.removeEnvironment,
		CmdSetEnvironment:    t.setEnvironment,
		CmdVariables:         t.variables,
		CmdSetVariable:       t.setVariable,
		CmdDeleteVariable:    t.deleteVariable,
		CmdReleases: func(ctx context.Context, _ application.Actor, in Input) (any, error) {
			return t.releases(ctx, in.Application)
		},
		CmdExport: t.bundle,
		CmdImport: t.bundle,
	}
}

func (t *Tool) run(ctx context.Context, actor application.Actor, in Input) (any, error) {
	h, ok := t.handlers()[in.Command]
	if !ok {
		return nil, &appsvc.RefusedError{Msg: fmt.Sprintf("unknown command %q", in.Command)}
	}
	return h(ctx, actor, in)
}

// listed is what list answers.
type listed struct {
	Applications []application.Application `json:"applications"`
	Total        int                       `json:"total"`
}

func listResult(apps []application.Application, err error) (any, error) {
	if err != nil {
		return nil, fmt.Errorf("listing applications: %w", err)
	}
	total := len(apps)
	return listed{Applications: apps, Total: total}, nil
}

func (t *Tool) releases(ctx context.Context, ref string) (any, error) {
	a, err := t.Service.Find(ctx, ref)
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller's message
	}
	rels, err := t.Service.Store.Releases(ctx, a.ID)
	if err != nil {
		return nil, fmt.Errorf("listing releases: %w", err)
	}
	return map[string]any{"application": a.Name, "current_release": a.CurrentRelease, "releases": rels}, nil
}

func (t *Tool) bundle(ctx context.Context, actor application.Actor, in Input) (any, error) {
	if t.Bundles == nil {
		return nil, &appsvc.RefusedError{Msg: "this deployment cannot export or import applications: it has no managed resources"}
	}
	if in.Command == CmdExport {
		return t.Bundles.Export(ctx, actor, in.Application, in.Release) //nolint:wrapcheck // the message is the caller's
	}
	return t.Bundles.Import(ctx, actor, in.Bundle, in.Map) //nolint:wrapcheck // the message is the caller's
}

// message is the caller-facing text of an error.
func message(err error) string {
	switch {
	case errors.Is(err, application.ErrNotFound):
		return "no such application: " + err.Error()
	case errors.Is(err, application.ErrNameTaken), errors.Is(err, application.ErrSchemaHeld),
		errors.Is(err, application.ErrPartTaken), errors.Is(err, application.ErrNoPart),
		errors.Is(err, application.ErrNoRelease), errors.Is(err, application.ErrNotAllowed),
		errors.Is(err, application.ErrStale):
		return err.Error()
	}
	if refused, ok := errors.AsType[*appsvc.RefusedError](err); ok {
		return refused.Msg
	}
	return err.Error()
}

func errorResult(msg string) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}, nil, nil
}

func jsonResult(v any) (*mcp.CallToolResult, any, error) {
	data, err := wirejson.Marshal(v)
	if err != nil {
		return errorResult("failed to encode the result: " + err.Error())
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}, nil, nil
}
