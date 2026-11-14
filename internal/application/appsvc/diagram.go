package appsvc

import (
	"context"
	"strconv"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/application/appdiagram"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// The versions of an application a diagram is drawn at (#2540), besides a
// release's number.
const (
	DiagramCurrent = "current"
	DiagramLatest  = "latest"
)

// DiagramDeps is what the diagram reads besides the service's stores: a
// script's endpoint bindings, an asset version's text, and the tables an
// application schema's migrations fill. Each may be nil, which leaves out
// what it reads.
type DiagramDeps struct {
	Bindings     func(ctx context.Context, scriptID string) ([]appdiagram.Binding, error)
	AssetContent func(ctx context.Context, assetID string, version int) string
	SchemaWrites func(ctx context.Context, connection, schema string) []string
}

// DiagramRequest is a diagram asked for: the application, the version drawn
// (current, latest, or a release's number), the version it is compared with
// (none when empty), and which assets the caller opens.
type DiagramRequest struct {
	App     string
	Version string
	Compare string
	View    AssetViewer
}

// Diagram draws how an application's parts fit together (#2540). An asset
// the caller cannot open is drawn unnamed and its content is not read.
func (s *Service) Diagram(ctx context.Context, actor application.Actor, req DiagramRequest) (*appdiagram.Diagram, error) {
	a, err := s.Find(ctx, req.App)
	if err != nil {
		return nil, err
	}
	r := &diagramReader{s: s, actor: actor, view: req.View}
	d, err := s.draw(ctx, r, a, req.Version)
	if err != nil || req.Compare == "" {
		return d, err
	}
	prev, err := s.draw(ctx, r, a, req.Compare)
	if err != nil {
		return nil, err
	}
	return appdiagram.Compare(d, prev), nil
}

// draw draws a at one version.
func (s *Service) draw(ctx context.Context, r appdiagram.Reader, a *application.Application, version string) (*appdiagram.Diagram, error) {
	if version == "" {
		version = DiagramCurrent
	}
	parts, err := s.partsAt(ctx, a, version)
	if err != nil {
		return nil, err
	}
	return appdiagram.Build(ctx, r, appdiagram.Input{App: a, Parts: parts, Version: version}), nil
}

// partsAt is each part at the version drawn: latest is each part's newest
// version; current is the version the current release pins, and the newest
// of a part it does not pin; a release's number is that release's pins, with
// the webhook sources and schema the application holds now.
func (s *Service) partsAt(ctx context.Context, a *application.Application, version string) ([]appdiagram.Part, error) {
	if version != DiagramLatest && version != DiagramCurrent {
		return s.releasePartsAt(ctx, a, version)
	}
	var pins *application.Release
	if version == DiagramCurrent && a.CurrentRelease > 0 {
		rel, err := s.Store.Release(ctx, a.ID, a.CurrentRelease)
		if err != nil {
			return nil, err //nolint:wrapcheck // the store's errors are read by identity
		}
		pins = rel
	}
	out := make([]appdiagram.Part, 0, len(a.Parts))
	for _, p := range a.Parts {
		v := pins.PinOf(p.Kind, p.ID)
		if v == 0 {
			pin, _ := s.newestPin(ctx, p)
			v = pin.Version
		}
		out = append(out, appdiagram.Part{Part: p, Version: v})
	}
	return out, nil
}

// releasePartsAt is the parts of the release version numbers.
func (s *Service) releasePartsAt(ctx context.Context, a *application.Application, version string) ([]appdiagram.Part, error) {
	n, err := strconv.Atoi(version)
	if err != nil || n <= 0 {
		return nil, refused("version %q: current, latest or a release's number", version)
	}
	rel, err := s.Store.Release(ctx, a.ID, n)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store's errors are read by identity
	}
	return releaseParts(a, rel), nil
}

// releaseParts is a release's pinned parts, with the parts it pins nothing
// of (webhook sources, the schema) as the application holds them now.
func releaseParts(a *application.Application, rel *application.Release) []appdiagram.Part {
	out := []appdiagram.Part{}
	for _, pin := range rel.Pins {
		p := appdiagram.Part{Version: pin.Version}
		p.Kind, p.ID = pin.Kind, pin.PartID
		out = append(out, p)
	}
	for _, p := range a.Parts {
		if p.Kind == application.KindSource || p.Kind == application.KindSchema {
			out = append(out, appdiagram.Part{Part: p})
		}
	}
	return out
}

// findings is what the diagram of the application's newest versions finds:
// what a release now would carry. No asset is read, so nothing an asset
// holds reaches the lines.
func (s *Service) findings(ctx context.Context, a *application.Application) []appdiagram.Finding {
	d, err := s.draw(ctx, &diagramReader{s: s, hideAssets: true}, a, DiagramLatest)
	if err != nil {
		return nil
	}
	return d.Findings
}

// blocking refuses a release a finding blocks, naming each.
func blocking(fs []appdiagram.Finding) error {
	var lines []string
	for _, f := range fs {
		if f.Blocks {
			lines = append(lines, f.Message)
		}
	}
	if len(lines) == 0 {
		return nil
	}
	return refused("nothing was released: %s; inside an application the release's pin is the version, so load the version it pins", strings.Join(lines, "; "))
}

// diagramReader is what the diagram reads, over the service's stores.
type diagramReader struct {
	s          *Service
	actor      application.Actor
	view       AssetViewer
	hideAssets bool
}

// Script reads a script.
func (r *diagramReader) Script(ctx context.Context, id string) (*script.Script, error) {
	return r.s.script(ctx, id)
}

// ScriptVersion reads one version of a script.
func (r *diagramReader) ScriptVersion(ctx context.Context, id string, version int) (*script.Version, error) {
	if r.s.Scripts == nil {
		return nil, nil //nolint:nilnil // no store, no version
	}
	return r.s.Scripts.GetVersion(ctx, id, version) //nolint:wrapcheck // the diagram leaves out what it cannot read
}

// Schedules reads a script's schedules.
func (r *diagramReader) Schedules(ctx context.Context, scriptID string) ([]script.Schedule, error) {
	if r.s.Schedules == nil {
		return nil, nil
	}
	return r.s.Schedules.ListSchedules(ctx, script.ScheduleFilter{ScriptID: scriptID}) //nolint:wrapcheck // the diagram leaves out what it cannot read
}

// Bindings reads a script's endpoint bindings.
func (r *diagramReader) Bindings(ctx context.Context, scriptID string) ([]appdiagram.Binding, error) {
	if r.s.Diagrams.Bindings == nil {
		return nil, nil
	}
	return r.s.Diagrams.Bindings(ctx, scriptID)
}

// EventSources reads the webhook sources a script runs for.
func (r *diagramReader) EventSources(ctx context.Context, scriptID string) []string {
	return r.s.eventSources(ctx, scriptID)
}

// Asset reads an asset the caller opens.
func (r *diagramReader) Asset(ctx context.Context, id string) (name, identity string, ok bool) {
	if r.hideAssets {
		return "", "", false
	}
	as, err := r.s.asset(ctx, id)
	if err != nil {
		return "", "", false
	}
	if !r.actor.Admin && !strings.EqualFold(as.OwnerEmail, r.actor.Email) && (r.view == nil || !r.view(ctx, as)) {
		return "", "", false
	}
	return as.Name, as.IdempotencyKey, true
}

// AssetContent reads an asset version's text.
func (r *diagramReader) AssetContent(ctx context.Context, id string, version int) string {
	if r.s.Diagrams.AssetContent == nil {
		return ""
	}
	return r.s.Diagrams.AssetContent(ctx, id, version)
}

// SchemaWrites reads the tables a schema's migrations fill.
func (r *diagramReader) SchemaWrites(ctx context.Context, connection, schema string) []string {
	if r.s.Diagrams.SchemaWrites == nil {
		return nil
	}
	return r.s.Diagrams.SchemaWrites(ctx, connection, schema)
}

// IsAppDB reports whether a connection is an application database.
func (r *diagramReader) IsAppDB(connection string) bool {
	return r.s.HasConnection != nil && r.s.HasConnection(connection)
}

// FindingsIntroduced is what a save of the script scriptID, over version
// before, newly gives its application's diagram to find (#2540): the
// findings of the application's newest versions that the same application
// with the script at before does not have. Empty for a script that is part
// of no application.
func (s *Service) FindingsIntroduced(ctx context.Context, scriptID string, before int) []string {
	a, err := s.Store.Owning(ctx, application.KindScript, scriptID)
	if err != nil {
		return nil
	}
	r := &diagramReader{s: s, hideAssets: true}
	after, err := s.partsAt(ctx, a, DiagramLatest)
	if err != nil {
		return nil
	}
	prior := make([]appdiagram.Part, len(after))
	copy(prior, after)
	for i := range prior {
		if prior[i].Kind == application.KindScript && prior[i].ID == scriptID {
			prior[i].Version = before
		}
	}
	had := map[string]bool{}
	for _, f := range appdiagram.Build(ctx, r, appdiagram.Input{App: a, Parts: prior, Version: DiagramLatest}).Findings {
		had[f.Message] = true
	}
	out := []string{}
	for _, f := range appdiagram.Build(ctx, r, appdiagram.Input{App: a, Parts: after, Version: DiagramLatest}).Findings {
		if !had[f.Message] {
			out = append(out, "application "+a.Name+": "+f.Message)
		}
	}
	return out
}
