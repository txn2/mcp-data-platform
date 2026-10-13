// Package catalogscan catalogs what the platform's own connections can see
// (#1918): every Trino table and view with its columns, every stored API
// operation and every GraphQL operation, written to the platform catalog so no
// external ingestion is needed. A scan never deletes: a dataset a connection no
// longer shows is marked removed and its curation stays readable.
package catalogscan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

// Connection is one connection a source scans: the instance name a catalog
// reference carries and the name a call binds it by.
type Connection struct {
	Instance string
	Bound    string
}

// Outcome is what one connection's scan did.
type Outcome struct {
	ConnectionKind string    `json:"connection_kind"`
	Connection     string    `json:"connection"`
	StartedAt      time.Time `json:"started_at"`
	FinishedAt     time.Time `json:"finished_at"`
	Datasets       int       `json:"datasets"`
	Removed        int       `json:"removed"`
	// Error is why the scan of this connection stopped. A failed scan marks
	// nothing removed: what it did not read is not evidence of absence.
	Error string `json:"error,omitempty"`
	// Unread are the parts of the connection the scan could not read, such
	// as a Trino catalog whose connector is down. The rest is cataloged, and
	// nothing under an unread part is marked removed.
	Unread []Unread `json:"unread"`
}

// Scanner runs scans.
type Scanner struct {
	sources  []Source
	store    Writer
	settings SettingsReader
	outcomes Recorder
	now      func() time.Time
}

// New builds a scanner over the given sources.
func New(store Writer, settings SettingsReader, outcomes Recorder, sources ...Source) *Scanner {
	return &Scanner{sources: sources, store: store, settings: settings, outcomes: outcomes, now: time.Now}
}

// scanConcurrency is how many connections a scan reads at once. A connection
// whose upstream does not answer holds one slot until its client gives up,
// not the whole scan.
const scanConcurrency = 4

// job is one connection of one source.
type job struct {
	src Source
	c   Connection
}

// Scan scans every connection of every source and returns what each did, in
// source and connection order. Connections are read concurrently, and one
// connection failing does not stop the others.
func (s *Scanner) Scan(ctx context.Context) ([]Outcome, error) {
	set, err := s.settings.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading catalog settings: %w", err)
	}
	jobs := make([]job, 0)
	for _, src := range s.sources {
		for _, c := range src.Connections() {
			jobs = append(jobs, job{src: src, c: c})
		}
	}
	out := make([]Outcome, len(jobs))
	var g errgroup.Group
	g.SetLimit(scanConcurrency)
	for i, j := range jobs {
		g.Go(func() error {
			o := s.scanConnection(ctx, j.src, j.c, set.ExclusionsFor(j.src.Kind(), j.c.Instance))
			if err := s.outcomes.RecordOutcome(ctx, o); err != nil {
				slog.WarnContext(ctx, "catalog scan: recording outcome failed", "connection", logsan.SanitizeForLog(j.c.Instance), "error", logsan.SanitizeForLog(err.Error()))
			}
			out[i] = o
			return nil
		})
	}
	_ = g.Wait() // every job returns nil; failures are on each outcome
	return out, nil
}

// scanConnection writes what one connection sees and marks the rest removed.
func (s *Scanner) scanConnection(ctx context.Context, src Source, c Connection, skip Exclusions) Outcome {
	o := Outcome{ConnectionKind: src.Kind(), Connection: c.Instance, StartedAt: s.now(), Unread: make([]Unread, 0)}
	keep := make([]string, 0)
	err := src.Datasets(ctx, c, skip, func(in catalog.DatasetInput) error {
		if _, err := s.store.PutDataset(ctx, in); err != nil {
			return fmt.Errorf("writing %s: %w", in.Ref, err)
		}
		keep = append(keep, in.Ref.String())
		o.Datasets++
		return nil
	})
	unread := make([]string, 0)
	var partial *UnreadError
	if errors.As(err, &partial) {
		o.Unread = partial.Parts
		for _, p := range partial.Parts {
			unread = append(unread, p.Path)
		}
		slog.WarnContext(ctx, "catalog scan could not read part of a connection", "kind", src.Kind(),
			"connection", logsan.SanitizeForLog(c.Instance), "error", logsan.SanitizeForLog(partial.Error()))
		err = nil
	}
	if err == nil {
		o.Removed, err = s.store.MarkRemoved(ctx, catalog.Removal{
			ConnectionKind: src.Kind(), Connection: c.Instance, Types: src.Types(), Keep: keep, Unread: unread,
		})
	}
	if err != nil {
		o.Error = err.Error()
		slog.WarnContext(ctx, "catalog scan failed", "kind", src.Kind(), "connection", logsan.SanitizeForLog(c.Instance), "error", logsan.SanitizeForLog(o.Error))
	}
	o.FinishedAt = s.now()
	return o
}

// Exclusions are the dataset paths an operator excluded on one connection,
// each a dotted prefix: "hive" excludes a Trino catalog, "hive.tmp" one
// schema in it, "mutation" every GraphQL mutation.
type Exclusions [][]string

// ParseExclusions reads dotted entries into prefixes, ignoring blank ones.
func ParseExclusions(entries []string) Exclusions {
	out := make(Exclusions, 0, len(entries))
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		out = append(out, strings.Split(e, "."))
	}
	return out
}

// Excludes reports whether a dataset path starts with any excluded prefix.
func (x Exclusions) Excludes(path ...string) bool {
	for _, prefix := range x {
		if hasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// hasPrefix reports whether path begins with prefix, segment by segment.
func hasPrefix(path, prefix []string) bool {
	if len(prefix) > len(path) {
		return false
	}
	for i, p := range prefix {
		if path[i] != p {
			return false
		}
	}
	return true
}
