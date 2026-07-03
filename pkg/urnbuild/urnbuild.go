// Package urnbuild constructs DataHub dataset URNs from query-engine table
// components. It is shared by the platform's resource templates and the
// reflexive-capture wiring so the URN convention (and its catalog mapping) has a
// single implementation.
package urnbuild

import "fmt"

// defaultPlatform is the platform name used when none is configured.
const defaultPlatform = "trino"

// DatasetURN builds a DataHub dataset URN of the form
// urn:li:dataset:(urn:li:dataPlatform:<platform>,<catalog>.<schema>.<table>,PROD).
// An empty platform defaults to "trino", and catalogMapping (query-engine
// catalog to metadata catalog) is applied when it carries the catalog.
func DatasetURN(platform string, catalogMapping map[string]string, catalog, schema, table string) string {
	if platform == "" {
		platform = defaultPlatform
	}
	if mapped, ok := catalogMapping[catalog]; ok {
		catalog = mapped
	}
	return fmt.Sprintf("urn:li:dataset:(urn:li:dataPlatform:%s,%s.%s.%s,PROD)",
		platform, catalog, schema, table)
}
