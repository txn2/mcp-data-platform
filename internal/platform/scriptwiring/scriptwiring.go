// Package scriptwiring assembles the managed-script feature: the execution
// handle the lifecycle starts, and the tool layer that authors and enqueues
// onto it.
//
// It is a seam rather than a method on the facade for the reason every other
// composition seam here is one, and the reason its own construction comment
// already gave: composition is not behavior the facade should own, and the
// facade is at its size budget.
package scriptwiring

import (
	"context"
	"database/sql"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/platform/resourcewrite"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptadmit"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptdraft"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptexec"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptlayer"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Wire assembles the managed-script feature and returns its execution
// handle for the lifecycle to start and stop. The handle also owns the schedule
// materializer, which runs wherever the run worker does.
//
// The two halves are wired together here because only one of them can exist
// without the other: the tool layer registers manage_script on any database
// deployment, while run_script appears only where there is a queue to enqueue
// onto, so a deployment that cannot execute scripts still authors them and says
// plainly that nothing will run them.
//
// The handle is built whether or not this replica runs the worker: the serving
// half of a split deployment still owns the queue it enqueues onto.
//
// Wire builds both halves and registers the tool layer, returning the handle.
func Wire(deps Deps) *scriptexec.Handle {
	scripts := scriptexec.New(scriptexec.Config{
		DB:     deps.DB,
		DSN:    deps.DSN,
		Server: deps.Server,
		Export: scriptexec.ExportDeps{
			Assets:   deps.Assets,
			Versions: deps.Versions,
			S3:       deps.S3,
			Bucket:   deps.Bucket,
			Prefix:   deps.Prefix,
			// A version a script writes moves the tables that follow its
			// asset (#1536). The registrar does not exist yet; the portal
			// layer reaches it through the toolkit it is bound to later.
			FollowTables: deps.FollowTables,
			// The same managed-resource destination the export tools land in
			// (#1663), which is how a script's output becomes one rolling file
			// with a version history rather than an asset series only it owns.
			Lander: deps.Lander,
		},
		Audit: deps.Audit,
		// The subject a run's author authenticates as, so the run files
		// managed resources where that person's own session does (#1677).
		Subjects:              deps.Subjects,
		Metrics:               deps.Metrics,
		Destinations:          deps.Destinations,
		PortalURL:             deps.PortalURL,
		RunRetention:          deps.RunRetention,
		Limits:                runLimits(deps.Worker),
		Admission:             admission(deps.Worker),
		MaxReclaims:           deps.Worker.MaxReclaims,
		WorkerDisabled:        !deps.WorkerEnabled,
		NotificationsDisabled: !deps.NotificationsEnabled,
		DigestHourUTC:         deps.DigestHourUTC,
	})
	layer := scriptlayer.New(scriptlayer.Config{
		DB:           deps.DB,
		Runs:         scripts.Runs(),
		AdminPersona: deps.AdminPersona,
		PortalURL:    deps.PortalURL,
		Destinations: deps.Destinations,
		// The ceilings a platform run executes under, which the help reports
		// beside the draft limits (#1843).
		RunLimits: runLimits(deps.Worker),
		// The live toolkits, so a draft's write barrier reads what an
		// api_invoke_endpoint call sends and what a proxied tool's upstream
		// declares, rather than refusing both (#1664).
		Toolkits: deps.Toolkits,
		// A draft allowed to write persists its exports through the writer a
		// platform run uses, over the same stores (#1822).
		DraftExports: draftExports(scripts),
	})
	layer.RegisterTool(deps.Server)
	deps.Bind(layer)
	return scripts
}

// draftExports adapts the execution handle's draft writer to the draft
// runner's hook. A deployment with nowhere to keep runs has no handle, and its
// drafts preview every export.
func draftExports(scripts *scriptexec.Handle) scriptdraft.Exports {
	if scripts == nil {
		return nil
	}
	return func(t scriptdraft.Target) scriptrun.Exporter {
		return scripts.DraftExporter(scriptexec.Draft{
			Script: t.Script, RunID: t.RunID, Caller: t.Caller,
			Email: t.Identity.Email, Subject: t.Identity.UserID, Roles: t.Identity.Roles,
		})
	}
}

// Deps are what the facade hands over: values it already holds, and one
// callback for the handle it keeps.
type Deps struct {
	DB     *sql.DB
	DSN    string
	Server *mcp.Server

	Assets       portal.AssetStore
	Versions     portal.VersionStore
	S3           portal.S3Client
	Bucket       string
	Prefix       string
	FollowTables func(ctx context.Context, assetID string, version int) []string
	Lander       *resourcewrite.Ref

	Audit    middleware.AuditLogger
	Subjects scriptexec.SubjectResolver
	Metrics  *observability.Metrics

	Destinations []script.Destination
	RunRetention time.Duration
	// Worker is the scripts.worker capacity settings: admission and a
	// platform run's ceilings (#1843).
	Worker               scriptadmit.Config
	WorkerEnabled        bool
	NotificationsEnabled bool
	DigestHourUTC        int
	PortalURL            string
	AdminPersona         string
	Toolkits             *registry.Registry

	// Bind hands the assembled tool layer back to the facade, which keeps it
	// because the index queue binds its write-path producer (#1370).
	Bind func(*scriptlayer.Handle)
}

// runLimits is a platform run's configured ceilings; unset fields take the
// engine's defaults where the limits are applied. The memory budget is
// resolved against this process's memory limit (#1861), which a draft on this
// replica and a run on it share.
func runLimits(c scriptadmit.Config) scriptrun.PlatformLimits {
	return scriptrun.PlatformLimits{
		Timeout: c.RunTimeout, MaxSteps: uint64(max(c.MaxSteps, 0)), MaxRows: c.MaxQueryRows,
		ResultMaxBytes: c.ResultMaxBytes, MaxMemoryBytes: c.ProcessRunMemoryBudget(),
	}
}

// admission is the configured admission. Config.Validate refuses a value
// scriptadmit cannot read before the platform is built, so one that arrives
// here unread (a platform assembled without validating its config) runs
// adaptive.
func admission(c scriptadmit.Config) scriptadmit.Admission {
	adm, err := c.Admission()
	if err != nil {
		return scriptadmit.Admission{}
	}
	return adm
}
