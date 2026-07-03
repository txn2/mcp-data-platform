// Package urnbuild is the single home of the DataHub dataset URN grammar
//
//	urn:li:dataset:(urn:li:dataPlatform:<platform>,<name>,<env>)
//
// It constructs URNs from query-engine table components and decomposes URNs
// back into platform, name, and environment. Adapters and middleware must
// build and parse through this package rather than hand-rolling the grammar,
// so edge cases (commas in dataset names, non-PROD environments) are handled
// in exactly one place. A repo-level gate (TestDatasetURNGrammarCentralized)
// fails on new grammar literals outside this package.
package urnbuild

import (
	"fmt"
	"strings"
)

// defaultPlatform is the platform name used when none is configured.
const defaultPlatform = "trino"

// datasetPrefix is the literal grammar prefix of a DataHub dataset URN.
const datasetPrefix = "urn:li:dataset:(urn:li:dataPlatform:"

// defaultEnv is the DataHub fabric/environment the platform mints URNs in.
const defaultEnv = "PROD"

// DatasetURN builds a DataHub dataset URN of the form
// urn:li:dataset:(urn:li:dataPlatform:<platform>,<catalog>.<schema>.<table>,PROD).
// An empty platform defaults to "trino", and catalogMapping (query-engine
// catalog to metadata catalog) is applied when it carries the catalog.
func DatasetURN(platform string, catalogMapping map[string]string, catalog, schema, table string) string {
	if mapped, ok := catalogMapping[catalog]; ok {
		catalog = mapped
	}
	return DatasetURNFromName(platform, catalog+"."+schema+"."+table)
}

// DatasetURNFromName builds a DataHub dataset URN from an already-joined
// dataset name. Callers own the name semantics (which components to join,
// catalog mapping); the grammar lives here. An empty platform defaults to
// "trino".
func DatasetURNFromName(platform, name string) string {
	if platform == "" {
		platform = defaultPlatform
	}
	return fmt.Sprintf("%s%s,%s,%s)", datasetPrefix, platform, name, defaultEnv)
}

// ParsedDataset is the decomposed form of a DataHub dataset URN.
type ParsedDataset struct {
	// Platform is the data platform segment (e.g. "trino", "s3").
	Platform string
	// Name is the dataset name segment, verbatim. For table-shaped
	// datasets this is <catalog>.<schema>.<table>; for object stores it
	// can be <bucket>/<prefix>. Names may legally contain commas.
	Name string
	// Env is the DataHub fabric/environment segment (e.g. "PROD").
	Env string
}

// ParseDatasetURN decomposes a DataHub dataset URN. The platform ends at the
// first comma and the environment starts at the last comma, so dataset names
// containing commas survive the round trip.
func ParseDatasetURN(urn string) (*ParsedDataset, error) {
	if !strings.HasPrefix(urn, datasetPrefix) || !strings.HasSuffix(urn, ")") {
		return nil, fmt.Errorf("invalid dataset URN: %s", urn)
	}

	body := urn[len(datasetPrefix) : len(urn)-1]
	firstComma := strings.Index(body, ",")
	lastComma := strings.LastIndex(body, ",")
	if firstComma < 0 || firstComma == lastComma {
		return nil, fmt.Errorf("invalid dataset URN format: %s", urn)
	}

	return &ParsedDataset{
		Platform: body[:firstComma],
		Name:     body[firstComma+1 : lastComma],
		Env:      body[lastComma+1:],
	}, nil
}
