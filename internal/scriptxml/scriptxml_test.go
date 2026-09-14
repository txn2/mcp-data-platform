package scriptxml_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/lib/json"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"

	"github.com/txn2/mcp-data-platform/internal/scriptxml"
)

// soapEnvelope is the document the module's worked example reads: a SOAP
// response whose prefixes are the sender's choice.
const soapEnvelope = `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
  <soap:Body>
    <GetRatesResponse xmlns="urn:acme:rates">
      <Rate currency="EUR">0.92</Rate>
      <Rate currency="GBP">0.79</Rate>
    </GetRatesResponse>
  </soap:Body>
</soap:Envelope>`

// exec runs a script against the same globals the engine predeclares for the
// names this package owns, and returns the value it bound to `got`. Driving
// the interpreter is the point: these functions are reached from Starlark and
// nowhere else, so calling them any other way would test a path no author uses.
func exec(t *testing.T, src string) starlark.Value {
	t.Helper()
	globals, err := starlark.ExecFileOptions(
		fileOptions, &starlark.Thread{Name: "test"}, "test.star", src,
		starlark.StringDict{"xml": scriptxml.Module, "json": json.Module},
	)
	require.NoError(t, err)
	return globals["got"]
}

func execErr(t *testing.T, src string) string {
	t.Helper()
	_, err := starlark.ExecFileOptions(
		fileOptions, &starlark.Thread{Name: "test"}, "test.star", src,
		starlark.StringDict{"xml": scriptxml.Module, "json": json.Module},
	)
	require.Error(t, err)
	return err.Error()
}

func TestDecode_ElementAttributes(t *testing.T) {
	got := exec(t, doc()+`
root = xml.decode(DOC)
got = [root.tag, root.ns, root.children[0].tag, str(len(root.children))]
`)
	assert.Equal(t, `["Envelope", "http://schemas.xmlsoap.org/soap/envelope/", "Body", "1"]`, got.String())
}

func TestDecode_TextAndAttrs(t *testing.T) {
	got := exec(t, doc()+`
rate = xml.find(xml.decode(DOC), "//Rate")
got = [rate.text, rate.attrs["currency"], rate.ns]
`)
	assert.Equal(t, `["0.92", "EUR", "urn:acme:rates"]`, got.String())
}

func TestFindAll_ReturnsEveryMatch(t *testing.T) {
	got := exec(t, doc()+`
got = [r.attrs["currency"] + "=" + r.text for r in xml.findall(xml.decode(DOC), "//Rate")]
`)
	assert.Equal(t, `["EUR=0.92", "GBP=0.79"]`, got.String())
}

func TestFind_ReturnsNoneWhenNothingMatches(t *testing.T) {
	got := exec(t, doc()+`
got = xml.find(xml.decode(DOC), "//Missing") == None
`)
	assert.Equal(t, "True", got.String())
}

func TestFindAll_EmptyListWhenNothingMatches(t *testing.T) {
	got := exec(t, `got = xml.findall(xml.decode("<a/>"), "//b")`)
	assert.Equal(t, "[]", got.String())
}

func TestElement_IsTrueSoAMatchIsDistinguishableFromNone(t *testing.T) {
	got := exec(t, doc()+`
body = xml.find(xml.decode(DOC), "//Body")
got = "found" if body else "missing"
`)
	assert.Equal(t, `"found"`, got.String())
}

func TestElement_JSONEncodesAsItsFiveFields(t *testing.T) {
	got := exec(t, `got = json.encode(xml.decode('<Rate currency="EUR">0.92</Rate>'))`)
	assert.JSONEq(t,
		`{"tag":"Rate","ns":"","attrs":{"currency":"EUR"},"text":"0.92","children":[]}`,
		mustString(t, got))
}

func TestElement_StringNamesTheTag(t *testing.T) {
	got := exec(t, `got = [str(xml.decode("<a/>")), str(xml.decode('<a xmlns="urn:x"/>'))]`)
	assert.Equal(t, `["<a>", "<a xmlns=\"urn:x\">"]`, got.String())
}

func TestEncode_RoundTripsAnElement(t *testing.T) {
	got := exec(t, doc()+`
root = xml.decode(DOC)
got = xml.encode(xml.decode(xml.encode(root))) == xml.encode(root)
`)
	assert.Equal(t, "True", got.String())
}

func TestEncode_BuildsADocumentFromDicts(t *testing.T) {
	got := exec(t, doc()+`
got = xml.encode({
    "tag": "Envelope",
    "ns": "http://schemas.xmlsoap.org/soap/envelope/",
    "children": [{"tag": "Body", "ns": "http://schemas.xmlsoap.org/soap/envelope/", "children": [
        {"tag": "GetRate", "attrs": {"currency": "EUR"}, "text": "now"},
    ]}],
})
`)
	assert.Equal(t,
		`<Envelope xmlns="http://schemas.xmlsoap.org/soap/envelope/"><Body><GetRate xmlns="" currency="EUR">now</GetRate></Body></Envelope>`,
		mustString(t, got))
}

func TestEncode_AcceptsAnElementInsideADict(t *testing.T) {
	got := exec(t, doc()+`
rate = xml.find(xml.decode(DOC), "//Rate")
got = xml.encode({"tag": "Wrapper", "children": [rate]})
`)
	assert.Equal(t, `<Wrapper><Rate xmlns="urn:acme:rates" currency="EUR">0.92</Rate></Wrapper>`, mustString(t, got))
}

func TestRefusals(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"malformed document", `xml.decode("<a>")`, "in xml.decode"},
		{"doctype", `xml.decode('<!DOCTYPE a []><a/>')`, "document type declarations are not accepted"},
		{"path outside the subset", `xml.find(xml.decode("<a/>"), "a[last()]")`, "unsupported path"},
		{"path on findall", `xml.findall(xml.decode("<a/>"), "/a")`, "unsupported path"},
		{"node that is not an element", `xml.find("<a/>", "a")`, "pass an element from xml.decode"},
		{"encode a string", `xml.encode("<a/>")`, `pass an element from xml.decode or a dict with a "tag" key`},
		{"encode a dict with no tag", `xml.encode({"text": "x"})`, `an element dict needs a non-empty "tag"`},
		{"encode a non-string tag", `xml.encode({"tag": 7})`, `the "tag" of an element dict is int; it has to be a string`},
		{"encode bad attrs", `xml.encode({"tag": "a", "attrs": [1]})`, `the "attrs" of an element dict is list; it has to be a dict of strings`},
		{"encode bad attr value", `xml.encode({"tag": "a", "attrs": {"k": 1}})`, "every attribute name and value has to be a string"},
		{"encode bad children", `xml.encode({"tag": "a", "children": "b"})`, `the "children" of an element dict is string; it has to be a list`},
		{"encode a bad child", `xml.encode({"tag": "a", "children": [1]})`, `in "children"[0]`},
		{"encode an unusable tag", `xml.encode({"tag": "a b"})`, "element name \"a b\" is not usable"},
		{"element is not a dict key", `{xml.decode("<a/>"): 1}`, "unhashable type: xml.element"},
		{"unknown field", `xml.decode("<a/>").missing`, "has no .missing field"},
		{"wrong argument name", `xml.decode(doc = "<a/>")`, "in xml.decode"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Contains(t, execErr(t, tt.src), tt.want)
		})
	}
}

func TestEncode_BoundsACycleBuiltInAScript(t *testing.T) {
	// A dict can reach itself through a list, which is the one way a
	// caller-built tree becomes unbounded.
	msg := execErr(t, `
kids = []
node = {"tag": "a", "children": kids}
kids.append(node)
xml.encode(node)
`)
	assert.Contains(t, msg, "exceeds the XML nesting limit")
}

func TestElement_SurvivesTheFreezeAtTheEndOfARun(t *testing.T) {
	// Starlark freezes a module's globals when execution ends. An element
	// is immutable, so the freeze is a no-op — and the value has to stay
	// readable afterwards, which is what a caller inspecting a run's
	// result does.
	got := exec(t, `got = xml.decode('<Rate currency="EUR">0.92</Rate>')`)
	el, ok := got.(starlark.HasAttrs)
	require.True(t, ok, "got %s; want an element", got.Type())

	text, err := el.Attr("text")
	require.NoError(t, err)
	assert.Equal(t, `"0.92"`, text.String())

	missing, err := el.Attr("nope")
	require.NoError(t, err)
	assert.Nil(t, missing, "an unknown field is reported by a nil value, not an error")

	got.Freeze()
	text, err = el.Attr("text")
	require.NoError(t, err, "a frozen element is still readable")
	assert.Equal(t, `"0.92"`, text.String())
}

func TestElement_MutatingTheViewIsRefusedRatherThanLost(t *testing.T) {
	// .children and .attrs are built per read. Frozen, an append fails
	// where it was written; unfrozen, it would succeed against a copy the
	// author never sees again.
	assert.Contains(t, execErr(t, `xml.decode("<a><b/></a>").children.append(1)`), "frozen")
	assert.Contains(t, execErr(t, `xml.decode('<a k="v"/>').attrs["k"] = "w"`), "frozen")
}

func TestModule_MembersAreTheFourDocumented(t *testing.T) {
	names := make([]string, 0, len(scriptxml.Module.Members))
	for name := range scriptxml.Module.Members {
		names = append(names, name)
	}
	assert.ElementsMatch(t, []string{"decode", "encode", "find", "findall"}, names)
}

// doc appends the shared fixture as a Starlark global.
func doc() string { return "DOC = " + starlark.String(soapEnvelope).String() + "\n" }

func mustString(t *testing.T, v starlark.Value) string {
	t.Helper()
	s, ok := starlark.AsString(v)
	require.True(t, ok, "got %s; want a string", v.Type())
	return s
}

// fileOptions are the dialect switches the engine runs a script under
// (internal/platform/scriptrun). The module is exercised through the same
// dialect an author writes in rather than through Starlark's defaults.
var fileOptions = &syntax.FileOptions{
	Set:               true,
	While:             false,
	TopLevelControl:   true,
	GlobalReassign:    true,
	LoadBindsGlobally: false,
	Recursion:         false,
}
