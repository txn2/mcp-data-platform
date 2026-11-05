// Package savegatewire assembles the gate a managed script's source save
// crosses on the portal (#1939, #1942): the script editor's saves and an
// application import (#2476) both go through it, as manage_script's saves go
// through the script layer's.
package savegatewire

import (
	"github.com/txn2/mcp-data-platform/internal/platform/notifylayer"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrec/recstore"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsave"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsrc"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptstore"
	"github.com/txn2/mcp-data-platform/internal/toolanswer"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// Gate is the save gate over the platform's recordings (sealed with its rest
// encryptor), destinations, memory budget, answer contracts, libraries and
// resource files. A script bound to an endpoint must keep defining
// handle(req) (#2171).
func Gate(p *platform.Platform) *scriptsave.Gate {
	gate := &scriptsave.Gate{
		Recordings: recstore.New(p.DB()).Sealed(p.RestEncryptor()), Destinations: p.Config().Scripts.ScriptDestinations(),
		MaxMemoryBytes: p.Config().Scripts.Worker.ProcessRunMemoryBudget(),
		Contracts:      toolanswer.Live{Kits: p.ToolkitRegistry(), Extra: notifylayer.AnswerContracts()},
		Libraries:      scriptstore.New(p.DB()),
		Resources:      scriptsrc.New(p.ResourceStore(), p.ResourceS3Client(), p.Config().Resources.Managed.S3Bucket, p.Config().Resources.Managed.URIScheme),
		CannotServe:    scriptsave.PassthroughConnections{Kits: p.ToolkitRegistry()}.CannotServe,
	}
	if e := p.Endpoints(); e != nil {
		gate.Endpoint = e.Commands.Bound
	}
	return gate
}
