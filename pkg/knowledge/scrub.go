package knowledge

import (
	"github.com/txn2/mcp-data-platform/pkg/catalog"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// maxCurationText bounds one scrubbed curation value. It is far above any
// description or note, so scrubbing never shortens a context document a fetch
// returns whole; the semantic layer's own, shorter cap is for enrichment.
const maxCurationText = 1 << 20

// curationSanitizer is the semantic layer's sanitizer with the cap above.
// Curation is written by people and agents and reaches a model in search and
// fetch results, so every value it carries passes through it on the way out.
var curationSanitizer = semantic.NewSanitizer(semantic.SanitizeConfig{
	MaxLength:              maxCurationText,
	StripInjectionPatterns: true,
	LogInjectionAttempts:   true,
})

// scrub removes control characters and known instruction patterns from one
// curation value.
func scrub(s string) string {
	return curationSanitizer.SanitizeString(s)
}

// scrubDataset is a copy of a dataset with its curation, and each column's,
// scrubbed.
func scrubDataset(d *catalog.Dataset) *catalog.Dataset {
	out := *d
	out.Aspects = d.Aspects.WithText(scrub)
	out.Columns = make([]catalog.Column, len(d.Columns))
	for i, c := range d.Columns {
		c.Aspects = c.Aspects.WithText(scrub)
		out.Columns[i] = c
	}
	return &out
}
