package appbundle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/txn2/mcp-data-platform/internal/pglint"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/application/appsvc"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epcmd"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrec"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsave"
	"github.com/txn2/mcp-data-platform/internal/platform/scripttest"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/secretref"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/script"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
	postgreskit "github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// Deps is what export and import use.
type Deps struct {
	Service    *appsvc.Service
	Migrations interface {
		List(ctx context.Context, connection, schema string) ([]postgreskit.Migration, error)
		Put(ctx context.Context, m postgreskit.Migration) (bool, error)
	}
	Bindings interface {
		Get(ctx context.Context, scriptID string) (*epbinding.Binding, error)
	}
	Schedules interface {
		GetSchedule(ctx context.Context, scriptID string) (*script.Schedule, error)
		SetSchedule(ctx context.Context, sched *script.Schedule) error
	}
	// FormConnection is the connection a form's webhook source is on.
	FormConnection func(ctx context.Context, sourceName string) (string, error)
	Blobs          interface {
		GetObject(ctx context.Context, bucket, key string) ([]byte, string, error)
		PutObject(ctx context.Context, bucket, key string, data []byte, contentType string) error
	}
	Lander interface {
		LandResource(ctx context.Context, dest toolkit.ResourceDestination, content io.Reader, contentType string) (*toolkit.ResourceLanding, error)
	}
	Reader interface {
		Source(ctx context.Context, ref string, claims resource.Claims, limit int64) (string, error)
	}
	Claims  func(ctx context.Context) resource.Claims
	Scripts interface {
		Create(ctx context.Context, sc *script.Script, author script.Author) error
		UpdateWithVersion(ctx context.Context, sc *script.Script, author script.Author) error
	}
	Assets interface {
		Insert(ctx context.Context, asset portaldomain.Asset) error
	}
	AssetVersions interface {
		CreateVersion(ctx context.Context, version portaldomain.AssetVersion) (int, error)
	}
	Bucket, Prefix string
	// Endpoints carries out an endpoint command, as manage_script does.
	Endpoints func(ctx context.Context, req epcmd.Request) (map[string]any, error)
	// HasSchemaConnection reports whether an application database
	// connection is configured here.
	HasSchemaConnection func(name string) bool
	// Recordings is where the recorded runs a script's tests replay are read
	// from on export and stored on import (#2476). Nil exports and imports
	// none: a script whose tests name one is refused.
	Recordings interface {
		Get(ctx context.Context, runID string) (*scriptrec.Stored, error)
		Save(ctx context.Context, rec scriptrec.Stored) error
		Keep(ctx context.Context, scriptID, author string, runIDs []string) error
	}
	// SaveGate is the gate every save of a script's source crosses
	// (internal/platform/scriptsave): an imported script is put through it as
	// the importing administrator before anything is written (#2476). Nil
	// refuses an import that holds a script.
	SaveGate SaveGate
}

// SaveGate checks one script source save; *scriptsave.Gate is one.
type SaveGate interface {
	Check(ctx context.Context, req scriptsave.Request) scriptsave.Result
}

// Bundler exports and imports applications.
type Bundler struct{ Deps }

// New builds a Bundler.
func New(d Deps) *Bundler { return &Bundler{Deps: d} }

var errNeedsAdmin = fmt.Errorf("exporting and importing an application is for administrators: %w", application.ErrNotAllowed)

func refused(format string, args ...any) error {
	return &appsvc.RefusedError{Msg: fmt.Sprintf(format, args...)}
}

// Exported is what an export reports.
type Exported struct {
	Application string                   `json:"application"`
	Release     int                      `json:"release"`
	Bundle      *toolkit.ResourceLanding `json:"bundle"`
	Names       Names                    `json:"names"`
	Scripts     int                      `json:"scripts"`
	Assets      int                      `json:"assets"`
	Migrations  int                      `json:"migrations"`
	// Notes are what the importer should know: a script whose connections a
	// static read cannot all see.
	Notes []string `json:"notes"`
}

// Export writes a release of an application as a bundle in the caller's
// library. release 0 is the current release.
func (b *Bundler) Export(ctx context.Context, actor application.Actor, ref string, release int) (any, error) {
	bundle, err := b.Bundle(ctx, actor, ref, release)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, fmt.Errorf("encoding the bundle: %w", err)
	}
	if len(data) > MaxBytes {
		return nil, refused("the bundle is %d bytes, more than the %d a bundle may be", len(data), MaxBytes)
	}
	if b.Lander == nil {
		return nil, refused("this deployment has no managed-resource library to write the bundle to")
	}
	landing, err := b.Lander.LandResource(ctx, toolkit.ResourceDestination{
		Path: "applications/" + bundle.Application.Name, Filename: bundle.Application.Name + "-release-" + strconv.Itoa(bundle.Release) + ".json",
		DisplayName: bundle.Application.Name + " release " + strconv.Itoa(bundle.Release),
		Description: "Application bundle exported by manage_application (#2412).",
	}, bytes.NewReader(data), "application/json")
	if err != nil {
		return nil, fmt.Errorf("writing the bundle: %w", err)
	}
	out := &Exported{
		Application: bundle.Application.Name, Release: bundle.Release, Bundle: landing, Names: bundle.Names,
		Scripts: len(bundle.Scripts), Assets: len(bundle.Assets), Notes: []string{},
	}
	if bundle.Schema != nil {
		out.Migrations = len(bundle.Schema.Migrations)
	}
	for _, s := range bundle.Scripts {
		if s.DynamicConnections {
			out.Notes = append(out.Notes, "script "+s.Name+" names a connection a static read cannot see; map it by hand on import")
		}
	}
	return out, nil
}

// Bundle assembles the bundle of a release for an administrator, without
// writing it: the portal hands it to the browser as a download.
func (b *Bundler) Bundle(ctx context.Context, actor application.Actor, ref string, release int) (*Bundle, error) {
	if !actor.Admin {
		return nil, errNeedsAdmin
	}
	return b.Build(ctx, ref, release)
}

// Build assembles the bundle of a release without writing it.
func (b *Bundler) Build(ctx context.Context, ref string, release int) (*Bundle, error) {
	svc := b.Service
	a, err := svc.Find(ctx, ref)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store's errors are read by identity
	}
	if release == 0 {
		release = a.CurrentRelease
	}
	if release == 0 {
		return nil, refused("application %q has no release to export; release it first", a.Name)
	}
	rel, err := svc.Store.Release(ctx, a.ID, release)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store's errors are read by identity
	}
	out := &Bundle{
		Format: Format, FormatVersion: FormatVersion, Release: rel.Number, Note: rel.Note,
		Application: AppInfo{Name: a.Name, Description: a.Description}, ExportedAt: time.Now().UTC(),
		Scripts: []Script{}, Assets: []Asset{}, Sources: []string{},
	}
	names := collected{connections: nameSet{}, secrets: nameSet{}, sources: nameSet{}, checks: nameSet{}}
	if err := b.buildSchema(ctx, a, rel, out, names); err != nil {
		return nil, err
	}
	if err := b.buildPins(ctx, rel.Pins, out, names); err != nil {
		return nil, err
	}
	for _, p := range a.Parts {
		if p.Kind == application.KindSource {
			out.Sources = append(out.Sources, p.ID)
			names.sources.add(p.ID)
		}
	}
	out.Names = Names{
		Connections: names.connections.sorted(), Secrets: names.secrets.sorted(),
		Sources: names.sources.sorted(), BotChecks: names.checks.sorted(),
	}
	return out, nil
}

// buildPins adds each pinned script and asset version to the bundle.
func (b *Bundler) buildPins(ctx context.Context, pins []application.Pin, out *Bundle, names collected) error {
	for _, p := range pins {
		switch p.Kind {
		case application.KindScript:
			s, err := b.buildScript(ctx, p, names)
			if err != nil {
				return err
			}
			out.Scripts = append(out.Scripts, *s)
		case application.KindSite:
			return refused("a bundle cannot carry a site's files: take site part %s out of the application to export it, and publish the site on the other deployment", p.PartID)
		case application.KindAsset:
			as, err := b.buildAsset(ctx, p)
			if err != nil {
				return err
			}
			out.Assets = append(out.Assets, *as)
		}
	}
	return nil
}

type collected struct {
	connections, secrets, sources, checks nameSet
}

func (b *Bundler) buildSchema(ctx context.Context, a *application.Application, rel *application.Release, out *Bundle, names collected) error {
	if a.Schema == "" {
		return nil
	}
	if b.Migrations == nil {
		return refused("this deployment keeps no migrations to export")
	}
	ms, err := b.Migrations.List(ctx, a.Connection, a.Schema)
	if err != nil {
		return fmt.Errorf("reading the schema's migrations: %w", err)
	}
	s := &Schema{Connection: a.Connection, Schema: a.Schema, Migrations: []Migration{}}
	for _, m := range ms {
		if m.Version > rel.MigrationVersion {
			continue
		}
		if err := reviewed(pglint.Target{}, m.Version, m.Up, m.Down); err != nil {
			return err
		}
		s.Migrations = append(s.Migrations, Migration{
			Version: m.Version, Name: m.Name, Up: m.Up, Down: m.Down, Transactional: m.Transactional,
		})
	}
	names.connections.add(a.Connection)
	out.Schema = s
	return nil
}

// reviewed refuses a migration the migration review refuses at add: one that
// writes a credential (#2411), and one needing a privilege an application's
// role must not hold. A bundle is held to it both ways: an export never
// carries one, and an import, whose migrations come from another deployment,
// never stores one past the review manage_schema add would give it.
func reviewed(target pglint.Target, version int, sqls ...string) error {
	for _, sql := range sqls {
		findings, _ := pglint.Lint(sql, target)
		for _, f := range findings {
			if f.Severity == pglint.SeverityRefused {
				return refused("migration %d is refused by the migration review (%s, %s): %s; nothing was written", version, f.Rule, f.Object, f.Effect)
			}
		}
	}
	return nil
}

func (b *Bundler) buildScript(ctx context.Context, p application.Pin, names collected) (*Script, error) {
	svc := b.Service
	sc, err := svc.Scripts.GetByID(ctx, p.PartID)
	if err != nil || sc == nil {
		return nil, refused("script %s cannot be read", p.PartID)
	}
	v, err := svc.Scripts.GetVersion(ctx, p.PartID, p.Version)
	if err != nil || v == nil {
		return nil, refused("script %s version %d cannot be read", sc.Name, p.Version)
	}
	report := scriptrun.Validate(v.Source)
	names.connections.add(report.Connections...)
	names.connections.add(report.RegisterConnections...)
	names.secrets.add(secretref.Names(v.Source)...)
	names.sources.add(sc.EventSource)
	out := &Script{
		Name: sc.Name, DisplayName: v.DisplayName, Description: v.Description, Category: v.Category,
		Tags: orEmpty(v.Tags), Source: v.Source, Params: v.Params, Exclusive: string(sc.Exclusive),
		EventSource: sc.EventSource, Version: p.Version, DynamicConnections: report.DynamicConnections,
	}
	if out.Params == nil {
		out.Params = []script.Param{}
	}
	if out.Endpoint, err = b.endpointOf(ctx, sc.ID, names); err != nil {
		return nil, err
	}
	if out.Schedule, err = b.scheduleOf(ctx, sc.ID); err != nil {
		return nil, err
	}
	if out.Recordings, err = b.recordingsOf(ctx, sc.Name, v.Source); err != nil {
		return nil, err
	}
	return out, nil
}

// recordingsOf reads each recorded run the source's tests replay. A test
// naming one this deployment no longer holds, or holds without a replayable
// recording, refuses the export: the importing deployment could not run it.
func (b *Bundler) recordingsOf(ctx context.Context, name, source string) ([]Recording, error) {
	ids := scripttest.RecordingsNamed(source)
	if len(ids) == 0 {
		return nil, nil
	}
	if b.Recordings == nil {
		return nil, refused("script %s: its tests replay recorded runs, and this deployment keeps no recordings to export", name)
	}
	out := make([]Recording, 0, len(ids))
	for _, id := range ids {
		rec, err := b.Recordings.Get(ctx, id)
		if err != nil || rec == nil || !rec.Replayable() {
			return nil, refused("script %s: its tests replay recording %s, which this deployment does not hold; save the script with a test that replays a recording it holds", name, id)
		}
		out = append(out, Recording{RunID: id, Data: rec.Data})
	}
	return out, nil
}

// endpointOf is the script's binding; nil, nil when it has none, which is not
// an error: the bundle carries no endpoint for it.
func (b *Bundler) endpointOf(ctx context.Context, scriptID string, names collected) (*Endpoint, error) {
	if b.Bindings == nil {
		return nil, nil //nolint:nilnil // no endpoint store, no endpoint
	}
	bd, err := b.Bindings.Get(ctx, scriptID)
	if errors.Is(err, epbinding.ErrNotFound) {
		return nil, nil //nolint:nilnil // an unbound script has no endpoint
	}
	if err != nil {
		return nil, fmt.Errorf("reading the script's endpoint: %w", err)
	}
	e := &Endpoint{
		Name: bd.Name, Methods: orEmpty(bd.Methods), Path: bd.Path, AuthMode: bd.AuthMode, Callers: orEmpty(bd.Callers),
		BotCheck: bd.BotCheck, CheckedMethods: orEmpty(bd.CheckedMethods), TimeoutSeconds: bd.TimeoutSeconds,
		MaxMemoryBytes: bd.MaxMemoryBytes, MaxBodyBytes: bd.MaxBodyBytes, AllowedOrigins: orEmpty(bd.AllowedOrigins),
		RateLimit: bd.RateLimit, CallerRateLimit: bd.CallerRateLimit, Enabled: bd.Enabled,
		ResponseMode: bd.ResponseMode, PageCDNs: bd.PageCDNs, PageFrameOrigins: bd.PageFrameOrigins,
		PageConnectOrigins: bd.PageConnectOrigins, PageFormOrigins: bd.PageFormOrigins, IPRateLimit: bd.IPRateLimit,
	}
	names.checks.add(bd.BotCheck)
	if b.FormConnection != nil {
		if conn, err := b.FormConnection(ctx, formSource(bd.Name)); err == nil && conn != "" {
			e.FormConnection = conn
			names.connections.add(conn)
		}
	}
	return e, nil
}

// scheduleOf is the script's schedule; nil, nil when it has none.
func (b *Bundler) scheduleOf(ctx context.Context, scriptID string) (*Schedule, error) {
	if b.Schedules == nil {
		return nil, nil //nolint:nilnil // no schedule store, no schedule
	}
	sched, err := b.Schedules.GetSchedule(ctx, scriptID)
	if errors.Is(err, script.ErrScheduleNotFound) {
		return nil, nil //nolint:nilnil // an unscheduled script has no schedule
	}
	if err != nil {
		return nil, fmt.Errorf("reading the script's schedule: %w", err)
	}
	return &Schedule{CronSpec: sched.CronSpec, Timezone: sched.Timezone, Params: sched.Params, Enabled: sched.Enabled}, nil
}

func (b *Bundler) buildAsset(ctx context.Context, p application.Pin) (*Asset, error) {
	svc := b.Service
	as, err := svc.Assets.Get(ctx, p.PartID)
	if err != nil || as == nil {
		return nil, refused("asset %s cannot be read", p.PartID)
	}
	v, err := svc.AssetVersions.GetByVersion(ctx, p.PartID, p.Version)
	if err != nil || v == nil {
		return nil, refused("asset %q version %d cannot be read", as.Name, p.Version)
	}
	if b.Blobs == nil {
		return nil, refused("this deployment has no asset storage to read asset %q from", as.Name)
	}
	bucket := v.S3Bucket
	if bucket == "" {
		bucket = as.S3Bucket
	}
	content, _, err := b.Blobs.GetObject(ctx, bucket, v.S3Key)
	if err != nil {
		return nil, fmt.Errorf("reading asset %q version %d: %w", as.Name, p.Version, err)
	}
	return &Asset{
		Name: as.Name, Description: as.Description, ContentType: v.ContentType, Tags: orEmpty(as.Tags),
		Version: p.Version, Content: content,
	}, nil
}

func orEmpty(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}
