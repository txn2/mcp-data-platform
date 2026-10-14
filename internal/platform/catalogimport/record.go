// Package catalogimport is the one-time import of a deployment's DataHub into
// the platform catalog, and the rewrite of the urn:li: references platform
// tables hold into the catalog's mcp: references (#1923).
//
// The import reads every dataset DataHub holds, the records it has marked
// removed among them, and every glossary node, glossary term, tag, domain and
// data product. A dataset's curation lands on the catalog dataset with the
// same connection, catalog, schema and table, which a scan must have
// cataloged first. Curation already written in the platform catalog by
// anyone else is kept: the import fills, it does not overwrite. Running it
// again writes only what changed in DataHub since.
package catalogimport

import (
	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

// Principal is who the import's catalog writes are recorded as.
const Principal = "datahub-import"

// DatasetHead is a dataset DataHub lists: its URN and whether DataHub has
// marked it removed.
type DatasetHead struct {
	URN     string
	Removed bool
}

// DatasetRecord is one DataHub dataset's curation in the catalog's shapes.
// Glossary terms and the domain are still DataHub URNs; the import maps them.
type DatasetRecord struct {
	URN     string
	Removed bool
	Aspects catalog.Aspects
	// Columns are the curation of each top-level field, by field name.
	Columns map[string]catalog.Aspects
	// Upstreams are the URNs of the datasets DataHub records this one is
	// derived from.
	Upstreams []string
}

// Definition is a DataHub glossary node, glossary term, tag, domain or data
// product.
type Definition struct {
	URN  string
	Kind catalog.Kind
	Name string
	// Parent is the URN of the glossary node a term or node sits under.
	Parent  string
	Aspects catalog.Aspects
}
