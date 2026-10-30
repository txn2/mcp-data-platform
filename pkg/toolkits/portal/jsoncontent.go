package portal

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/contenttype"
)

// errContentForm refuses a content argument that is neither a string nor a
// JSON object or array.
var errContentForm = errors.New("content must be a string, or a JSON object or array for a JSON content type")

// UnmarshalJSON reads save_asset's arguments, taking content as a string or,
// for a JSON content type, as the JSON object or array itself (#2296).
func (in *saveAssetInput) UnmarshalJSON(data []byte) error {
	type plain saveAssetInput
	var aux struct {
		plain
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err //nolint:wrapcheck // the decode error is the caller's to read as is
	}
	text, structured, err := decodeContent(aux.Content)
	if err != nil {
		return err
	}
	*in = saveAssetInput(aux.plain)
	in.Content, in.contentStructured = text, structured
	return nil
}

// UnmarshalJSON reads manage_asset's arguments, taking content as save_asset
// does.
func (in *manageAssetInput) UnmarshalJSON(data []byte) error {
	type plain manageAssetInput
	var aux struct {
		plain
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err //nolint:wrapcheck // the decode error is the caller's to read as is
	}
	text, structured, err := decodeContent(aux.Content)
	if err != nil {
		return err
	}
	*in = manageAssetInput(aux.plain)
	in.Content, in.contentStructured = text, structured
	return nil
}

// decodeContent returns a content argument as the text to store, and whether
// it arrived as a JSON object or array rather than a string. A structured
// value is stored as its own JSON, indented, with its keys in the order the
// caller wrote them, so a version diff reads field by field.
func decodeContent(raw json.RawMessage) (text string, structured bool, err error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", false, nil
	}
	switch trimmed[0] {
	case '"':
		var s string
		if err = json.Unmarshal(trimmed, &s); err != nil {
			return "", false, fmt.Errorf("content: %w", err)
		}
		return s, false, nil
	case '{', '[':
		var out bytes.Buffer
		if err = json.Indent(&out, trimmed, "", "  "); err != nil {
			return "", false, fmt.Errorf("content: %w", err)
		}
		return out.String(), true, nil
	default:
		return "", false, errContentForm
	}
}

// structuredContentRefusal refuses content sent as a JSON object or array
// under a content type that is not JSON, where storing its JSON text would
// change what the caller meant. It returns the empty string when the content
// may be stored.
func structuredContentRefusal(structured bool, contentType string) string {
	if !structured || isJSONType(contentType) {
		return ""
	}
	return fmt.Sprintf("content was sent as a JSON object or array, which is accepted only for a JSON content type "+
		"(application/json, %s, or another +json type); content_type is %s, so send the content as a string. "+
		"Nothing was written.", contenttype.VegaLite, contentType)
}

// isJSONType reports whether a content type's syntax is JSON.
func isJSONType(contentType string) bool {
	norm := contenttype.Normalize(contentType)
	return norm == contenttype.JSON || strings.HasSuffix(norm, "+json")
}
