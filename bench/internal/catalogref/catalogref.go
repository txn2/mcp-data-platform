// Package catalogref is how the bench names a warehouse table in the
// platform catalog and reads one back: the mcp:dataset reference of a bench
// table on the bench's Trino connection, and the curation the `fetch` tool
// returns for it.
package catalogref

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrNotFound is returned by Parse for a reference the catalog does not hold.
var ErrNotFound = errors.New("catalog holds no such dataset")

// Connection is the Trino connection every bench config serves the warehouse
// through (toolkits.trino.instances.e2e).
const Connection = "e2e"

// FetchTool is the platform tool that dereferences a catalog reference.
const FetchTool = "fetch"

// Dataset is the catalog reference of a table in the bench warehouse schema
// (memory.bench).
func Dataset(table string) string {
	return fmt.Sprintf("mcp:dataset:(trino,%s,memory.bench.%s)", Connection, table)
}

// FetchArgs are the fetch arguments that read one reference.
func FetchArgs(ref string) map[string]any {
	return map[string]any{"reference": ref}
}

// Curation is the part of a fetched dataset's curation the bench inspects.
type Curation struct {
	Description      string            `json:"description"`
	Tags             []string          `json:"tags"`
	CustomProperties map[string]string `json:"custom_properties"`
}

// fetchResult is the fetch tool's answer for a dataset reference.
type fetchResult struct {
	Found    bool   `json:"found"`
	Message  string `json:"message"`
	Document *struct {
		Content *struct {
			Aspects Curation `json:"aspects"`
		} `json:"content"`
	} `json:"document"`
}

// Parse reads the curation out of a fetch result's text. Enrichment
// middleware may append context after the result JSON, so the decoder reads
// one value and ignores the rest. A reference the catalog does not hold is an
// error naming the fetch tool's own explanation.
func Parse(text string) (Curation, error) {
	var res fetchResult
	if err := json.NewDecoder(strings.NewReader(text)).Decode(&res); err != nil {
		return Curation{}, fmt.Errorf("parse fetch result: %w (text: %.200s)", err, text)
	}
	if !res.Found {
		return Curation{}, fmt.Errorf("%w: %s", ErrNotFound, res.Message)
	}
	if res.Document == nil || res.Document.Content == nil {
		return Curation{}, fmt.Errorf("fetch result carries no dataset content (text: %.200s)", text)
	}
	return res.Document.Content.Aspects, nil
}

// Render is the fetch tool's answer for a dataset carrying the given
// curation, in the shape Parse reads. The bench's fake platforms answer fetch
// with it, so they speak the same wire form the platform does.
func Render(ref string, c Curation) string {
	raw, _ := json.Marshal(map[string]any{ //nolint:errchkjson // strings and string maps always encode
		"found":     true,
		"reference": ref,
		"document": map[string]any{
			"reference": ref,
			"source":    "catalog",
			"content":   map[string]any{"reference": ref, "aspects": c},
		},
	})
	return string(raw)
}
