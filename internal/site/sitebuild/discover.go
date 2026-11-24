package sitebuild

import (
	"context"
	"fmt"
	"io/fs"
	"slices"

	"github.com/evanw/esbuild/pkg/api"
)

// Specifiers walks every entry's module graph through the tree and reports
// each bare specifier it imports that the control file does not map to a
// folder of the site: what an imports map has to cover (#2785). Nothing is
// fetched and nothing is written.
func Specifiers(ctx context.Context, src fs.FS, cfg *Config) ([]string, error) {
	found := map[string]bool{}
	for _, entry := range cfg.Entries {
		c := &compile{
			ctx: ctx, src: src, cfg: cfg, entry: entry, limits: Limits{}.withDefaults(),
			inputs: map[string]bool{}, bare: found,
		}
		opts := c.options()
		opts.Metafile, opts.Sourcemap = false, api.SourceMapNone
		result := api.Build(opts)
		if c.stop != nil {
			return nil, c.stop
		}
		if len(result.Errors) > 0 {
			return nil, fmt.Errorf("reading what %s imports: %w", entry, &Error{Messages: messages(result.Errors)})
		}
	}
	out := make([]string, 0, len(found))
	for spec := range found {
		out = append(out, spec)
	}
	slices.Sort(out)
	return out, nil
}
