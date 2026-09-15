package soap

import (
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/xmltree"
)

// faultIn decodes a response and asks it for a fault, which is the pair of
// steps the gateway performs: the decoder parses the body once and the fault
// is read off the tree it produced.
func faultIn(doc string) (Fault, bool) {
	root, err := xmltree.Decode(doc, xmltree.DefaultLimits)
	if err != nil {
		return Fault{}, false
	}
	return FaultFromTree(root)
}

// A SOAP 1.1 fault, with the prefix a .NET or Axis service typically writes.
const fault11 = `<?xml version="1.0"?>
<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
  <soap:Body>
    <soap:Fault>
      <faultcode>soap:Client</faultcode>
      <faultstring>City is required.</faultstring>
      <detail><ValidationError field="City"/></detail>
    </soap:Fault>
  </soap:Body>
</soap:Envelope>`

// The same failure as SOAP 1.2 spells it, under a different prefix again.
const fault12 = `<?xml version="1.0"?>
<env:Envelope xmlns:env="http://www.w3.org/2003/05/soap-envelope">
  <env:Body>
    <env:Fault>
      <env:Code><env:Value>env:Sender</env:Value></env:Code>
      <env:Reason><env:Text xml:lang="en">City is required.</env:Text></env:Reason>
    </env:Fault>
  </env:Body>
</env:Envelope>`

const successEnvelope = `<?xml version="1.0"?>
<S:Envelope xmlns:S="http://schemas.xmlsoap.org/soap/envelope/">
  <S:Body><GetForecastResponse xmlns="urn:w"><Ok>true</Ok></GetForecastResponse></S:Body>
</S:Envelope>`

func TestFaultReadsASOAP11Fault(t *testing.T) {
	got, ok := faultIn(fault11)
	if !ok {
		t.Fatal("no fault found in a 1.1 fault document")
	}
	if got.Code != "soap:Client" {
		t.Errorf("code = %q, want soap:Client", got.Code)
	}
	if got.Reason != "City is required." {
		t.Errorf("reason = %q", got.Reason)
	}
	if !strings.Contains(got.Detail, "ValidationError") {
		t.Errorf("detail = %q, want the application's own element", got.Detail)
	}
}

// The two versions spell the same failure differently. A caller should not
// have to know which one answered, so both read into the same two fields.
func TestFaultReadsASOAP12Fault(t *testing.T) {
	got, ok := faultIn(fault12)
	if !ok {
		t.Fatal("no fault found in a 1.2 fault document")
	}
	if got.Code != "env:Sender" {
		t.Errorf("code = %q, want env:Sender", got.Code)
	}
	if got.Reason != "City is required." {
		t.Errorf("reason = %q", got.Reason)
	}
}

// The prefix is the sender's choice and is never the same twice, so matching
// has to be on the local name.
func TestFaultIgnoresThePrefixTheSenderChose(t *testing.T) {
	for _, prefix := range []string{"soapenv", "S", "SOAP-ENV"} {
		doc := strings.ReplaceAll(fault11, "soap:", prefix+":")
		doc = strings.ReplaceAll(doc, "xmlns:soap=", "xmlns:"+prefix+"=")
		got, ok := faultIn(doc)
		if !ok {
			t.Errorf("prefix %q: no fault found", prefix)
			continue
		}
		if got.Reason != "City is required." {
			t.Errorf("prefix %q: reason = %q", prefix, got.Reason)
		}
	}
}

func TestFaultReportsNoFaultOnASuccessfulResponse(t *testing.T) {
	if _, ok := faultIn(successEnvelope); ok {
		t.Error("a successful response was read as a fault")
	}
}

func TestFaultReportsNoFaultOnSomethingThatIsNotAnEnvelope(t *testing.T) {
	for _, doc := range []string{
		``,
		`not xml at all`,
		`<html><body>502 Bad Gateway</body></html>`,
		`{"error":"nope"}`,
		`<Envelope><Header/></Envelope>`,
	} {
		if _, ok := faultIn(doc); ok {
			t.Errorf("%q was read as a fault", doc)
		}
	}
}

func TestFaultFromTreeHandlesAnAbsentTree(t *testing.T) {
	if _, ok := FaultFromTree(nil); ok {
		t.Error("FaultFromTree claimed a nil tree is a fault")
	}
}

// The message is what the gateway reports the failure by, in place of the
// HTTP status text that says only "Internal Server Error".
func TestFaultMessageCombinesWhatThereIs(t *testing.T) {
	tests := []struct {
		name  string
		fault Fault
		want  string
	}{
		{"both", Fault{Code: "soap:Client", Reason: "bad input"}, "soap:Client: bad input"},
		{"reason only", Fault{Reason: "bad input"}, "bad input"},
		{"code only", Fault{Code: "soap:Server"}, "soap:Server"},
		{"neither", Fault{}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.fault.Message(); got != tc.want {
				t.Errorf("Message() = %q, want %q", got, tc.want)
			}
		})
	}
}

// A 1.2 Reason with text directly on it, rather than in a Text child, is
// malformed but is what some stacks emit; reading it is better than reporting
// a fault with no words in it.
func TestFaultFallsBackToTheReasonsOwnText(t *testing.T) {
	doc := `<env:Envelope xmlns:env="http://www.w3.org/2003/05/soap-envelope"><env:Body>
	  <env:Fault><env:Code><env:Value>env:Receiver</env:Value></env:Code>
	  <env:Reason>backend unavailable</env:Reason></env:Fault></env:Body></env:Envelope>`
	got, ok := faultIn(doc)
	if !ok {
		t.Fatal("no fault found")
	}
	if got.Reason != "backend unavailable" {
		t.Errorf("reason = %q", got.Reason)
	}
}

func TestFaultReadsATwelveSpelledDetail(t *testing.T) {
	doc := strings.ReplaceAll(fault11, "<detail>", "<Detail>")
	doc = strings.ReplaceAll(doc, "</detail>", "</Detail>")
	got, ok := faultIn(doc)
	if !ok {
		t.Fatal("no fault found")
	}
	if !strings.Contains(got.Detail, "ValidationError") {
		t.Errorf("detail = %q", got.Detail)
	}
}

func TestFaultReportsAFaultCarryingNoDetail(t *testing.T) {
	got, ok := faultIn(fault12)
	if !ok {
		t.Fatal("no fault found")
	}
	if got.Detail != "" {
		t.Errorf("detail = %q, want empty", got.Detail)
	}
}

// The namespace is what makes an envelope a SOAP one. A document whose root is
// named Envelope in some other vocabulary is not a fault, and reading it as
// one would put another protocol's words into the gateway's error field.
func TestFaultRefusesAnEnvelopeInAForeignNamespace(t *testing.T) {
	doc := `<Envelope xmlns="urn:something:else"><Body><Fault>` +
		`<faultcode>x</faultcode><faultstring>y</faultstring></Fault></Body></Envelope>`
	if _, ok := faultIn(doc); ok {
		t.Error("a foreign-namespace Envelope was read as a SOAP fault")
	}
}

// A sender that declares no namespace at all leaves the element names as the
// only evidence, and refusing those would lose a real fault.
func TestFaultAcceptsAnEnvelopeWithNoNamespace(t *testing.T) {
	doc := `<Envelope><Body><Fault><faultcode>soap:Server</faultcode>` +
		`<faultstring>down</faultstring></Fault></Body></Envelope>`
	got, ok := faultIn(doc)
	if !ok {
		t.Fatal("an undeclared-namespace fault was refused")
	}
	if got.Reason != "down" {
		t.Errorf("reason = %q", got.Reason)
	}
}
