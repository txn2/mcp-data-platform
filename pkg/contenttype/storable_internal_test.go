package contenttype

import "testing"

// TestStorableTextExcludesScriptableDocumentsItCannotContain pins the one
// security-relevant absence in the set. HTML, JSX, SVG, JavaScript and XML are
// members and are contained at serve time by blobserve's sandbox CSP and
// attachment disposition; XHTML is not a member, so a declaration alone cannot
// put the natively-rendered family into an asset.
func TestStorableTextExcludesScriptableDocumentsItCannotContain(t *testing.T) {
	t.Parallel()

	if storableTextTypes[XHTML] {
		t.Error("XHTML must not be storable text")
	}
	for _, ct := range []string{HTML, JSX, SVG, JavaScript, XML} {
		if !storableTextTypes[ct] {
			t.Errorf("%q is expected to remain storable text; removing it is a capability change, not a hardening", ct)
		}
	}
}

// TestStorableTextTypesHaveExtensions holds the set to the families the platform
// names a storage extension for. A member without one would be stored under a
// key ending in .bin, which is the state the extension table exists to end.
func TestStorableTextTypesHaveExtensions(t *testing.T) {
	t.Parallel()

	for ct := range storableTextTypes {
		if _, ok := extensions[ct]; !ok {
			t.Errorf("%q is storable text but has no storage extension", ct)
		}
	}
}
