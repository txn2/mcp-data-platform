package contenttype

import "testing"

// TestActiveTypesAreScriptableDocuments pins the containment the two sets rest
// on, over the real set rather than a copy of it. A family added to activeTypes
// alone would be refused by detection and then served inline, which is the one
// combination that must not be reachable.
func TestActiveTypesAreScriptableDocuments(t *testing.T) {
	t.Parallel()

	if len(activeTypes) == 0 {
		t.Fatal("activeTypes is empty; the containment check would pass vacuously")
	}
	for ct := range activeTypes {
		if !IsScriptableDocument(ct) {
			t.Errorf("%q is active but not a scriptable document; the two sets have drifted", ct)
		}
	}
}

// TestScriptableDocumentSetCoversXML records why the scriptable set is wider
// than the active set: XML must stay detectable, and must not be served inline.
func TestScriptableDocumentSetCoversXML(t *testing.T) {
	t.Parallel()

	if activeTypes[XML] {
		t.Error("XML must not be active: Detect names it from content, and that is safe")
	}
	if !scriptableDocumentTypes[XML] {
		t.Error("XML must be a scriptable document: a browser honors <?xml-stylesheet?> in it")
	}
}
