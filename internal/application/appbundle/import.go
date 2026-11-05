package appbundle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epcmd"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrec"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsave"
	"github.com/txn2/mcp-data-platform/internal/platform/scripttest"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/runstate"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/pkg/script"
	postgreskit "github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// formSource is the webhook source a form bound at endpoint is filed in.
func formSource(endpoint string) string { return whsource.FormSourceName(endpoint) }

// Imported is what an import reports.
type Imported struct {
	Application string               `json:"application"`
	Created     bool                 `json:"created"`
	Release     *application.Release `json:"release"`
	// Scripts and Assets name each part and whether the import created it,
	// gave it a new version, or found it unchanged.
	Scripts map[string]string `json:"scripts"`
	Assets  map[string]string `json:"assets"`
}

// What the import did to a part.
const (
	partCreated   = "created"
	partNewVer    = "new_version"
	partUnchanged = "unchanged"
)

// importer is one import's state: the caller, the mapping, the application.
type importer struct {
	*Bundler
	actor   application.Actor
	author  script.Author
	ownerID string
	mapping map[string]string
	app     *application.Application
	scripts map[string]*script.Script
	assets  map[string]*portaldomain.Asset
	// gated is what the save gate made of each bundle script, by name: the
	// formatted source a write stores and the test report it keeps.
	gated map[string]scriptsave.Result
	// carried is the recordings each bundle script's tests replay, renamed
	// to this deployment's names, by script name and run id; fresh names the
	// ones this deployment does not hold yet, which the write stores.
	carried  map[string]map[string][]byte
	fresh    map[string]map[string]bool
	imported *Imported
}

// Import reads a bundle from a managed resource, maps every name it uses to
// mapping's name on this deployment, creates the application or finds the one
// with its name, writes each part, and releases it. A name left unmapped
// refuses the import before anything is written.
func (b *Bundler) Import(ctx context.Context, actor application.Actor, ref string, mapping map[string]string) (any, error) {
	if !actor.Admin {
		return nil, errNeedsAdmin
	}
	bundle, err := b.read(ctx, ref)
	if err != nil {
		return nil, err
	}
	out, err := b.ImportBundle(ctx, actor, bundle, mapping)
	if err != nil {
		return nil, err // never an interface holding a nil *Imported
	}
	return out, nil
}

// ImportBundle imports a bundle already read: the portal's upload.
func (b *Bundler) ImportBundle(ctx context.Context, actor application.Actor, bundle *Bundle, mapping map[string]string) (*Imported, error) {
	if !actor.Admin {
		return nil, errNeedsAdmin
	}
	if err := checkFormat(bundle); err != nil {
		return nil, err
	}
	if err := checkMapped(bundle.Names, mapping); err != nil {
		return nil, err
	}
	im := b.importer(actor, mapping)
	// Everything an import refuses is decided before the first write: the
	// application it lands in is found (not yet created), and every migration
	// and every script is reviewed as this deployment reviews a save (#2476).
	if err := im.locate(ctx, bundle); err != nil {
		return nil, err
	}
	if err := im.review(ctx, bundle); err != nil {
		return nil, err
	}
	if err := im.application(ctx, bundle); err != nil {
		return nil, err
	}
	if err := im.migrations(ctx, bundle.Schema); err != nil {
		return nil, err
	}
	if err := im.parts(ctx, bundle); err != nil {
		return nil, err
	}
	note := fmt.Sprintf("imported from release %d of %s", bundle.Release, bundle.Application.Name)
	rel, err := b.Service.ReleaseAs(ctx, actor, im.app.ID, note, application.ActionImport)
	if err != nil {
		return nil, fmt.Errorf("the parts were written but the release did not apply: %w", err)
	}
	im.imported.Release = rel
	return im.imported, nil
}

func (b *Bundler) importer(actor application.Actor, mapping map[string]string) *importer {
	im := &importer{
		Bundler: b, actor: actor, mapping: mapping,
		author:  script.Author{Email: actor.Email},
		scripts: map[string]*script.Script{}, assets: map[string]*portaldomain.Asset{},
		gated:   map[string]scriptsave.Result{},
		carried: map[string]map[string][]byte{}, fresh: map[string]map[string]bool{},
		imported: &Imported{Scripts: map[string]string{}, Assets: map[string]string{}},
	}
	// The run identity of an imported script is the importing administrator's,
	// captured from their own session (the MCP call or the portal request):
	// nothing in a bundle names who a part runs as.
	im.author.Roles, im.author.Subject, im.ownerID = slices.Clone(actor.Roles), actor.Subject, actor.Subject
	if im.ownerID == "" {
		im.ownerID = actor.Email
	}
	return im
}

func (b *Bundler) read(ctx context.Context, ref string) (*Bundle, error) {
	if b.Reader == nil || b.Claims == nil {
		return nil, refused("this deployment has no managed-resource library to read a bundle from")
	}
	if strings.TrimSpace(ref) == "" {
		return nil, refused("name the bundle: the managed resource export wrote, by mcp:resource:<id> or its mcp:// URI")
	}
	text, err := b.Reader.Source(ctx, ref, b.Claims(ctx), MaxBytes)
	if err != nil {
		return nil, fmt.Errorf("reading the bundle: %w", err)
	}
	var bundle Bundle
	if err := json.Unmarshal([]byte(text), &bundle); err != nil {
		return nil, refused("the bundle is not a JSON application bundle: %v", err)
	}
	return &bundle, nil
}

// checkFormat refuses a bundle this deployment does not read.
func checkFormat(bundle *Bundle) error {
	if bundle == nil || bundle.Format != Format || bundle.FormatVersion != FormatVersion {
		var format string
		var version int
		if bundle != nil {
			format, version = bundle.Format, bundle.FormatVersion
		}
		return refused("the bundle's format is %q version %d; this deployment reads %q version %d",
			format, version, Format, FormatVersion)
	}
	return nil
}

// checkMapped refuses a bundle with a name the mapping does not map, naming
// each by kind.
func checkMapped(n Names, mapping map[string]string) error {
	var missing []string
	for _, kind := range []struct {
		label string
		names []string
	}{{"connection", n.Connections}, {"secret", n.Secrets}, {"webhook source", n.Sources}, {"bot check", n.BotChecks}} {
		for _, name := range kind.names {
			if strings.TrimSpace(mapping[name]) == "" {
				missing = append(missing, fmt.Sprintf("%s %q", kind.label, name))
			}
		}
	}
	if len(missing) > 0 {
		return refused("nothing was imported: map every name the bundle uses to one on this deployment (map): unmapped %s",
			strings.Join(missing, ", "))
	}
	// A mapped name is written into script sources, inside a string literal,
	// so it is a plain name and nothing else.
	for _, name := range slices.Sorted(maps.Keys(mapping)) {
		if to := strings.TrimSpace(mapping[name]); to != "" && !mappedName.MatchString(to) {
			return refused("nothing was imported: map %q to a name of letters, digits, '.', '_' and '-', not %q", name, to)
		}
	}
	return nil
}

// mappedName is what a name the bundle is mapped to may be.
var mappedName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

func (im *importer) mapped(name string) string {
	if v := strings.TrimSpace(im.mapping[name]); v != "" {
		return v
	}
	return name
}

// locate finds the application the bundle names and reads its parts, or
// leaves im.app nil when there is none yet. It writes nothing.
func (im *importer) locate(ctx context.Context, bundle *Bundle) error {
	conn, schema := im.schemaOf(bundle)
	if bundle.Schema != nil && (im.HasSchemaConnection == nil || !im.HasSchemaConnection(conn)) {
		return refused("nothing was imported: %q is not an application database connection here", conn)
	}
	a, err := im.Service.Store.GetByName(ctx, bundle.Application.Name)
	switch {
	case errors.Is(err, application.ErrNotFound):
		return nil
	case err != nil:
		return fmt.Errorf("reading application %q: %w", bundle.Application.Name, err)
	case a.Connection != conn || a.Schema != schema:
		return refused("nothing was imported: application %q here is on %s/%s, the bundle on %s/%s", a.Name,
			a.Connection, a.Schema, conn, schema)
	}
	im.app, im.imported.Application = a, a.Name
	return im.indexParts(ctx)
}

// schemaOf is the connection, mapped, and schema the bundle's migrations are
// for, empty when it carries none.
func (im *importer) schemaOf(bundle *Bundle) (conn, schema string) {
	if bundle.Schema == nil {
		return "", ""
	}
	return im.mapped(bundle.Schema.Connection), bundle.Schema.Schema
}

// review refuses the import before anything is written when a migration is
// one manage_schema add refuses, or a script one this deployment's save gate
// refuses (#2476). A script is checked as the importing administrator saving
// it, into the part with its name when the application holds one; what it
// does differently from that part is agreed by the import itself, with the
// bundle's release as the summary.
func (im *importer) review(ctx context.Context, bundle *Bundle) error {
	if bundle.Schema != nil {
		for _, m := range bundle.Schema.Migrations {
			if err := reviewed(m.Version, m.Up, m.Down); err != nil {
				return err
			}
		}
	}
	if len(bundle.Scripts) > 0 && im.SaveGate == nil {
		return refused("nothing was imported: this deployment has no script save gate to check the bundle's scripts with")
	}
	summary := fmt.Sprintf("imported from release %d of %s", bundle.Release, bundle.Application.Name)
	for i := range bundle.Scripts {
		s := &bundle.Scripts[i]
		source := rename(s.Source, im.mapping)
		if err := im.carry(ctx, s, source); err != nil {
			return err
		}
		res := im.SaveGate.Check(ctx, scriptsave.Request{
			Existing: im.scripts[s.Name], Name: s.Name, Source: source,
			Caller:        scriptsave.Caller{Email: im.actor.Email, Admin: im.actor.Admin, Roles: slices.Clone(im.actor.Roles)},
			ChangeSummary: summary, Agreed: true, Carried: im.carried[s.Name],
		})
		if res.Refused() {
			return refused("nothing was imported: script %s is refused by this deployment's save gate: %s", s.Name, res.Refusal)
		}
		im.gated[s.Name] = res
	}
	return nil
}

// carry reads the recordings a bundle script's tests replay, renamed by the
// mapping as its source is. Only the ones the renamed source's tests name are
// read. One over the size a run's own recording may hold, one that does not
// read as a recording, and one whose run id this deployment holds with other
// content refuse the import: a bundle never replaces another script's
// recording.
func (im *importer) carry(ctx context.Context, s *Script, source string) error {
	named := scripttest.RecordingsNamed(source)
	if len(named) == 0 {
		return nil
	}
	if im.Recordings == nil {
		return refused("nothing was imported: script %s replays recorded runs in its tests, and this deployment keeps no recordings", s.Name)
	}
	carried, fresh := map[string][]byte{}, map[string]bool{}
	for _, r := range s.Recordings {
		if !slices.Contains(named, r.RunID) || carried[r.RunID] != nil {
			continue
		}
		data, held, err := im.carried1(ctx, s.Name, r)
		if err != nil {
			return err
		}
		carried[r.RunID], fresh[r.RunID] = data, !held
	}
	im.carried[s.Name], im.fresh[s.Name] = carried, fresh
	return nil
}

// carried1 reads one carried recording: renamed by the mapping, and whether
// this deployment already holds it under its run id, refusing one it holds
// with other content.
func (im *importer) carried1(ctx context.Context, name string, r Recording) (data []byte, held bool, err error) {
	if len(r.Data) > scriptrec.MaxBytes {
		return nil, false, refused("nothing was imported: script %s's recording %s is over the %d MiB a recording may hold", name, r.RunID, scriptrec.MaxBytes>>20)
	}
	if data, err = scriptrec.Renamed(r.Data, im.renameText); err != nil {
		return nil, false, refused("nothing was imported: script %s's recording %s cannot be read: %v", name, r.RunID, err)
	}
	stored, err := im.Recordings.Get(ctx, r.RunID)
	switch {
	case errors.Is(err, scriptrec.ErrNotFound):
		return data, false, nil
	case err != nil:
		return nil, false, fmt.Errorf("reading recording %s: %w", r.RunID, err)
	case !scriptrec.Same(stored.Data, data):
		return nil, false, refused("nothing was imported: script %s's recording %s is held here with other content", name, r.RunID)
	}
	return data, true, nil
}

// renameText is a string in a recording's arguments under the mapping: a name
// the mapping maps, or text holding a {{secret:name}} placeholder of one.
func (im *importer) renameText(v string) string {
	if to := strings.TrimSpace(im.mapping[v]); to != "" {
		return to
	}
	if !strings.Contains(v, "{{secret:") {
		return v
	}
	return rename(v, im.mapping)
}

// keepRecordings stores the recordings a script's tests replay that this
// deployment did not hold, as the importing administrator's drafts of the
// script, and marks the ones its tests name kept.
func (im *importer) keepRecordings(ctx context.Context, sc *script.Script) error {
	for id, data := range im.carried[sc.Name] {
		if !im.fresh[sc.Name][id] {
			continue
		}
		if err := im.Recordings.Save(ctx, scriptrec.Stored{
			RunID: id, ScriptID: sc.ID, ScriptName: sc.Name, Kind: scriptrec.KindDraft, RecordedBy: im.actor.Email,
			SourceSHA256: scriptrec.SourceHash(sc.Source), Succeeded: true, Bytes: len(data), Data: data,
		}); err != nil {
			return fmt.Errorf("storing recording %s of script %s: %w", id, sc.Name, err)
		}
	}
	if im.Recordings == nil || len(im.carried[sc.Name]) == 0 {
		return nil
	}
	if err := im.Recordings.Keep(ctx, sc.ID, im.actor.Email, scripttest.RecordingsNamed(sc.Source)); err != nil {
		return fmt.Errorf("keeping the recordings of script %s: %w", sc.Name, err)
	}
	return nil
}

// application creates the application the bundle names when locate found
// none.
func (im *importer) application(ctx context.Context, bundle *Bundle) error {
	if im.app != nil {
		return nil
	}
	conn, schema := im.schemaOf(bundle)
	a, err := im.Service.Create(ctx, im.actor, application.Application{
		Name: bundle.Application.Name, Description: bundle.Application.Description, Connection: conn, Schema: schema,
	})
	if err != nil {
		return err //nolint:wrapcheck // the service's refusals are the caller's
	}
	im.imported.Created = true
	im.app, im.imported.Application = a, a.Name
	return nil
}

// indexParts reads the application's script and asset parts by name, which is
// how a later release finds the part an earlier import created.
func (im *importer) indexParts(ctx context.Context) error {
	for _, p := range im.app.Parts {
		switch p.Kind {
		case application.KindScript:
			if sc, err := im.Service.Scripts.GetByID(ctx, p.ID); err == nil && sc != nil {
				im.scripts[sc.Name] = sc
			}
		case application.KindAsset:
			if as, err := im.Service.Assets.Get(ctx, p.ID); err == nil && as != nil && as.DeletedAt == nil {
				im.assets[as.Name] = as
			}
		}
	}
	return nil
}

// migrations stores each migration the platform does not hold yet. One it
// holds with a different checksum is drift, and refuses the import.
func (im *importer) migrations(ctx context.Context, s *Schema) error {
	if s == nil {
		return nil
	}
	if im.Migrations == nil {
		return refused("this deployment keeps no migrations")
	}
	conn := im.mapped(s.Connection)
	have, err := im.Migrations.List(ctx, conn, s.Schema)
	if err != nil {
		return fmt.Errorf("reading the schema's migrations: %w", err)
	}
	held := map[int]postgreskit.Migration{}
	for _, m := range have {
		held[m.Version] = m
	}
	for _, m := range s.Migrations {
		sum := postgreskit.ChecksumOf(m.Up, m.Down, m.Transactional)
		if h, ok := held[m.Version]; ok {
			if h.Checksum != sum {
				return refused("nothing was imported: migration %d of %s/%s differs from the bundle's", m.Version, conn, s.Schema)
			}
			continue
		}
		if _, err := im.Migrations.Put(ctx, postgreskit.Migration{
			Connection: conn, Schema: s.Schema, Version: m.Version, Name: m.Name, Up: m.Up, Down: m.Down,
			Transactional: m.Transactional, Checksum: sum, Author: im.actor.Email,
		}); err != nil {
			return fmt.Errorf("storing migration %d: %w", m.Version, err)
		}
	}
	return nil
}

// parts writes every script and asset and adds each, and every source, to the
// application.
func (im *importer) parts(ctx context.Context, bundle *Bundle) error {
	for i := range bundle.Scripts {
		id, err := im.script(ctx, &bundle.Scripts[i])
		if err != nil {
			return err
		}
		if err := im.add(ctx, application.KindScript, id); err != nil {
			return err
		}
	}
	for i := range bundle.Assets {
		id, err := im.asset(ctx, &bundle.Assets[i])
		if err != nil {
			return err
		}
		if err := im.add(ctx, application.KindAsset, id); err != nil {
			return err
		}
	}
	for _, src := range bundle.Sources {
		if err := im.add(ctx, application.KindSource, im.mapped(src)); err != nil {
			return err
		}
	}
	return nil
}

func (im *importer) add(ctx context.Context, kind, id string) error {
	if slices.ContainsFunc(im.app.Parts, func(p application.Part) bool { return p.Kind == kind && p.ID == id }) {
		return nil
	}
	a, err := im.Service.AddPart(ctx, im.actor, im.app.ID, kind, id)
	if err != nil {
		return fmt.Errorf("adding %s %s: %w", kind, id, err)
	}
	im.app = a
	return nil
}

// script writes one script: a new version of the part with its name, or a new
// script owned by the importing administrator.
func (im *importer) script(ctx context.Context, s *Script) (string, error) {
	sc, err := im.writeScript(ctx, s, im.gated[s.Name])
	if err != nil {
		return "", err
	}
	if err := im.keepRecordings(ctx, sc); err != nil {
		return "", err
	}
	if err := im.eventSource(ctx, sc, s); err != nil {
		return "", err
	}
	if err := im.endpoint(ctx, sc, s.Endpoint); err != nil {
		return "", err
	}
	return sc.ID, im.schedule(ctx, sc, s.Schedule)
}

// writeScript creates the script, gives the part with its name a new
// version, or leaves it when nothing it runs changed.
func (im *importer) writeScript(ctx context.Context, s *Script, gated scriptsave.Result) (*script.Script, error) {
	src := gated.Lint.Source
	sc := im.scripts[s.Name]
	if sc == nil {
		sc = &script.Script{
			Name: s.Name, DisplayName: s.DisplayName, Description: s.Description, Category: s.Category,
			Tags: s.Tags, Params: s.Params, OwnerEmail: im.actor.Email, Enabled: true,
			Status: script.StatusActive, Exclusive: runstate.ExclusiveMode(s.Exclusive),
		}
		gated.Apply(sc)
		if err := sc.Validate(); err != nil {
			return nil, refused("script %s: %v", s.Name, err)
		}
		if err := im.Scripts.Create(ctx, sc, im.author); err != nil {
			return nil, fmt.Errorf("creating script %s: %w", s.Name, err)
		}
		im.imported.Scripts[s.Name] = partCreated
		return sc, nil
	}
	if sc.Source == src && sc.Description == s.Description && sameParams(sc.Params, s.Params) {
		im.imported.Scripts[s.Name] = partUnchanged
		return sc, nil
	}
	next := *sc
	gated.Apply(&next)
	next.Params, next.DisplayName, next.Description = s.Params, s.DisplayName, s.Description
	next.Category, next.Tags = s.Category, s.Tags
	if err := next.Validate(); err != nil {
		return nil, refused("script %s: %v", s.Name, err)
	}
	if err := im.Scripts.UpdateWithVersion(ctx, &next, im.author); err != nil {
		return nil, fmt.Errorf("updating script %s: %w", s.Name, err)
	}
	im.imported.Scripts[s.Name] = partNewVer
	return &next, nil
}

func sameParams(a, b []script.Param) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

// eventSource sets the webhook source the script runs for, mapped.
func (im *importer) eventSource(ctx context.Context, sc *script.Script, s *Script) error {
	want := ""
	if s.EventSource != "" {
		want = im.mapped(s.EventSource)
	}
	if sc.EventSource == want {
		return nil
	}
	updater, ok := im.Scripts.(interface {
		Update(ctx context.Context, sc *script.Script) error
	})
	if !ok {
		return refused("this deployment cannot set the webhook source script %s runs for", sc.Name)
	}
	sc.EventSource = want
	if err := updater.Update(ctx, sc); err != nil {
		return fmt.Errorf("setting the webhook source of script %s: %w", sc.Name, err)
	}
	return nil
}

// endpoint binds the script as the bundle's endpoint, as endpoint_set does.
func (im *importer) endpoint(ctx context.Context, sc *script.Script, e *Endpoint) error {
	if e == nil {
		return nil
	}
	if im.Endpoints == nil {
		return refused("this deployment cannot bind endpoints, which script %s needs", sc.Name)
	}
	path := e.Path
	in := epcmd.Input{
		Endpoint: e.Name, Methods: e.Methods, Path: &path, AuthMode: e.AuthMode, Callers: e.Callers,
		CheckedMethods: e.CheckedMethods, MaxMemoryBytes: e.MaxMemoryBytes, MaxBodyBytes: e.MaxBodyBytes,
		AllowedOrigins: e.AllowedOrigins, RateLimit: e.RateLimit, CallerRateLimit: e.CallerRateLimit,
	}
	if e.BotCheck != "" {
		in.BotCheck = im.mapped(e.BotCheck)
	}
	if e.FormConnection != "" {
		in.Connection = im.mapped(e.FormConnection)
	}
	req := epcmd.Request{Command: epcmd.CmdSet, Input: in, TimeoutSeconds: e.TimeoutSeconds, Script: sc, Admin: true, Actor: im.actor.Email}
	if _, err := im.Endpoints(ctx, req); err != nil {
		return fmt.Errorf("binding script %s to endpoint %s: %w", sc.Name, e.Name, err)
	}
	if !e.Enabled {
		req.Command = epcmd.CmdDisable
		if _, err := im.Endpoints(ctx, req); err != nil {
			return fmt.Errorf("disabling endpoint %s: %w", e.Name, err)
		}
	}
	return nil
}

// schedule sets the script's schedule.
func (im *importer) schedule(ctx context.Context, sc *script.Script, s *Schedule) error {
	if s == nil || im.Schedules == nil {
		return nil
	}
	prev, err := im.Schedules.GetSchedule(ctx, sc.ID)
	if err != nil && !errors.Is(err, script.ErrScheduleNotFound) {
		return fmt.Errorf("reading the schedule of script %s: %w", sc.Name, err)
	}
	enabled := s.Enabled
	sched, err := script.BuildSchedule(sc, prev, script.ScheduleRequest{
		CronSpec: s.CronSpec, Timezone: s.Timezone, Params: maps.Clone(s.Params), Enabled: &enabled, Actor: im.actor.Email,
	}, time.Now())
	if err != nil {
		return refused("the schedule of script %s: %v", sc.Name, err)
	}
	if err := im.Schedules.SetSchedule(ctx, sched); err != nil {
		return fmt.Errorf("setting the schedule of script %s: %w", sc.Name, err)
	}
	return nil
}

// asset writes one asset: a new version of the part with its name, or a new
// asset owned by the importing administrator.
func (im *importer) asset(ctx context.Context, a *Asset) (string, error) {
	if im.Blobs == nil || im.Assets == nil || im.AssetVersions == nil || im.Bucket == "" {
		return "", refused("this deployment has no asset storage to import asset %q into", a.Name)
	}
	existing := im.assets[a.Name]
	if existing != nil {
		if cur, _, err := im.Blobs.GetObject(ctx, existing.S3Bucket, existing.S3Key); err == nil && bytes.Equal(cur, a.Content) {
			im.imported.Assets[a.Name] = partUnchanged
			return existing.ID, nil
		}
		return existing.ID, im.assetVersion(ctx, a, assetHome{existing.ID, existing.OwnerID, existing.S3Bucket}, partNewVer)
	}
	id := uuid.NewString()
	if err := im.Assets.Insert(ctx, portaldomain.Asset{
		ID: id, OwnerID: im.ownerID, OwnerEmail: im.actor.Email, Name: a.Name, Description: a.Description,
		ContentType: a.ContentType, S3Bucket: im.Bucket, Tags: orEmpty(a.Tags),
	}); err != nil {
		return "", fmt.Errorf("creating asset %q: %w", a.Name, err)
	}
	return id, im.assetVersion(ctx, a, assetHome{id, im.ownerID, im.Bucket}, partCreated)
}

// assetHome is the asset a version is written to, its owner and its bucket.
type assetHome struct{ id, owner, bucket string }

// assetVersion stores the bundle's content as the asset's next version.
func (im *importer) assetVersion(ctx context.Context, a *Asset, home assetHome, state string) error {
	id, owner, bucket := home.id, home.owner, home.bucket
	versionID := uuid.NewString()
	key := portaldomain.AssetContentKey(im.Prefix, owner, id, versionID, portaldomain.ExtensionForContentType(a.ContentType))
	if err := im.Blobs.PutObject(ctx, bucket, key, a.Content, a.ContentType); err != nil {
		return fmt.Errorf("storing asset %q: %w", a.Name, err)
	}
	if _, err := im.AssetVersions.CreateVersion(ctx, portaldomain.AssetVersion{
		ID: versionID, AssetID: id, S3Key: key, S3Bucket: bucket, ContentType: a.ContentType,
		SizeBytes: int64(len(a.Content)), CreatedBy: im.actor.Email, ChangeSummary: "Imported from an application bundle",
	}); err != nil {
		return fmt.Errorf("recording asset %q: %w", a.Name, err)
	}
	im.imported.Assets[a.Name] = state
	return nil
}

// rename rewrites the names a script names to the names they are mapped to:
// a string literal that is exactly the name, and a {{secret:name}}
// placeholder.
func rename(source string, mapping map[string]string) string {
	keys := slices.Sorted(maps.Keys(mapping))
	pairs := []string{}
	for _, from := range keys {
		to := strings.TrimSpace(mapping[from])
		if to == "" || to == from {
			continue
		}
		pairs = append(pairs, `"`+from+`"`, `"`+to+`"`, `'`+from+`'`, `'`+to+`'`,
			"{{secret:"+from+"}}", "{{secret:"+to+"}}")
	}
	if len(pairs) == 0 {
		return source
	}
	return strings.NewReplacer(pairs...).Replace(source)
}
