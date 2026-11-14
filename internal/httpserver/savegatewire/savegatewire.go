// Package savegatewire assembles the gate a managed script's source save
// crosses on the portal (#1939, #1942): the script editor's saves and an
// application import (#2476) both go through it, as manage_script's saves go
// through the script layer's.
package savegatewire

import (
	"github.com/txn2/mcp-data-platform/internal/endpoint/epcmd"
	"github.com/txn2/mcp-data-platform/internal/httpserver/scripthttp/handlerhttp"
	"github.com/txn2/mcp-data-platform/internal/platform/notifylayer"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptdraft"
	"github.com/txn2/mcp-data-platform/internal/platform/scripthandler"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrec/recstore"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsave"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsrc"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptstore"
	"github.com/txn2/mcp-data-platform/internal/toolanswer"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// Gate is the save gate over the platform's recordings (sealed with its rest
// encryptor), destinations, memory budget, answer contracts, libraries and
// resource files. A script's handlers must still be served by each version
// saved (#2171, #2441).
func Gate(p *platform.Platform) *scriptsave.Gate {
	gate := &scriptsave.Gate{
		Recordings: recstore.New(p.DB()).Sealed(p.RestEncryptor()), Destinations: p.Config().Scripts.ScriptDestinations(),
		MaxMemoryBytes: p.Config().Scripts.Worker.ProcessRunMemoryBudget(),
		Contracts:      toolanswer.Live{Kits: p.ToolkitRegistry(), Extra: notifylayer.AnswerContracts()},
		Libraries:      scriptstore.New(p.DB()),
		Shared:         scriptstore.New(p.DB()),
		Resources:      scriptsrc.New(p.ResourceStore(), p.ResourceS3Client(), p.Config().Resources.Managed.S3Bucket, p.Config().Resources.Managed.URIScheme),
		CannotServe:    scriptsave.PassthroughConnections{Kits: p.ToolkitRegistry()}.CannotServe,
	}
	if h := Handlers(p); h != nil {
		gate.Handlers = h.Check
	}
	return gate
}

// Handlers is the handler service over the platform's database (#2441): the
// portal's handler routes, the save gate's contract question and an
// application import act through it. Nil with no database.
func Handlers(p *platform.Platform) *scripthandler.Service {
	if p == nil || p.DB() == nil {
		return nil
	}
	var commands *epcmd.Handler
	e := p.Endpoints()
	if e != nil {
		commands = e.Commands
	}
	svc := scripthandler.New(p.DB(), commands, scriptstore.New(p.DB()))
	if svc != nil && e != nil && e.Approvals != nil {
		svc.Approvals = e.Approvals
		svc.Versions = scripthandler.ApprovalVersions{Approvals: e.Approvals}
	}
	if svc != nil && e != nil && e.CallHandlers != nil && e.CallHandlers.Evaluator != nil {
		svc.OnCallChange = e.CallHandlers.Evaluator.Index.Notify
	}
	return svc
}

// CallTester is the test panel's answer for a connection's handlers (#2436),
// nil when the tool-call handlers are not wired.
func CallTester(p *platform.Platform) handlerhttp.Tester {
	if e := p.Endpoints(); e != nil && e.CallHandlers != nil && e.CallHandlers.Evaluator != nil {
		return e.CallHandlers.Evaluator
	}
	return nil
}

// Drafts is the runner a draft started on the portal executes on: the script
// editor's dry run and a form's preview (#2494). It reads the libraries and
// files gate reads and keeps a draft's recording in gate's store, over the
// assembled MCP server, so a draft's platform calls cross the middleware chain
// an agent's calls cross.
func Drafts(p *platform.Platform, gate *scriptsave.Gate) *scriptdraft.Runner {
	return scriptdraft.New(p.MCPServer(), p.Config().Scripts.ScriptDestinations()).
		WithToolkits(p.ToolkitRegistry()).WithExports(p.ScriptDraftExports()).
		WithMemoryBudget(p.Config().Scripts.Worker.ProcessRunMemoryBudget()).WithStatementCeiling(p.Config().Scripts.SQL.MaxTimeout()).
		WithRecordings(gate.Recordings).WithLibraries(gate.Libraries).WithResources(gate.Resources).
		WithTokens(p.Endpoints().TokenIssuer())
}
