// Package exportmeta reads platform.export's tags= and metadata= arguments
// (#1848): what identifies a portal output beyond its name, so an application
// finds the reports it generated again.
package exportmeta

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"go.starlark.net/starlark"

	"github.com/txn2/mcp-data-platform/internal/platform/starlarkconv"
)

// An output's tags and metadata (#1848): what identifies it beyond its name,
// so an application finds the reports it generated again. Both describe a
// portal asset, and both are bounded: a tag is a label and metadata is a small
// object, not a second copy of the data.
const (
	maxTags          = 20
	maxTagLen        = 100
	maxMetadataBytes = 4 << 10
)

// PlatformMetadataKeys are the metadata keys the platform records on every
// output version itself; a script may not set them.
var PlatformMetadataKeys = []string{"run_id", "script", "script_version", "requested_by"}

// errNotPortal refuses tags or metadata on an output that is not a portal
// asset, which has neither.
var errNotPortal = errors.New("tags and metadata describe a portal output; this destination keeps neither")

// Describe reads platform.export's tags= and metadata= arguments. Both are
// optional; either is refused on a destination other than the portal.
func Describe(tags, metadata starlark.Value, portal bool) (tagList []string, metadataObj map[string]any, err error) {
	absent := func(v starlark.Value) bool { return v == nil || v == starlark.None }
	if absent(tags) && absent(metadata) {
		return nil, nil, nil
	}
	if !portal {
		return nil, nil, errNotPortal
	}
	if tagList, err = readTags(tags); err != nil {
		return nil, nil, err
	}
	if metadataObj, err = metadataObject(metadata); err != nil {
		return nil, nil, err
	}
	return tagList, metadataObj, nil
}

// readTags reads tags= as a list of short, non-empty strings.
func readTags(v starlark.Value) ([]string, error) {
	if v == nil || v == starlark.None {
		return nil, nil
	}
	l, ok := v.(*starlark.List)
	if !ok {
		return nil, fmt.Errorf("tags is a list of strings, got %s", v.Type())
	}
	if l.Len() > maxTags {
		return nil, fmt.Errorf("tags names at most %d tags, got %d", maxTags, l.Len())
	}
	out := make([]string, 0, l.Len())
	for i := range l.Len() {
		s, ok := starlark.AsString(l.Index(i))
		s = strings.TrimSpace(s)
		if !ok || s == "" || len(s) > maxTagLen {
			return nil, fmt.Errorf("tag %d is a string of 1 to %d characters", i, maxTagLen)
		}
		out = append(out, s)
	}
	return out, nil
}

// metadataObject reads metadata= as a small JSON object whose keys are not
// the platform's own.
func metadataObject(v starlark.Value) (map[string]any, error) {
	if v == nil || v == starlark.None {
		return nil, nil //nolint:nilnil // no argument, no metadata
	}
	d, ok := v.(*starlark.Dict)
	if !ok {
		return nil, fmt.Errorf("metadata is a dict, got %s", v.Type())
	}
	goValue, err := starlarkconv.FromStarlark(d)
	if err != nil {
		return nil, fmt.Errorf("metadata: %w", err)
	}
	obj, _ := goValue.(map[string]any)
	for _, key := range PlatformMetadataKeys {
		if _, taken := obj[key]; taken {
			return nil, fmt.Errorf("metadata key %q is recorded by the platform on every output; name yours differently", key)
		}
	}
	raw, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("metadata: %w", err)
	}
	if len(raw) > maxMetadataBytes {
		return nil, fmt.Errorf("metadata is at most %d bytes as JSON, got %d", maxMetadataBytes, len(raw))
	}
	return obj, nil
}
