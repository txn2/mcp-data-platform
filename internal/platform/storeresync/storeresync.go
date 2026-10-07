// Package storeresync makes a replica's live connections and personas match
// what the database stores. It is how the platform loads its database
// personas at startup, and how a replica catches up when its reload channel's
// LISTEN connection comes back: a notification sent while that connection was
// down is lost and never repeated, so the replica re-reads what the channel
// carries, as a restart would (#1902).
package storeresync

import (
	"context"
	"log/slog"

	"github.com/txn2/mcp-data-platform/internal/platform/personacfg"
	"github.com/txn2/mcp-data-platform/pkg/connid"
	"github.com/txn2/mcp-data-platform/pkg/connreconcile"
	"github.com/txn2/mcp-data-platform/pkg/persona"
)

// slog keys the package logs under.
const (
	logKeyKind  = "kind"
	logKeyName  = "name"
	logKeyError = "error"
)

// StoredConnection is one row of the connection store.
type StoredConnection struct {
	Kind, Name string
	Config     map[string]any
}

// Connections makes the live toolkits match stored. Every stored connection
// is adopted as a peer's upsert would adopt it, and a live connection the
// store no longer holds is removed as a peer's delete would remove it. The
// connections the configuration file declares are left as they are, which is
// what a restart does with them, and so are those builtin reports the platform
// registers itself: neither the file nor the store holds one, so its absence
// from the store is not a deletion.
func Connections(stored []StoredConnection, toolkits connreconcile.ToolkitSource, declared connid.Declarer, builtin func(kind, name string) bool) {
	held := adopt(stored, toolkits, declared)
	if toolkits == nil {
		return
	}
	rec := connreconcile.New(toolkits)
	for _, c := range connid.NewResolver(toolkits.All(), declared).All("") {
		name := string(c.Instance)
		if c.FileDeclared || held[[2]string{c.Kind, name}] || builtin(c.Kind, name) {
			continue
		}
		for _, f := range rec.Remove(c.Kind, name) {
			slog.Warn("reload-bus: failed to remove a connection the store no longer holds",
				logKeyKind, c.Kind, logKeyName, name, logKeyError, f.Err)
		}
	}
}

// adopt applies every stored connection the file does not declare and
// reports every stored (kind, name).
func adopt(stored []StoredConnection, toolkits connreconcile.ToolkitSource, declared connid.Declarer) map[[2]string]bool {
	rec := connreconcile.New(toolkits)
	held := make(map[[2]string]bool, len(stored))
	for _, s := range stored {
		held[[2]string{s.Kind, s.Name}] = true
		if declared != nil && declared.DeclaresConnection(s.Kind, s.Name) {
			continue
		}
		for _, f := range rec.Adopt(s.Kind, s.Name, s.Config) {
			slog.Error("reload-bus: failed to reconcile connection onto toolkit",
				logKeyKind, s.Kind, logKeyName, s.Name, "phase", f.Phase.String(), logKeyError, f.Err)
		}
	}
	return held
}

// Sources are the Persona.Source values a registered persona carries: from
// the file alone, from the database alone, or from the database over the file.
type Sources struct{ File, Database, Both string }

// PersonaLister lists the stored persona definitions. The persona store
// satisfies it with D its definition type, which this package does not import
// (internal/platform seams do not depend back on pkg/platform).
type PersonaLister[D any] interface {
	List(ctx context.Context) ([]D, error)
}

// Personas makes the persona registry match the database. Every stored
// definition is registered, overriding a file persona of the same name. A
// registered persona that came from the database and is no longer stored was
// deleted on another replica: it reverts to the file's definition when the
// file has one and is removed otherwise, as the replica that deleted it did
// (#1902). A store that cannot be read changes nothing.
func Personas[D any, PD interface {
	*D
	ToPersona() *persona.Persona
}](ctx context.Context, store PersonaLister[D], reg *persona.Registry, file map[string]personacfg.PersonaDef, src Sources) {
	if store == nil || reg == nil {
		return
	}
	defs, err := store.List(ctx)
	if err != nil {
		slog.WarnContext(ctx, "failed to load DB personas", logKeyError, err)
		return
	}
	stored := make([]*persona.Persona, 0, len(defs))
	for i := range defs {
		stored = append(stored, PD(&defs[i]).ToPersona())
	}
	dropDeleted(reg, stored, file, src)
	for _, per := range stored {
		per.Source = src.Database
		if _, ok := file[per.Name]; ok {
			per.Source = src.Both
		}
		if err := reg.Register(per); err != nil {
			slog.WarnContext(ctx, "failed to load DB persona", logKeyName, per.Name, logKeyError, err)
		}
	}
	if len(stored) > 0 {
		slog.InfoContext(ctx, "loaded DB persona overrides", "count", len(stored))
	}
}

// dropDeleted reverts or removes each registered persona that came from the
// database and is not among the stored ones.
func dropDeleted(reg *persona.Registry, stored []*persona.Persona, file map[string]personacfg.PersonaDef, src Sources) {
	names := make(map[string]bool, len(stored))
	for _, per := range stored {
		names[per.Name] = true
	}
	for _, per := range reg.All() {
		if names[per.Name] || (per.Source != src.Database && per.Source != src.Both) {
			continue
		}
		if fileDef, ok := file[per.Name]; ok {
			if err := reg.Register(fileDef.ToPersona(per.Name, src.File)); err != nil {
				slog.Warn("failed to revert deleted DB persona to its file definition", logKeyName, per.Name, logKeyError, err)
			}
			continue
		}
		if err := reg.Unregister(per.Name); err != nil {
			slog.Warn("failed to remove deleted DB persona", logKeyName, per.Name, logKeyError, err)
		}
	}
}
