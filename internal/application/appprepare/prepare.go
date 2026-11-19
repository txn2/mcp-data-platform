package appprepare

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/application/appschema"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptbehavior"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptlib"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrec"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsave"
	"github.com/txn2/mcp-data-platform/internal/platform/scripttest"
	"github.com/txn2/mcp-data-platform/internal/schemaref"
	"github.com/txn2/mcp-data-platform/pkg/script"
	postgreskit "github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// Scripts reads an application's scripts and saves their new versions.
type Scripts interface {
	GetByID(ctx context.Context, id string) (*script.Script, error)
	UpdateWithVersion(ctx context.Context, sc *script.Script, author script.Author) error
}

// Libraries reads a library's version and its record by name.
type Libraries interface {
	LibrarySource(ctx context.Context, name string, version int) (string, error)
	LibraryNamed(ctx context.Context, name string) (*script.Script, error)
}

// Migrations reads and replaces the application's migrations.
type Migrations interface {
	List(ctx context.Context, connection, schema string) ([]postgreskit.Migration, error)
	Put(ctx context.Context, m postgreskit.Migration) (bool, error)
}

// Pending reads which of the application's migrations its schema has not
// applied.
type Pending interface {
	StateIn(ctx context.Context, at appschema.Target) (*appschema.State, error)
}

// Gate is the save gate a script's new source crosses.
type Gate interface {
	Check(ctx context.Context, req scriptsave.Request) scriptsave.Result
}

// Preparer prepares applications.
type Preparer struct {
	Scripts    Scripts
	Libraries  Libraries
	Migrations Migrations
	Schema     Pending
	Gate       Gate
	// Recordings is where a script's recorded runs are kept: the ones its
	// tests and the save gate replay are stored again with the same
	// statements rewritten once its new version is saved, so the next save
	// still replays them (#2709). Nil rewrites none.
	Recordings scriptrec.Store
}

// The kinds of file a plan lists.
const (
	KindScript    = "script"
	KindLibrary   = "library"
	KindMigration = "migration"
)

// Counts is how many of an application's files name its schema or roles.
type Counts struct {
	Scripts    int `json:"scripts"`
	Libraries  int `json:"libraries"`
	Migrations int `json:"migrations"`
}

// Total is every file counted.
func (c Counts) Total() int { return c.Scripts + c.Libraries + c.Migrations }

// File is one file prepare changes: what it names, the text before and after,
// and for a script or library what the save gate made of the new text.
type File struct {
	Kind string `json:"kind" example:"script"`
	// ID is a script's or library's id, a migration's version.
	ID   string `json:"id" example:"5b0c..."`
	Name string `json:"name" example:"orders-api"`
	// Version is the version changed: a script's current version, a
	// migration's own.
	Version int `json:"version" example:"4"`
	// Names is how many names of the schema or its roles it holds.
	Names  int    `json:"names" example:"3"`
	Before string `json:"before"`
	After  string `json:"after"`
	// Replayed is the recorded runs the save gate replayed against the new
	// text, and Differences what any of them answered differently.
	Replayed    []string                    `json:"replayed"`
	Differences []scriptbehavior.Difference `json:"differences"`
	// Refusal is why the save gate refuses the new text, empty when it does
	// not.
	Refusal string `json:"refusal,omitempty"`
	// Saved is the version saved, 0 until it is.
	Saved int `json:"saved,omitempty"`
}

// Plan is what prepare does to one application.
type Plan struct {
	Application string `json:"application" example:"shop"`
	Schema      string `json:"schema" example:"orders"`
	Files       []File `json:"files"`
	// Values is each qualified name inside a quoted SQL value: data, such as
	// an outbox row's source table, that prepare leaves as written. Each
	// environment needs its own text there (current_schema() || '.invoices'),
	// so a person changes it; it does not block an environment.
	Values []Value `json:"values"`
	Counts Counts  `json:"counts"`
	// Ready is true when nothing names the schema or its roles, so an
	// environment can be added.
	Ready bool `json:"ready"`
	// Saved is true when the plan's changes were saved.
	Saved bool `json:"saved"`
}

// Value is one qualified name inside a quoted SQL value of a file.
type Value struct {
	Kind string `json:"kind" example:"script"`
	Name string `json:"name" example:"orders-api"`
	Line int    `json:"line" example:"86"`
	Text string `json:"text" example:"orders.invoices"`
}

// addValues records the quoted values of one file.
func (plan *Plan) addValues(kind, name string, vals []schemaref.Value) {
	for _, v := range vals {
		plan.Values = append(plan.Values, Value{Kind: kind, Name: name, Line: v.Line, Text: v.Text})
	}
}

// ErrNotReady refuses saving a plan the save gate refuses a file of.
var ErrNotReady = errors.New("prepare saves nothing while the save gate refuses a file; see each file's refusal and differences")

// schemas is every schema the application runs in.
func schemas(a *application.Application) []string {
	out := []string{a.Schema}
	for _, e := range a.Environments {
		if e.Schema != "" && !slices.Contains(out, e.Schema) {
			out = append(out, e.Schema)
		}
	}
	return out
}

// Named counts the files of a that name its schema or its roles: its
// scripts, the libraries they load and its pending migrations. An
// application holding no schema names none.
func (p *Preparer) Named(ctx context.Context, a *application.Application) (Counts, error) {
	plan, err := p.read(ctx, a)
	if err != nil {
		return Counts{}, err
	}
	return plan.Counts, nil
}

// Plan is what prepare would change in a, each changed script and library
// put through the save gate without saving.
func (p *Preparer) Plan(ctx context.Context, a *application.Application, caller scriptsave.Caller) (*Plan, error) {
	plan, err := p.read(ctx, a)
	if err != nil {
		return nil, err
	}
	for i := range plan.Files {
		f := &plan.Files[i]
		if f.Kind == KindMigration {
			continue
		}
		sc, err := p.Scripts.GetByID(ctx, f.ID)
		if err != nil {
			return nil, fmt.Errorf("reading %s %s: %w", f.Kind, f.Name, err)
		}
		p.check(ctx, f, sc, caller, schemas(a))
	}
	return plan, nil
}

// rewrite is the text change prepare makes, applied to one string.
func rewrite(names []string) func(string) string {
	return func(s string) string {
		out, _ := schemaref.Unqualified(s, names)
		return out
	}
}

// check puts f's new text through the save gate against sc, recording what
// it made of it. The recorded runs it replays see their statements rewritten
// as the source was.
func (p *Preparer) check(ctx context.Context, f *File, sc *script.Script, caller scriptsave.Caller, names []string) scriptsave.Result {
	res := p.Gate.Check(ctx, scriptsave.Request{
		Existing: sc, Name: sc.Name, Source: f.After, Caller: caller, RecordingText: rewrite(names),
		ChangeSummary: "prepared for environments of their own: the schema qualifier removed from its SQL (#2709)",
	})
	f.Replayed, f.Differences, f.Refusal = res.Replayed, res.Differences, res.Refusal
	return res
}

// read finds every file of a that names its schema or roles, with the text
// prepare proposes for it.
func (p *Preparer) read(ctx context.Context, a *application.Application) (*Plan, error) {
	plan := &Plan{Application: a.Name, Schema: a.Schema, Files: []File{}, Values: []Value{}}
	if a.Schema == "" {
		plan.Ready = true
		return plan, nil
	}
	names := schemas(a)
	libs := map[scriptlib.Ref]bool{}
	for _, part := range a.Parts {
		if part.Kind != application.KindScript {
			continue
		}
		sc, err := p.Scripts.GetByID(ctx, part.ID)
		if err != nil || sc == nil {
			return nil, fmt.Errorf("reading script %s: %w", part.ID, err)
		}
		after, n, loads := schemaref.Script(sc.Source, names)
		plan.addValues(KindScript, sc.Name, schemaref.Values(sc.Source, names))
		for _, ref := range loads {
			libs[ref] = true
		}
		if n > 0 {
			plan.Files = append(plan.Files, File{Kind: KindScript, ID: sc.ID, Name: sc.Name, Version: sc.Version, Names: n, Before: sc.Source, After: after})
			plan.Counts.Scripts++
		}
	}
	if err := p.readLibraries(ctx, plan, libs, names); err != nil {
		return nil, err
	}
	if err := p.readMigrations(ctx, a, plan); err != nil {
		return nil, err
	}
	plan.Ready = plan.Counts.Total() == 0
	return plan, nil
}

// readLibraries adds each loaded library version that names a schema.
func (p *Preparer) readLibraries(ctx context.Context, plan *Plan, libs map[scriptlib.Ref]bool, names []string) error {
	if p.Libraries == nil {
		return nil
	}
	refs := make([]scriptlib.Ref, 0, len(libs))
	for ref := range libs {
		refs = append(refs, ref)
	}
	slices.SortFunc(refs, compareRefs)
	for _, ref := range refs {
		source, err := p.Libraries.LibrarySource(ctx, ref.Name, ref.Version)
		if err != nil {
			continue // a load the script cannot make fails its own save; prepare names what it can read
		}
		after, n, _ := schemaref.Script(source, names)
		plan.addValues(KindLibrary, ref.Name, schemaref.Values(source, names))
		if n == 0 {
			continue
		}
		lib, err := p.Libraries.LibraryNamed(ctx, ref.Name)
		if err != nil {
			return fmt.Errorf("reading library %s: %w", ref.Name, err)
		}
		plan.Files = append(plan.Files, File{Kind: KindLibrary, ID: lib.ID, Name: ref.Name, Version: ref.Version, Names: n, Before: source, After: after})
		plan.Counts.Libraries++
	}
	return nil
}

func compareRefs(x, y scriptlib.Ref) int {
	if x.Name != y.Name {
		if x.Name < y.Name {
			return -1
		}
		return 1
	}
	return x.Version - y.Version
}

// readMigrations adds each pending migration that names the schema or a
// role; an applied one stays as its ledger records it.
func (p *Preparer) readMigrations(ctx context.Context, a *application.Application, plan *Plan) error {
	if p.Migrations == nil || p.Schema == nil {
		return nil
	}
	st, err := p.Schema.StateIn(ctx, appschema.Target{Connection: a.Connection, Source: a.Schema, Schema: a.Schema})
	if err != nil {
		return fmt.Errorf("reading the schema's migrations: %w", err)
	}
	ms, err := p.Migrations.List(ctx, a.Connection, a.Schema)
	if err != nil {
		return fmt.Errorf("reading the schema's migrations: %w", err)
	}
	for _, m := range ms {
		if !slices.Contains(st.Pending, m.Version) {
			continue
		}
		up, n := migrationText(m.Up, a.Schema)
		_, d := migrationText(m.Down, a.Schema)
		plan.addValues(KindMigration, m.Name, schemaref.ValuesIn(m.Up, []string{a.Schema}))
		if n+d == 0 {
			continue
		}
		plan.Files = append(plan.Files, File{
			Kind: KindMigration, ID: fmt.Sprint(m.Version), Name: m.Name, Version: m.Version, Names: n + d,
			Before: m.Up, After: up, Replayed: []string{}, Differences: []scriptbehavior.Difference{},
		})
		plan.Counts.Migrations++
	}
	return nil
}

// Save saves what Plan proposes: each library a new version, each script a
// new version loading the libraries' new versions, each pending migration
// replaced. Nothing is saved while the save gate refuses a script or a
// library; a library saved before a script it is loaded by is refused stays
// a version nothing loads.
func (p *Preparer) Save(ctx context.Context, a *application.Application, caller scriptsave.Caller, author script.Author) (*Plan, error) {
	plan, err := p.Plan(ctx, a, caller)
	if err != nil {
		return nil, err
	}
	for _, f := range plan.Files {
		if f.Refusal != "" {
			return plan, ErrNotReady
		}
	}
	sv := &saving{caller: caller, author: author, moved: map[string]int{}, names: schemas(a)}
	if err := p.saveKind(ctx, plan, KindLibrary, sv); err != nil {
		return plan, err
	}
	if err := p.saveKind(ctx, plan, KindScript, sv); err != nil {
		return plan, err
	}
	if err := p.saveMigrations(ctx, a, plan, author); err != nil {
		return plan, err
	}
	plan.Saved = true
	return plan, nil
}

// saving is one save of a plan: who saves it, the library versions saved so
// far, and the schemas the application runs in.
type saving struct {
	caller scriptsave.Caller
	author script.Author
	moved  map[string]int
	names  []string
}

// saveKind saves each file of the plan of kind: a library records the
// version a script loading it moves to, and a script's recorded runs are
// rewritten once it is saved.
func (p *Preparer) saveKind(ctx context.Context, plan *Plan, kind string, sv *saving) error {
	for i := range plan.Files {
		f := &plan.Files[i]
		if f.Kind != kind {
			continue
		}
		if err := p.saveScript(ctx, f, sv); err != nil {
			return err
		}
		if kind == KindLibrary {
			sv.moved[f.Name] = f.Saved
			continue
		}
		if err := p.rewriteRecordings(ctx, f, sv.names); err != nil {
			return err
		}
	}
	return nil
}

// saveScript gives f's script or library a new version holding its prepared
// text, its library loads moved to the versions just saved.
func (p *Preparer) saveScript(ctx context.Context, f *File, sv *saving) error {
	sc, err := p.Scripts.GetByID(ctx, f.ID)
	if err != nil {
		return fmt.Errorf("reading %s %s: %w", f.Kind, f.Name, err)
	}
	if f.Kind == KindScript && len(sv.moved) > 0 {
		_, _, loads := schemaref.Script(f.After, nil)
		f.After = schemaref.WithLoads(f.After, sv.moved, loads)
	}
	res := p.check(ctx, f, sc, sv.caller, sv.names)
	if res.Refused() {
		return fmt.Errorf("prepare saves nothing more: the save gate refuses %s %s: %s: %w", f.Kind, f.Name, res.Refusal, ErrNotReady)
	}
	next := *sc
	res.Apply(&next)
	if err := p.Scripts.UpdateWithVersion(ctx, &next, sv.author); err != nil {
		return fmt.Errorf("saving %s %s: %w", f.Kind, f.Name, err)
	}
	f.Saved = next.Version
	return nil
}

// saveMigrations replaces each pending migration the plan changes.
func (p *Preparer) saveMigrations(ctx context.Context, a *application.Application, plan *Plan, author script.Author) error {
	if p.Migrations == nil {
		return nil
	}
	ms, err := p.Migrations.List(ctx, a.Connection, a.Schema)
	if err != nil {
		return fmt.Errorf("reading the schema's migrations: %w", err)
	}
	for i := range plan.Files {
		f := &plan.Files[i]
		if f.Kind != KindMigration {
			continue
		}
		at := slices.IndexFunc(ms, func(m postgreskit.Migration) bool { return m.Version == f.Version })
		if at < 0 {
			continue
		}
		m := ms[at]
		m.Up = f.After
		m.Down, _ = migrationText(m.Down, a.Schema)
		m.Checksum, m.Author = postgreskit.ChecksumOf(m.Up, m.Down, m.Transactional), author.Email
		if _, err := p.Migrations.Put(ctx, m); err != nil {
			return fmt.Errorf("saving migration %d: %w", m.Version, err)
		}
		f.Saved = m.Version
	}
	return nil
}

// rewriteRecordings stores again, with the statements rewritten as f's source
// was, each recording its saved version's tests name and its recent runs the
// save gate replays, so a later save replays them against the prepared
// source. A recording nothing in changes is left as it was.
func (p *Preparer) rewriteRecordings(ctx context.Context, f *File, names []string) error {
	if p.Recordings == nil {
		return nil
	}
	ids, err := p.replayed(ctx, f)
	if err != nil {
		return err
	}
	for id := range ids {
		stored, err := p.Recordings.Get(ctx, id)
		if errors.Is(err, scriptrec.ErrNotFound) {
			continue
		}
		if err != nil {
			return fmt.Errorf("reading recording %s: %w", id, err)
		}
		data, err := scriptrec.Renamed(stored.Data, rewrite(names))
		if err != nil || scriptrec.Same(data, stored.Data) {
			continue
		}
		stored.Data = data
		if err := p.Recordings.Save(ctx, *stored); err != nil {
			return fmt.Errorf("storing recording %s: %w", id, err)
		}
	}
	return nil
}

// replayed is the run ids a save of f replays: the ones its tests name and
// its recent runs.
func (p *Preparer) replayed(ctx context.Context, f *File) (map[string]bool, error) {
	ids := map[string]bool{}
	for _, id := range scripttest.RecordingsNamed(f.After) {
		ids[id] = true
	}
	recent, err := p.Recordings.Recent(ctx, f.ID, scriptsave.RecentRuns)
	if err != nil {
		return nil, fmt.Errorf("reading %s's recorded runs: %w", f.Name, err)
	}
	for _, r := range recent {
		ids[r.RunID] = true
	}
	return ids, nil
}
