package producedview

import (
	"context"
	"fmt"

	"golang.org/x/sync/errgroup"
)

// Opener answers whether one reader may open a file a script produced. Each
// surface brings its own: the portal's share graph for a person in the
// portal, the same rules fetch applies for a caller over MCP.
type Opener interface {
	CanOpen(ctx context.Context, it Item) bool
}

// Produced is what one script produced as one reader may see it, among the
// limit files it wrote most recently.
type Produced struct {
	// Open are the files the reader can open, named.
	Open []Item
	// Hidden counts the others, which are not named.
	Hidden int
	// More is true when the script wrote more files than limit, which are
	// neither listed nor counted.
	More bool
}

// ProducedFor lists what one script produced as one reader may see it
// (#2027): the files the reader can open, named, and how many others there
// are, counted and not named, the rule the reference Used-by panels follow
// (#1475). A file since deleted is neither listed nor counted. The script's
// definition is everyone's to read; what its runs wrote is not, so a reader
// never learns the name of an asset that was not shared with them.
func (r *Reader) ProducedFor(ctx context.Context, scriptID string, limit int, o Opener) (Produced, error) {
	items, err := r.Produced(ctx, scriptID, limit+1)
	if err != nil {
		return Produced{}, fmt.Errorf("listing what script %s produced for a reader: %w", scriptID, err)
	}
	out := Produced{Open: make([]Item, 0, len(items))}
	if len(items) > limit {
		items, out.More = items[:limit], true
	}
	opens := make([]bool, len(items))
	if o != nil {
		// Each check is its own read, so they run together, bounded as the
		// name resolution above is.
		g, gctx := errgroup.WithContext(ctx)
		g.SetLimit(resolveConcurrency)
		for i, it := range items {
			if it.Deleted {
				continue
			}
			g.Go(func() error {
				opens[i] = o.CanOpen(gctx, it)
				return nil
			})
		}
		_ = g.Wait() // CanOpen answers false on a failed read; none returns an error
	}
	for i, it := range items {
		switch {
		case it.Deleted:
		case opens[i]:
			out.Open = append(out.Open, it)
		default:
			out.Hidden++
		}
	}
	return out, nil
}
