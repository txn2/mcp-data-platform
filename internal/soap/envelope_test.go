package soap

import (
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/txn2/mcp-data-platform/internal/xmltree"
)

// loadOperation imports a WSDL, loads the document it renders through the same
// loader the catalog uses, and returns one operation's extension and request
// schema. Going through the rendered document rather than the in-memory model
// is the point: it is what proves the two extensions survive being written to
// JSON and read back by kin-openapi, which is the only path the gateway has.
func loadOperation(t *testing.T, wsdl, pathKey string) (*Extension, *openapi3.Schema) {
	t.Helper()
	_, rendered, err := Import(wsdl)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	loader := openapi3.Loader{IsExternalRefsAllowed: false}
	doc, err := loader.LoadFromData([]byte(rendered))
	if err != nil {
		t.Fatalf("loading the rendered document: %v", err)
	}
	item := doc.Paths.Find(pathKey)
	if item == nil || item.Post == nil {
		t.Fatalf("rendered document has no POST at %q", pathKey)
	}
	ext, ok := ExtensionFrom(item.Post)
	if !ok {
		t.Fatalf("operation at %q carries no %s extension", pathKey, ExtensionKey)
	}
	var schema *openapi3.Schema
	if item.Post.RequestBody != nil {
		for _, mt := range item.Post.RequestBody.Value.Content {
			schema = mt.Schema.Value
		}
	}
	return ext, schema
}

func TestEncodeBuildsASOAP11Envelope(t *testing.T) {
	ext, schema := loadOperation(t, weatherWSDL, "/svc/Weather.asmx/GetForecast")

	got, err := Encode(ext, schema, map[string]any{
		"City":      "Paris",
		"Days":      float64(3),
		"RequestId": "r-1",
	})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	for _, want := range []string{
		envelopeNS11,
		// The schema is unqualified, so a child is written into no
		// namespace explicitly; without the empty declaration it would
		// inherit the body element's.
		`<City xmlns="">Paris</City>`,
		`<Days xmlns="">3</Days>`,
		`RequestId="r-1"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("envelope does not contain %q:\n%s", want, got)
		}
	}
}

// An xsd:sequence is ordered and a JSON object is not. The encoder has to emit
// the schema's order whatever order the caller's object was written in, or an
// upstream rejects a body whose every value is correct.
func TestEncodeWritesChildrenInTheSchemasOrder(t *testing.T) {
	ext, schema := loadOperation(t, weatherWSDL, "/svc/Weather.asmx/GetForecast")

	got, err := Encode(ext, schema, map[string]any{
		"Units":     "metric",
		"Days":      float64(2),
		"City":      "Oslo",
		"RequestId": "r-2",
	})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	city, days, units := strings.Index(got, "<City"), strings.Index(got, "<Days"), strings.Index(got, "<Units")
	if city < 0 || days < 0 || units < 0 {
		t.Fatalf("envelope is missing an element:\n%s", got)
	}
	if city >= days || days >= units {
		t.Errorf("elements are out of schema order (City %d, Days %d, Units %d):\n%s", city, days, units, got)
	}
}

// The XSD default is unqualified: a child element carries no namespace, and an
// upstream refuses one that does.
func TestEncodeLeavesChildrenUnqualifiedByDefault(t *testing.T) {
	ext, schema := loadOperation(t, weatherWSDL, "/svc/Weather.asmx/GetForecast")

	got, err := Encode(ext, schema, map[string]any{"City": "Lima", "RequestId": "r"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if ext.Input.Qualified {
		t.Fatal("the fixture declares no elementFormDefault, so it must import as unqualified")
	}
	// The operation element is global and carries the namespace. Its
	// children are in no namespace, which under a namespaced parent has to
	// be said rather than left out: an omitted declaration would put them
	// in the parent's namespace, which is the opposite of unqualified.
	if !strings.Contains(got, `<GetForecast xmlns="http://example.org/weather"`) {
		t.Errorf("the body element does not carry its namespace:\n%s", got)
	}
	if !strings.Contains(got, `<City xmlns="">`) {
		t.Errorf("an unqualified child was not placed in the empty namespace:\n%s", got)
	}
}

func TestEncodeQualifiesChildrenWhenTheSchemaSaysSo(t *testing.T) {
	qualified := strings.Replace(weatherWSDL,
		`<xsd:schema targetNamespace="http://example.org/weather">`,
		`<xsd:schema targetNamespace="http://example.org/weather" elementFormDefault="qualified">`, 1)
	ext, schema := loadOperation(t, qualified, "/svc/Weather.asmx/GetForecast")

	if !ext.Input.Qualified {
		t.Fatal("elementFormDefault=qualified did not survive the import")
	}
	got, err := Encode(ext, schema, map[string]any{"City": "Lima", "RequestId": "r"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	// The child inherits the body element's namespace, so the encoder emits
	// no second declaration; what matters is that it is IN that namespace.
	if !strings.Contains(got, "<City>Lima</City>") {
		t.Errorf("qualified child is not written in the inherited namespace:\n%s", got)
	}
	if strings.Contains(got, `<City xmlns="">`) {
		t.Errorf("qualified child was written with an empty namespace:\n%s", got)
	}
}

// A repeated element is a run of siblings in XML, not one element holding a
// list.
func TestEncodeExpandsAListIntoSiblingElements(t *testing.T) {
	ext, schema := loadOperation(t, weatherWSDL, "/svc/Weather.asmx/GetForecast")
	// The request has no repeated field, so drive the encoder with the
	// response element's shape, which does.
	_ = schema
	respExt := &Extension{Version: Version11, Input: ExtensionElement{Element: "GetForecastResponse", Namespace: ext.Input.Namespace}}

	got, err := Encode(respExt, nil, map[string]any{
		"Day": []any{
			map[string]any{"Date": "2026-01-01"},
			map[string]any{"Date": "2026-01-02"},
		},
	})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if n := strings.Count(got, "<Day"); n != 2 {
		t.Errorf("list produced %d Day elements, want 2:\n%s", n, got)
	}
	if !strings.Contains(got, "<Date>2026-01-02</Date>") {
		t.Errorf("a list item's fields are missing:\n%s", got)
	}
}

func TestEncodeNestsAnObjectField(t *testing.T) {
	ext, schema := loadOperation(t, extendedWSDL, "/PostOrder")

	got, err := Encode(ext, schema, map[string]any{
		"Order": map[string]any{
			"Id":       "A-1",
			"Priority": float64(2),
			"Carrier":  "DHL",
		},
	})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !strings.Contains(got, "<Id>A-1</Id>") || !strings.Contains(got, "<Priority>2</Priority>") {
		t.Errorf("nested object fields are missing:\n%s", got)
	}
	// Carrier is an attribute of the nested element, not a child of it.
	if !strings.Contains(got, `Carrier="DHL"`) {
		t.Errorf("a nested attribute was not placed on the start tag:\n%s", got)
	}
	if strings.Contains(got, "<Carrier>") {
		t.Errorf("a nested attribute was written as an element:\n%s", got)
	}
}

// JSON has one number type. An upstream whose schema says xsd:int rejects "3"
// spelled "3.0", so an integral value must not acquire a decimal point.
func TestEncodeWritesAnIntegralNumberWithoutADecimalPoint(t *testing.T) {
	ext := &Extension{Version: Version11, Input: ExtensionElement{Element: "Op", Namespace: "urn:x"}}

	got, err := Encode(ext, nil, map[string]any{
		"Count": float64(7),
		"Rate":  2.5,
		"On":    true,
		"Empty": nil,
	})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	for _, want := range []string{`<Count xmlns="">7</Count>`, `<Rate xmlns="">2.5</Rate>`, `<On xmlns="">true</On>`} {
		if !strings.Contains(got, want) {
			t.Errorf("envelope does not contain %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, `<Empty xmlns=""></Empty>`) && !strings.Contains(got, `<Empty xmlns=""/>`) {
		t.Errorf("a null field did not produce an empty element:\n%s", got)
	}
}

func TestEncodeUsesTheSOAP12Envelope(t *testing.T) {
	ext, schema := loadOperation(t, extendedWSDL, "/PostOrder")

	if ext.Version != Version12 {
		t.Fatalf("fixture version = %q, want %q", ext.Version, Version12)
	}
	got, err := Encode(ext, schema, map[string]any{"Order": map[string]any{"Id": "x", "Priority": float64(1)}})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !strings.Contains(got, envelopeNS12) {
		t.Errorf("envelope does not use the SOAP 1.2 namespace:\n%s", got)
	}
	if strings.Contains(got, envelopeNS11) {
		t.Errorf("envelope carries the SOAP 1.1 namespace:\n%s", got)
	}
}

// A caller holding an envelope already has a way through — a string body is
// sent verbatim — so the refusal has to name it rather than just failing.
func TestEncodeRefusesABodyThatIsNotAnObject(t *testing.T) {
	ext := &Extension{Version: Version11, Input: ExtensionElement{Element: "Op"}}

	for _, body := range []any{"<Envelope/>", float64(3), []any{1, 2}, nil} {
		if _, err := Encode(ext, nil, body); err == nil {
			t.Errorf("Encode accepted %T as a body", body)
		}
	}
	_, err := Encode(ext, nil, "x")
	if !strings.Contains(err.Error(), "string containing the whole envelope") {
		t.Errorf("refusal = %q, want it to name the string form", err)
	}
}

func TestEncodeRefusesAnOperationWithNoRequestElement(t *testing.T) {
	if _, err := Encode(nil, nil, map[string]any{}); err == nil {
		t.Error("Encode accepted a nil extension")
	}
	if _, err := Encode(&Extension{}, nil, map[string]any{}); err == nil {
		t.Error("Encode accepted an extension with no input element")
	}
}

// Without a schema every field is still written, because a connection may
// carry an operation whose WSDL left the shape open and refusing would strand
// the caller.
func TestEncodeWithoutASchemaWritesEveryFieldInSortedOrder(t *testing.T) {
	ext := &Extension{Version: Version11, Input: ExtensionElement{Element: "Op", Namespace: "urn:x"}}

	got, err := Encode(ext, nil, map[string]any{"b": "2", "a": "1"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if strings.Index(got, "<a") > strings.Index(got, "<b") {
		t.Errorf("fields without a schema are not in a stable order:\n%s", got)
	}
}

// A field the schema does not declare is written rather than refused: the
// gateway does not validate request bodies, and the upstream's own error is a
// better answer than a guess about whether the schema is complete.
func TestEncodeKeepsAFieldTheSchemaDoesNotDeclare(t *testing.T) {
	ext, schema := loadOperation(t, weatherWSDL, "/svc/Weather.asmx/GetForecast")

	got, err := Encode(ext, schema, map[string]any{"City": "Rome", "RequestId": "r", "Extra": "kept"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !strings.Contains(got, `<Extra xmlns="">kept</Extra>`) {
		t.Errorf("an undeclared field was dropped:\n%s", got)
	}
}

func TestEncodeRefusesABodyThatNestsWithoutEnd(t *testing.T) {
	ext := &Extension{Version: Version11, Input: ExtensionElement{Element: "Op"}}
	deep := map[string]any{"leaf": "x"}
	for range xmltree.DefaultLimits.MaxDepth + 5 {
		deep = map[string]any{"n": deep}
	}
	if _, err := Encode(ext, nil, deep); err == nil {
		t.Error("Encode accepted a body deeper than the shared limit")
	}
}

func TestExtensionFromIgnoresANonSOAPOperation(t *testing.T) {
	if _, ok := ExtensionFrom(nil); ok {
		t.Error("ExtensionFrom claimed a nil operation is SOAP")
	}
	if _, ok := ExtensionFrom(&openapi3.Operation{}); ok {
		t.Error("ExtensionFrom claimed an operation with no extension is SOAP")
	}
	bad := &openapi3.Operation{Extensions: map[string]any{ExtensionKey: "not an object"}}
	if _, ok := ExtensionFrom(bad); ok {
		t.Error("ExtensionFrom accepted an extension that is not an object")
	}
	empty := &openapi3.Operation{Extensions: map[string]any{ExtensionKey: map[string]any{"version": "1.1"}}}
	if _, ok := ExtensionFrom(empty); ok {
		t.Error("ExtensionFrom accepted an extension naming no input element")
	}
}
