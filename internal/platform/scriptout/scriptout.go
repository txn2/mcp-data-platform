// Package scriptout is the one serializer for a managed script's outputs:
// the bytes platform.export and platform.publish_data write, the ceiling they
// are held to, and the media type and file extension they are stored under.
//
// It is one package because both runs of a script go through it. A platform
// run persists exactly these bytes and a draft run measures them, so an
// output too large or malformed to write is refused while the author is
// still iterating rather than at the first scheduled fire. It was extracted
// from internal/platform/scriptrun, which composes these arms by the shape
// of the export request, when the xlsx arm (#1849) arrived.
package scriptout

import (
	"encoding/json"
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/platform/starlarkconv"
	"github.com/txn2/mcp-data-platform/internal/tablexlsx"
	"github.com/txn2/mcp-data-platform/pkg/contenttype"
	trinokit "github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

// MaxBytes caps one serialized output. It matches the ceiling the portal
// export path applies, so a script cannot write an asset a human could not
// have exported by hand.
const MaxBytes = 100 << 20

// Identity is how one output is stored: the media type it carries and the
// file extension its object keys take. It is a value rather than the
// formatter that produced the bytes, because the bytes are already
// serialized when a serializer here returns -- nothing downstream may
// re-serialize, and a document has no serializer to hand back.
type Identity struct {
	ContentType string
	Extension   string
}

// documentTypes maps each document format to the canonical media type it is
// stored under -- the same types the portal stores and renders for saved
// assets, so a script-published document is patchable like any other. The
// extension an object key carries follows from the type through
// contenttype.Extension, the one authority every other write path derives
// keys from, so a jsx document lands on the same key spelling a
// save_asset-written text/jsx object does.
var documentTypes = map[string]string{
	"markdown": contenttype.Markdown,
	"text":     contenttype.PlainText,
	"html":     contenttype.HTML,
	"jsx":      contenttype.JSX,
}

// documentFormats is documentTypes' key set, for the refusal that lists them.
var documentFormats = func() map[string]bool {
	out := make(map[string]bool, len(documentTypes))
	for format := range documentTypes {
		out[format] = true
	}
	return out
}()

// Rows serializes row dicts in a tabular format, projected onto columns, and
// checks the result against the ceiling.
func Rows(name, format string, columns []string, rows []any) ([]byte, Identity, error) {
	formatter, err := trinokit.NewFormatter(format)
	if err != nil {
		return nil, Identity{}, fmt.Errorf("output %q: %w", name, err)
	}
	data, err := formatter.Format(columns, tabular(columns, rows))
	if err != nil {
		return nil, Identity{}, fmt.Errorf("formatting output %q: %w", name, err)
	}
	if len(data) > MaxBytes {
		return nil, Identity{}, fmt.Errorf("output %q is %d bytes, over the %d-byte limit; aggregate in SQL or write fewer columns",
			name, len(data), MaxBytes)
	}
	return data, Identity{ContentType: formatter.ContentType(), Extension: formatter.FileExtension()}, nil
}

// Document passes a string body through as the output's bytes, checked
// against the same ceiling a tabular output is. Verbatim is the contract:
// what the script composed is what the portal stores or the bucket receives,
// byte for byte, so a draft's measurement and a real run's write cannot
// differ.
//
// The format is checked here as well as at the argument edge: this is the
// serializer both runs share, and a request some other constructor built with
// a body under csv or json must be refused rather than written verbatim as a
// "well-formed by construction" feed.
func Document(name, format, body string) ([]byte, Identity, error) {
	ct, ok := documentTypes[format]
	if !ok {
		return nil, Identity{}, fmt.Errorf("output %q: format %q is serialized from rows, a list of dicts; a string body is valid for the document formats %s",
			name, format, starlarkconv.SortedSet(documentFormats))
	}
	if len(body) > MaxBytes {
		return nil, Identity{}, fmt.Errorf("output %q is %d bytes, over the %d-byte limit; write a smaller document",
			name, len(body), MaxBytes)
	}
	return []byte(body), Identity{ContentType: ct, Extension: contenttype.Extension(ct)}, nil
}

// Workbook writes an xlsx workbook, checked against the ceiling (#1849).
func Workbook(name string, book *tablexlsx.Workbook) ([]byte, Identity, error) {
	data, err := book.Write(MaxBytes)
	if err != nil {
		return nil, Identity{}, fmt.Errorf("output %q: %w", name, err)
	}
	return data, Identity{ContentType: tablexlsx.ContentType, Extension: tablexlsx.Extension}, nil
}

// DataPayload serializes one publish_data payload as the JSON the data
// region will hold, checked against the same ceiling every export is.
//
// It is the single serializer for the payload -- a draft measures exactly the
// bytes a platform run splices -- and it keeps encoding/json's default
// escaping, which writes <, > and & as \u escapes, so no string in the
// payload can ever terminate the <script> element it lands inside. Go
// serializes map keys in sorted order, so the bytes are deterministic for a
// given payload. The indentation is for the reader of a version diff: a
// refreshed dashboard's history should read field by field, not as one
// replaced line.
func DataPayload(name string, data any) ([]byte, error) {
	out, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("output %q: the data cannot be serialized as JSON: %w", name, err)
	}
	if len(out) > MaxBytes {
		return nil, fmt.Errorf("output %q is %d bytes, over the %d-byte limit; aggregate in SQL or publish less data",
			name, len(out), MaxBytes)
	}
	return out, nil
}

// tabular projects row dicts onto the column order the script wrote. A row
// missing a column contributes an empty cell rather than shifting the row,
// which is what keeps a ragged result readable instead of misaligned.
func tabular(columns []string, rows []any) [][]any {
	out := make([][]any, 0, len(rows))
	for _, row := range rows {
		dict, ok := row.(map[string]any)
		if !ok {
			// A non-dict row has no columns to project. Rendering it as an empty
			// row keeps the row count honest; the alternative, dropping it, would
			// make the output disagree with the row count the script was told.
			out = append(out, make([]any, len(columns)))
			continue
		}
		cells := make([]any, len(columns))
		for i, column := range columns {
			cells[i] = dict[column]
		}
		out = append(out, cells)
	}
	return out
}
