package compactor

import (
	"context"
	"errors"
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
)

// The view every source is read through is replaced once by each process
// (#2140). A source created by an earlier release keeps the view that release
// wrote until something rewrites it, and the view is where a reader's
// duplicates are removed, so a release that changes it rewrites every
// existing one. A source created since has the current view already; writing
// it again is a no-op the statement allows.

// viewsOwed reports whether a source's view has not been made current yet by
// this process.
func (w *Worker) viewsOwed() bool {
	return !w.viewsCurrent
}

// ReplaceViews rewrites the view of every source this process has not
// rewritten yet. A source that fails is logged, does not stop the others, and
// is tried again on the next pass; the error returned joins the failures.
func (w *Worker) ReplaceViews(ctx context.Context) error {
	sources, err := w.deps.Sources.List(ctx)
	if err != nil {
		w.warn(ctx, "listing sources to replace their views", "", err)
		return fmt.Errorf("webhooks: listing sources to replace their views: %w", err)
	}
	var errs []error
	for _, src := range sources {
		if w.viewed[src.Name] {
			continue
		}
		if err := w.replaceView(ctx, src.WithDefaults()); err != nil {
			w.warn(ctx, "replacing the view", src.Name, err)
			errs = append(errs, err)
			continue
		}
		w.viewed[src.Name] = true
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("webhooks: replacing views: %w", err)
	}
	w.viewsCurrent = true
	return nil
}

// replaceView rewrites one source's view on its connection.
func (w *Worker) replaceView(ctx context.Context, src whsource.Source) error {
	tg, err := w.deps.Tables.TargetFor(src.Connection)
	if err != nil {
		return fmt.Errorf("resolving the connection of source %s: %w", src.Name, err)
	}
	return w.deps.Tables.ReplaceView(ctx, tg, src) //nolint:wrapcheck // the table names the source
}
