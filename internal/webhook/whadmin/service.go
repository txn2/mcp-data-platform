// Package whadmin is what an administrator does to a webhook source (#1870):
// create one, change it, rotate its secret, read its status, and delete it.
//
// Creating a source is more than writing its record. Its tables and view are
// created on the connection it names, and the connection is proved able to
// hold them -- its catalog reads the managed-resources bucket and allows
// register_partition -- before the source is stored, so a source the platform
// accepts is one whose events can be queried. The table is recorded as a
// registration, which is how the table listing and every surface reading
// registrations sees it.
package whadmin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/platform/tableregister"
	"github.com/txn2/mcp-data-platform/internal/webhook/whevent"
	"github.com/txn2/mcp-data-platform/internal/webhook/whlayout"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
	"github.com/txn2/mcp-data-platform/internal/webhook/whtable"
)

// Refusals a request can meet, beyond whsource.ErrInvalid and the table
// package's connection refusals.
var (
	ErrNotFound  = whsource.ErrNotFound
	ErrExists    = whsource.ErrExists
	ErrNameTaken = errors.New("a table with this source's name is already registered on that connection")
	// ErrUnusable wraps the reason a connection could not hold the source's
	// tables, as the query engine gave it.
	ErrUnusable = errors.New("the connection cannot hold this source's table")
)

// Deps are what the service acts through.
type Deps struct {
	Sources       Sources
	Tables        Tables
	Registrations Registrations
	Windows       Windows
	Objects       Objects
	Resources     Resources
	Bucket        string
	// Changed is called after every write, so this replica's receiver
	// serves the change at once rather than at its next refresh.
	Changed func()
	// PersonaExists reports whether a persona is defined, so a source cannot
	// name one nobody belongs to. Nil accepts any name.
	PersonaExists func(name string) bool
	NewID         func() (string, error)
	Logger        *slog.Logger
	Now           func() time.Time
}

// Service manages sources.
type Service struct {
	deps Deps
}

// New builds the service.
func New(d Deps) *Service {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Changed == nil {
		d.Changed = func() {}
	}
	return &Service{deps: d}
}

// List returns every source.
func (s *Service) List(ctx context.Context) ([]whsource.Source, error) {
	list, err := s.deps.Sources.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing webhook sources: %w", err)
	}
	for i := range list {
		list[i] = list[i].WithDefaults()
	}
	return list, nil
}

// Get returns one source with its status.
func (s *Service) Get(ctx context.Context, name string) (whsource.Source, whstore.Status, error) {
	src, err := s.deps.Sources.Get(ctx, name)
	if err != nil {
		return whsource.Source{}, whstore.Status{}, err //nolint:wrapcheck // ErrNotFound is what a surface renders
	}
	st, err := s.deps.Windows.Status(ctx, name, s.deps.Now())
	if err != nil {
		return whsource.Source{}, whstore.Status{}, fmt.Errorf("reading webhook source status: %w", err)
	}
	return src.WithDefaults(), st, nil
}

// Create makes a source: its tables, the proof its connection can hold them,
// its record and its registration. A failure part-way removes what was made.
func (s *Service) Create(ctx context.Context, src whsource.Source) (whsource.Source, error) {
	src = src.WithDefaults()
	tg, err := s.admit(ctx, src)
	if err != nil {
		return whsource.Source{}, err
	}
	if err := s.build(ctx, tg, src); err != nil {
		return whsource.Source{}, err
	}
	s.deps.Changed()
	stored, err := s.deps.Sources.Get(ctx, src.Name)
	if err != nil {
		return src, nil //nolint:nilerr // the source exists; only the re-read for its timestamps failed
	}
	return stored.WithDefaults(), nil
}

// admit refuses a source before anything is made for it: settings that do
// not validate, a name taken by a source or by a table, a persona nobody
// belongs to, a connection that cannot hold a table.
func (s *Service) admit(ctx context.Context, src whsource.Source) (whtable.Target, error) {
	if err := whsource.Validate(src); err != nil {
		return whtable.Target{}, err //nolint:wrapcheck // ErrInvalid is what a surface renders
	}
	if _, err := s.deps.Sources.Get(ctx, src.Name); err == nil {
		return whtable.Target{}, ErrExists
	} else if !errors.Is(err, whsource.ErrNotFound) {
		return whtable.Target{}, fmt.Errorf("reading webhook source: %w", err)
	}
	if p := src.Config.Persona; p != "" && s.deps.PersonaExists != nil && !s.deps.PersonaExists(p) {
		return whtable.Target{}, whsource.Refusal("config.persona names no persona: %s", p) //nolint:wrapcheck // ErrInvalid is what a surface renders
	}
	tg, err := s.deps.Tables.TargetFor(src.Connection)
	if err != nil {
		return whtable.Target{}, err //nolint:wrapcheck // the connection refusal is what a surface renders
	}
	holder, err := s.deps.Registrations.ByName(ctx, tg.Connection, tg.Catalog, tg.Schema, src.TableName())
	if err != nil {
		return whtable.Target{}, fmt.Errorf("checking the table name: %w", err)
	}
	if holder != nil {
		return whtable.Target{}, fmt.Errorf("table %s is registered by %s: %w", holder.QualifiedName(), holder.RegisteredBy, ErrNameTaken)
	}
	return tg, nil
}

// build makes the source's tables, proves the connection can hold them, and
// stores the source and its registration, removing what it made when a step
// fails.
func (s *Service) build(ctx context.Context, tg whtable.Target, src whsource.Source) error {
	if err := s.deps.Tables.Create(ctx, tg, src); err != nil {
		return unusable{err}
	}
	if err := s.deps.Tables.Probe(ctx, tg, src); err != nil {
		s.dropTables(ctx, tg, src)
		return unusable{err}
	}
	if err := s.deps.Sources.Create(ctx, src); err != nil {
		s.dropTables(ctx, tg, src)
		return err //nolint:wrapcheck // ErrExists is what a surface renders
	}
	if err := s.register(ctx, tg, src); err != nil {
		s.dropTables(ctx, tg, src)
		_ = s.deps.Sources.Delete(ctx, src.Name)
		return err
	}
	return nil
}

// unusable is the query engine's reason a connection cannot hold a source's
// table. It is ErrUnusable to errors.Is, and carries the engine's words.
type unusable struct{ err error }

func (u unusable) Error() string {
	return "the connection cannot hold this source's table: " + u.err.Error()
}

func (u unusable) Unwrap() []error { return []error{ErrUnusable, u.err} }

// register records the source's view as a registration.
func (s *Service) register(ctx context.Context, tg whtable.Target, src whsource.Source) error {
	id, err := s.deps.NewID()
	if err != nil {
		return fmt.Errorf("generating the registration id: %w", err)
	}
	cols := make([]tableregister.Column, 0, len(whevent.Columns))
	for _, c := range append(append([]string{}, whevent.Columns...), whtable.PartitionColumns...) {
		typ := "varchar"
		if c == "received_at" || c == "landed_at" {
			typ = "timestamp(6)"
		}
		cols = append(cols, tableregister.Column{Name: c, Type: typ})
	}
	if err := s.deps.Registrations.Insert(ctx, tableregister.Registration{
		ID: id, SourceKind: tableregister.KindWebhook, SourceID: src.Name,
		Connection: tg.Connection, Catalog: tg.Catalog, Schema: tg.Schema, Table: src.TableName(),
		Location: s.deps.Tables.S3Location(whlayout.SourcePrefix(src.Name)), Columns: cols,
		RegisteredBy: src.CreatedBy, Format: tableregister.FormatParquet,
	}); err != nil {
		return fmt.Errorf("recording the source's table: %w", err)
	}
	return nil
}

// Update is what an administrator can change on a source.
type Update struct {
	Enabled *bool
	Auth    whsource.Auth
	Config  whsource.Config
	// RotationOverlap keeps the previous secret valid this long when Auth
	// carries a new one. Zero ends it at once.
	RotationOverlap time.Duration
}

// Update changes a source's settings. The name, the connection and the persona
// stay: the table was created on that connection under a name derived from the
// source's, and the windows already written are in that persona's library.
// An empty secret keeps the stored one, so a form never has to carry it.
func (s *Service) Update(ctx context.Context, name string, u Update) (whsource.Source, error) {
	cur, err := s.deps.Sources.Get(ctx, name)
	if err != nil {
		return whsource.Source{}, err //nolint:wrapcheck // ErrNotFound is what a surface renders
	}
	next := cur
	if u.Enabled != nil {
		next.Enabled = *u.Enabled
	}
	auth := u.Auth
	auth.Secret, auth.PreviousSecret, auth.PreviousUntil = cur.Auth.Secret, cur.Auth.PreviousSecret, cur.Auth.PreviousUntil
	next.Auth = auth.Rotate(u.Auth.Secret, u.RotationOverlap, s.deps.Now())
	next.Config = u.Config
	next.Config.Persona = cur.Config.Persona
	next = next.WithDefaults()
	if err := whsource.Validate(next); err != nil {
		return whsource.Source{}, err //nolint:wrapcheck // ErrInvalid is what a surface renders
	}
	if err := s.deps.Sources.Update(ctx, next); err != nil {
		return whsource.Source{}, err //nolint:wrapcheck // ErrNotFound is what a surface renders
	}
	s.deps.Changed()
	return next, nil
}

// Delete removes a source and everything it wrote: its tables, its windows'
// resources, its raw segments, its registration and its record.
func (s *Service) Delete(ctx context.Context, name string) error {
	src, err := s.deps.Sources.Get(ctx, name)
	if err != nil {
		return err //nolint:wrapcheck // ErrNotFound is what a surface renders
	}
	if tg, err := s.deps.Tables.TargetFor(src.Connection); err == nil {
		s.dropTables(ctx, tg, src)
	} else {
		s.warn("the source's connection is gone; its tables were not dropped", name, err)
	}
	for _, step := range []func(context.Context, string) error{s.deleteWindows, s.deleteSegments, s.deleteRegistrations} {
		if err := step(ctx, name); err != nil {
			return err
		}
	}
	if err := s.deps.Sources.Delete(ctx, name); err != nil {
		return err //nolint:wrapcheck // ErrNotFound is what a surface renders
	}
	s.deps.Changed()
	return nil
}

// deleteWindows deletes the resource every compacted window of a source is.
func (s *Service) deleteWindows(ctx context.Context, name string) error {
	ids, err := s.deps.Windows.ResourceIDs(ctx, name)
	if err != nil {
		return fmt.Errorf("listing the source's windows: %w", err)
	}
	for _, id := range ids {
		if err := s.deps.Resources.Delete(ctx, id); err != nil {
			return fmt.Errorf("deleting a window's resource: %w", err)
		}
	}
	return nil
}

// deleteSegments deletes every object the source wrote outside the resource
// store.
func (s *Service) deleteSegments(ctx context.Context, name string) error {
	keys, err := s.deps.Objects.ListKeys(ctx, s.deps.Bucket, whlayout.SourcePrefix(name))
	if err != nil {
		return fmt.Errorf("listing the source's segments: %w", err)
	}
	for _, key := range keys {
		if err := s.deps.Objects.DeleteObject(ctx, s.deps.Bucket, key); err != nil {
			return fmt.Errorf("deleting a segment: %w", err)
		}
	}
	return nil
}

// deleteRegistrations removes the record of the source's table.
func (s *Service) deleteRegistrations(ctx context.Context, name string) error {
	regs, err := s.deps.Registrations.BySource(ctx, tableregister.KindWebhook, name)
	if err != nil {
		return fmt.Errorf("reading the source's registration: %w", err)
	}
	for _, reg := range regs {
		if err := s.deps.Registrations.Delete(ctx, reg.ID); err != nil {
			return fmt.Errorf("removing the source's registration: %w", err)
		}
	}
	return nil
}

// dropTables drops a source's tables, logging rather than failing: the
// caller is already undoing or removing the source.
func (s *Service) dropTables(ctx context.Context, tg whtable.Target, src whsource.Source) {
	if err := s.deps.Tables.Drop(ctx, tg, src); err != nil {
		s.warn("dropping the source's tables", src.Name, err)
	}
}

func (s *Service) warn(msg, source string, err error) {
	s.deps.Logger.Warn("webhooks: "+msg, "source", logsan.SanitizeForLog(source),
		"error", logsan.SanitizeForLog(err.Error()))
}
