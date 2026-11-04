package appdbdrift

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// AdoptKit is the part of the postgres toolkit an adoption uses.
type AdoptKit interface {
	Kit
	Reader
	Apply(ctx context.Context, o postgres.ApplyOptions, migrations []postgres.Migration) ([]postgres.Migration, error)
}

// Store keeps the platform's migrations and their history.
type Store interface {
	Migrations
	Put(ctx context.Context, m postgres.Migration) (bool, error)
	Record(ctx context.Context, e postgres.MigrationEvent) error
}

// AdoptResult is what an adoption did, or why it did nothing.
type AdoptResult struct {
	Connection string `json:"connection"`
	Schema     string `json:"schema"`
	// Adopted is true when the baseline was saved and marked applied.
	Adopted bool `json:"adopted"`
	// Version is the baseline's version, 1.
	Version  int    `json:"version"`
	Checksum string `json:"checksum,omitempty"`
	// Up is the baseline's SQL.
	Up string `json:"up,omitempty"`
	// Unsupported are the objects the baseline cannot write; Differences
	// what the baseline, built from zero, still differs in. Either refuses.
	Unsupported []Unsupported `json:"unsupported"`
	Differences []Difference  `json:"differences"`
}

// Target is the schema an adoption reads and who asked for it.
type Target struct {
	Connection, Schema string
	// Actor is recorded as the baseline's author and in its history.
	Actor string
}

// BaselineVersion is the version an adopted baseline takes.
const BaselineVersion = 1

// baselineName is the adopted baseline's name.
const baselineName = "baseline adopted from the existing schema"

// ErrManaged refuses to adopt a schema the platform already keeps migrations
// for, or whose ledger records any.
var ErrManaged = errors.New("the schema is already managed by migrations; adopting is for a schema the platform keeps none for")

// Adopt writes schema as a baseline migration and marks it applied, so a
// database created before the platform knew it is managed from then on. The
// baseline is first applied from zero on a throwaway database and compared
// with the live schema; it is saved only when nothing differs.
func Adopt(ctx context.Context, kit AdoptKit, store Store, target Target) (*AdoptResult, error) {
	connection, schema, actor := target.Connection, target.Schema, target.Actor
	res := &AdoptResult{
		Connection: connection, Schema: schema, Version: BaselineVersion,
		Unsupported: []Unsupported{}, Differences: []Difference{},
	}
	existing, err := store.List(ctx, connection, schema)
	if err != nil {
		return nil, fmt.Errorf("reading the migrations: %w", err)
	}
	st, err := kit.Status(ctx, connection, schema, existing)
	if err != nil {
		return nil, err //nolint:wrapcheck // the toolkit's value-free summary is the text
	}
	if len(existing) > 0 || len(st.Applied) > 0 {
		return nil, ErrManaged
	}
	up, unsupported, err := Render(ctx, kit, connection, schema)
	if err != nil {
		return nil, err
	}
	if len(unsupported) > 0 {
		res.Unsupported = unsupported
		return res, nil
	}
	baseline := postgres.Migration{
		Connection: connection, Schema: schema, Version: BaselineVersion, Name: baselineName, Up: up,
		Transactional: true, Checksum: postgres.ChecksumOf(up, "", true), Author: actor,
	}
	res.Up, res.Checksum = up, baseline.Checksum
	built, err := kit.DescribeBuilt(ctx, connection, schema, []postgres.Migration{baseline})
	if err != nil {
		return nil, fmt.Errorf("applying the baseline from zero: %w", err)
	}
	live, err := kit.Describe(ctx, connection, schema)
	if err != nil {
		return nil, err //nolint:wrapcheck // the toolkit's value-free summary is the text
	}
	if res.Differences = Compare(live, built); len(res.Differences) > 0 {
		return res, nil
	}
	return save(ctx, kit, store, baseline, res)
}

// save stores the baseline, writes its ledger row without running it, and
// records both in its history.
func save(ctx context.Context, kit AdoptKit, store Store, baseline postgres.Migration, res *AdoptResult) (*AdoptResult, error) {
	actor := baseline.Author
	if _, err := store.Put(ctx, baseline); err != nil {
		return nil, fmt.Errorf("saving the baseline: %w", err)
	}
	event := func(name, detail string) {
		_ = store.Record(ctx, postgres.MigrationEvent{
			Connection: baseline.Connection, Schema: baseline.Schema, Version: baseline.Version, Event: name,
			Actor: actor, Checksum: baseline.Checksum, Detail: detail, At: time.Now().UTC(),
		})
	}
	event(postgres.EventAdded, "adopted from the existing schema")
	if _, err := kit.Apply(ctx, postgres.ApplyOptions{
		Connection: baseline.Connection, Schema: baseline.Schema, Actor: actor, MarkOnly: true,
	}, []postgres.Migration{baseline}); err != nil {
		return nil, fmt.Errorf("marking the baseline applied: %w", err)
	}
	event(postgres.EventApplied, "marked applied: the schema already held it")
	res.Adopted = true
	return res, nil
}
