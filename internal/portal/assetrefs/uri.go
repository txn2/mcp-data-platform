package assetrefs

import (
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

// FormURIPrefix starts the reference an asset embeds a form by:
// mcp:form:<endpoint> (#2177).
const FormURIPrefix = "mcp:form:"

// FormPagePrefix is the form page a form reference is rewritten to:
// internal/endpoint/epform's PagePrefix, restated here so this package does
// not import the endpoint layer; a test holds them together.
const FormPagePrefix = "/forms/"

// FormURI is the reference an asset embeds the form at endpoint by.
func FormURI(endpoint string) string { return FormURIPrefix + endpoint }

// ParseFormURI is the endpoint a form reference names, and whether uri is one.
func ParseFormURI(uri string) (string, bool) {
	name, ok := strings.CutPrefix(uri, FormURIPrefix)
	return name, ok && name != ""
}

// AssetURI is the canonical form an asset is referenced by from another
// asset's content: mcp:asset:<id> (#1488).
//
// It is built from the platform's one reference vocabulary rather than
// formatted here, so the string a picker records, the string a search hit
// hands an agent, and the string fetch dereferences are the same string. An
// author who found an asset through search can paste that reference straight
// into their markup.
func AssetURI(assetID string) string {
	return knowledgepage.EntityRef{
		TargetType: knowledgepage.RefTargetAsset,
		AssetID:    assetID,
	}.URN()
}
