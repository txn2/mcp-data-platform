package soap

import (
	"strings"

	"github.com/txn2/mcp-data-platform/internal/xmltree"
)

// Fault is a soap:Fault read off a response.
//
// The two versions spell it differently — SOAP 1.1 carries faultcode and
// faultstring as unqualified children, SOAP 1.2 carries Code/Value and
// Reason/Text — and a caller should not have to know which one answered. Both
// are read into the same two fields, named for the 1.1 spelling because that
// is the one the gateway's output has always used for an upstream's own error
// text.
type Fault struct {
	// Code is the fault code: the 1.1 faultcode, or the 1.2 Code/Value.
	Code string
	// Reason is the human-readable text: the 1.1 faultstring, or the 1.2
	// Reason/Text.
	Reason string
	// Detail is the fault's application-specific detail as text, empty when
	// the fault carries none. It is not parsed further: its content is
	// defined by the upstream's own schema, and the decoded response body
	// carries the whole tree for a caller that needs it.
	Detail string
}

// Message renders a fault as the one line the gateway reports it by.
func (f Fault) Message() string {
	switch {
	case f.Code != "" && f.Reason != "":
		return f.Code + ": " + f.Reason
	case f.Reason != "":
		return f.Reason
	default:
		return f.Code
	}
}

// FaultFromTree reports the soap:Fault in an already-decoded response.
//
// It takes a decoded tree rather than a document because that is what the
// gateway holds: a SOAP operation's response is read as XML on the way out of
// the decoder, and parsing it a second time to ask one more question of it
// would be waste.
//
// The document is matched on local names, so the prefix the sender chose —
// soap, soapenv, S, env — never has to be guessed at.
func FaultFromTree(root *xmltree.Node) (Fault, bool) {
	if !isEnvelope(root) {
		return Fault{}, false
	}
	body := childByTag(root, "Body")
	if body == nil {
		return Fault{}, false
	}
	fault := childByTag(body, "Fault")
	if fault == nil {
		return Fault{}, false
	}
	return Fault{
		Code:   faultCode(fault),
		Reason: faultReason(fault),
		Detail: faultDetail(fault),
	}, true
}

// faultCode reads the code in whichever version's spelling the fault used.
func faultCode(fault *xmltree.Node) string {
	if v := text(childByTag(fault, "faultcode")); v != "" {
		return v
	}
	// SOAP 1.2 nests the code as Code/Value, and a subcode may nest further
	// still; the outermost value is the one that names the class of fault.
	return text(childByTag(childByTag(fault, "Code"), "Value"))
}

// faultReason reads the human-readable text in either version's spelling.
func faultReason(fault *xmltree.Node) string {
	if v := text(childByTag(fault, "faultstring")); v != "" {
		return v
	}
	reason := childByTag(fault, "Reason")
	if reason == nil {
		return ""
	}
	if v := text(childByTag(reason, "Text")); v != "" {
		return v
	}
	return text(reason)
}

// faultDetail reads the fault's detail element, which 1.1 spells detail and
// 1.2 spells Detail.
func faultDetail(fault *xmltree.Node) string {
	for _, tag := range []string{"detail", "Detail"} {
		if node := childByTag(fault, tag); node != nil {
			if rendered, err := xmltree.Encode(node, xmltree.DefaultLimits); err == nil {
				return rendered
			}
			return text(node)
		}
	}
	return ""
}

// isEnvelope reports whether a document's root is a SOAP envelope.
//
// The namespace is what makes an envelope a SOAP one, and checking it is what
// stops an unrelated document whose root happens to be named Envelope from
// being read as a fault. A document that declares no namespace at all is
// accepted, because some stacks emit one and the element names are then the
// only evidence there is; a document in a DIFFERENT namespace is refused,
// because there the sender has said what it is and it is not this.
func isEnvelope(n *xmltree.Node) bool {
	if n == nil || n.Tag != "Envelope" {
		return false
	}
	return n.NS == envelopeNS11 || n.NS == envelopeNS12 || n.NS == ""
}

// childByTag returns a node's first child with the given local name. A nil
// receiver answers nil so a two-level lookup reads as one expression.
func childByTag(n *xmltree.Node, tag string) *xmltree.Node {
	if n == nil {
		return nil
	}
	for _, c := range n.Children {
		if c.Tag == tag {
			return c
		}
	}
	return nil
}

// text returns a node's trimmed character data, empty when the node is absent.
func text(n *xmltree.Node) string {
	if n == nil {
		return ""
	}
	return strings.TrimSpace(n.Text)
}
