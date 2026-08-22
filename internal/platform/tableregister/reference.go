package tableregister

import (
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

// ParseReference resolves the canonical reference an agent already holds --
// the string `search` emits on a hit and `fetch` dereferences -- into the kind
// and id a registration is built over.
//
// It is what makes one action serve every kind of stored file. The platform
// has exactly one vocabulary for naming a record across tools, so a
// registration keyed by that vocabulary needs no per-kind argument and no
// second tool; the kind travels inside the reference.
//
// Only the two stored-file kinds resolve. Any other well-formed reference (a
// knowledge page, a dataset, a memory record) parses and is then refused by
// name, because naming what was passed tells the caller what to pass instead.
func ParseReference(reference string) (kind, id string, err error) {
	ref, parseErr := knowledgepage.ParseEntityRef(reference)
	if parseErr != nil {
		return "", "", fmt.Errorf("reference %q is %w: %s", reference, ErrBadReference, parseErr.Error())
	}
	switch ref.TargetType {
	case knowledgepage.RefTargetAsset:
		return KindAsset, ref.AssetID, nil
	case knowledgepage.RefTargetResource:
		return KindResource, ref.ResourceID, nil
	default:
		return "", "", fmt.Errorf(
			"reference %q names a %s, which is %w -- only a stored file can be a table, so pass the "+
				"mcp:resource: or mcp:asset: reference a search hit carries",
			reference, ref.TargetType, ErrBadReference)
	}
}
