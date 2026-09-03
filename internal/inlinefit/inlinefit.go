// Package inlinefit renders a tool result within a budget on the
// rendered result rather than on the bytes a handler read.
//
// A budget applied to an upstream read does not bound what the client
// receives. Between the two sit the JSON envelope the handler wraps the
// body in and the indentation the result is rendered with, and a parsed
// JSON body re-rendered indented is several times the size of the
// compact bytes it was read from. Issue #1606 measured a 26,809-byte
// JSON response, comfortably inside a 32 KiB read budget and therefore
// carrying no truncation flag and no steer, render as a 64,238-character
// tool result, past the size the client refuses.
//
// Fitting spends the cheapest lever first. The indented rendering is
// returned when it fits. Otherwise the compact one is, because
// indentation is whitespace and dropping it costs the caller nothing
// where the alternative is dropping content. Only when neither fits is
// the body cut, which is the case a caller flags and steers to a
// streamed export.
//
// What is measured is the result's text, which is what issue #1606
// measured a client refusing. The MCP SDK also marshals the same value
// into the result's structured output, so the wire message carries the
// body a second time, compactly; a client that counts the whole message
// rather than the text it renders sees roughly twice the budget. That
// copy is not removable here -- a managed script reads the structured
// output, and the call reference the platform stamps rides on it -- so
// it is deliberately outside this budget rather than unaccounted for.
package inlinefit

import (
	"encoding/json"
	"fmt"
	"math"

	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// DefaultReserve is the headroom a merge keeps under a budget for the
// envelope its collection is wrapped in: the status, the headers, the
// pagination signal, the hint and any echoed arguments. A merge that
// admits whole pages has to leave room before accepting one rather than
// cut afterwards, since cutting would leave a resume signal pointing
// past content the caller never received.
const DefaultReserve = int64(4096)

// maxAttempts bounds the bisection below. The rendered size is not a
// linear function of the body kept -- a parsed body renders to several
// times its text, and the text is escaped back into it -- so the largest
// prefix that fits is searched for rather than computed, and 16 steps
// resolve a body of tens of kilobytes to within a few bytes.
const maxAttempts = 16

// Render renders v the way a tool result carries it.
func Render(v any) ([]byte, error) {
	b, err := toolkit.MarshalResultJSON(v)
	if err != nil {
		return nil, fmt.Errorf("inlinefit: rendering the result: %w", err)
	}
	return b, nil
}

// RenderWithin renders v within budget by the cheapest lever that works:
// the indented rendering when it fits, the compact one when it does not.
// The bool reports whether the returned rendering is within the budget.
// A budget of zero or less fits everything.
func RenderWithin(v any, budget int) ([]byte, bool) {
	indented, err := Render(v)
	if err != nil {
		return nil, false
	}
	if budget <= 0 || len(indented) <= budget {
		return indented, true
	}
	compact, err := json.Marshal(v)
	if err != nil {
		return indented, false
	}
	return compact, len(compact) <= budget
}

// NeedsCut reports whether v can be rendered within budget without
// dropping content. A caller sets its truncation flags from this before
// calling Fit, so the rendering Fit measures is the one it returns.
func NeedsCut(v any, budget int) bool {
	_, ok := RenderWithin(v, budget)
	return !ok
}

// Fit renders v within budget, shortening body through setBody when
// re-encoding alone is not enough, and returns the rendering to hand
// back. The body kept is the longest prefix found that fits.
func Fit(v any, budget int, body string, setBody func(string)) []byte {
	text, ok := RenderWithin(v, budget)
	if ok || budget <= 0 || setBody == nil {
		return text
	}
	lo, hi, best := 0, len(body), -1
	var bestText []byte
	for range maxAttempts {
		if lo > hi {
			break
		}
		mid := (lo + hi) / 2
		setBody(body[:mid])
		if t, fits := RenderWithin(v, budget); fits {
			best, bestText, lo = mid, t, mid+1
		} else {
			hi = mid - 1
		}
	}
	if best < 0 {
		setBody("")
		text, _ = RenderWithin(v, budget)
		return text
	}
	setBody(body[:best])
	return bestText
}

// BodyText is a response body as the text a cut body carries: a string
// body is itself, and a parsed value is its compact JSON, the form the
// upstream sent it in. An absent body has no text, rather than the
// "null" a marshal would give it, so a fit cannot invent one.
func BodyText(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// ItemsSize measures a merged collection the way a result renders it, so
// a merge stops at a page boundary that keeps the whole result inside
// the budget instead of at a count of compact bytes. A collection that
// cannot be rendered measures as unbounded, so a merge refuses it rather
// than admitting it under a cap that could not be checked.
func ItemsSize(items []json.RawMessage) int64 {
	b, err := Render(items)
	if err != nil {
		return math.MaxInt64
	}
	return int64(len(b))
}

// Reserve is the envelope headroom to keep under a budget, capped at a
// quarter of it so a small budget still merges pages instead of having
// its first one refused by a reserve larger than the budget itself.
func Reserve(limit int64) int64 {
	return min(DefaultReserve, limit/4)
}
