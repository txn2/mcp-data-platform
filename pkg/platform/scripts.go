package platform

import (
	"github.com/txn2/mcp-data-platform/internal/platform/scriptexec"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptlayer"
)

// wireScripts assembles the managed-script feature and returns its execution
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
// It is a function over the platform rather than a method on it, following
// wireUtilConnection: composition is not behavior the facade should own, and
// the god-object gate is what keeps that distinction from eroding.
func wireScripts(p *Platform) *scriptexec.Handle {
	scripts := scriptexec.New(scriptexec.Config{
		DB:     p.db,
		DSN:    p.config.Database.DSN,
		Server: p.mcpServer,
		Export: scriptexec.ExportDeps{
			Assets:   p.portalStore.AssetStore(),
			Versions: p.portalStore.VersionStore(),
			S3:       p.portalStore.S3Client(),
			Bucket:   p.config.Portal.S3Bucket,
			Prefix:   p.config.Portal.S3Prefix,
		},
		Audit:                 p.audit.Logger(),
		Metrics:               p.obs.Metrics(),
		RunRetention:          p.config.Scripts.RunRetention(),
		WorkerDisabled:        !p.config.Scripts.IsWorkerEnabled(),
		NotificationsDisabled: !p.config.Notifications.IsEnabled(),
		DigestHourUTC:         p.config.Notifications.DigestHour(),
	})
	p.scripts = scriptlayer.New(scriptlayer.Config{
		DB:           p.db,
		Runs:         scripts.Runs(),
		AdminPersona: p.config.Admin.Persona,
		PortalURL:    p.config.Portal.PublicBaseURL,
	})
	p.scripts.RegisterTool(p.mcpServer)
	return scripts
}
