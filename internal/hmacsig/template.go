package hmacsig

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Placeholders a signed-bytes template names (#2223).
const (
	PlaceholderID        = "{id}"
	PlaceholderTimestamp = "{timestamp}"
	PlaceholderBody      = "{body}"
)

// Placeholders a header pattern names (#2223).
const (
	PlaceholderSignature = "{signature}"
)

// The named signed forms written as templates. Each produces the bytes its
// name always produced.
const (
	templateBody            = PlaceholderBody
	templateTimestampBody   = PlaceholderTimestamp + "." + PlaceholderBody
	templateIDTimestampBody = PlaceholderID + "." + PlaceholderTimestamp + "." + PlaceholderBody
)

// segKind is what one segment of a template or pattern stands for.
type segKind uint8

const (
	segLiteral segKind = iota
	segID
	segTimestamp
	segBody
	segSignature
)

// segment is literal text or one placeholder.
type segment struct {
	kind segKind
	text string
}

// ErrTemplate is returned for a template or pattern that cannot be compiled.
var ErrTemplate = errors.New("invalid template")

// Template is the bytes a scheme signs, compiled from text such as
// "v0:{timestamp}:{body}" into literal and placeholder segments. Nothing about
// it is parsed per request.
type Template struct {
	segs []segment
	text string
	// bodyOnly is a template that signs the body alone, written straight
	// through.
	bodyOnly bool
}

// CompileTemplate reads a signed-bytes template, or one of the named forms
// body, timestamp.body and id.timestamp.body. {body} is required, since a
// signature that does not cover the body proves nothing about it; each
// placeholder appears at most once.
func CompileTemplate(text string) (Template, error) {
	switch text {
	case SignedBody:
		text = templateBody
	case SignedTimestampBody:
		text = templateTimestampBody
	case SignedIDTimestampBody:
		text = templateIDTimestampBody
	}
	segs, err := compileSegments(text, map[string]segKind{
		PlaceholderID: segID, PlaceholderTimestamp: segTimestamp, PlaceholderBody: segBody,
	}, false)
	if err != nil {
		return Template{}, err
	}
	if !hasKind(segs, segBody) {
		return Template{}, fmt.Errorf("template %q does not name {body}: %w", text, ErrTemplate)
	}
	return Template{segs: segs, text: text, bodyOnly: len(segs) == 1}, nil
}

// String is the template as text, the named forms written out.
func (t Template) String() string { return t.text }

// UsesID reports whether the signed bytes include the delivery id.
func (t Template) UsesID() bool { return hasKind(t.segs, segID) }

// UsesTimestamp reports whether the signed bytes include the timestamp.
func (t Template) UsesTimestamp() bool { return hasKind(t.segs, segTimestamp) }

// scratchBytes is the room WriteTo holds for the text around the body without
// growing: every sender's id, timestamp and separators fit in it.
const scratchBytes = 128

// WriteTo writes the signed bytes to w, so the body is never copied to put
// text in front of it. The text around the body is gathered into one buffer,
// allocated once per call and reused after the body, rather than written a
// segment at a time: a short write of a string to a hash copies it.
func (t Template) WriteTo(w io.Writer, id, timestamp string, body []byte) {
	if t.bodyOnly {
		_, _ = w.Write(body)
		return
	}
	var pending []byte
	for _, s := range t.segs {
		var text string
		switch s.kind {
		case segLiteral:
			text = s.text
		case segID:
			text = id
		case segTimestamp:
			text = timestamp
		case segBody:
			if len(pending) > 0 {
				_, _ = w.Write(pending)
				pending = pending[:0]
			}
			_, _ = w.Write(body)
			continue
		}
		if pending == nil {
			pending = make([]byte, 0, scratchBytes)
		}
		pending = append(pending, text...)
	}
	if len(pending) > 0 {
		_, _ = w.Write(pending)
	}
}

// Bytes is the signed bytes as one slice, for a verifier that needs the whole
// message rather than a stream (Ed25519).
func (t Template) Bytes(id, timestamp string, body []byte) []byte {
	b := bytes.NewBuffer(make([]byte, 0, len(body)))
	t.WriteTo(b, id, timestamp, body)
	return b.Bytes()
}

// Pattern is a signature header's layout, compiled from text such as
// "{timestamp}|{signature}" (#2223). It is read by cutting the value at the
// pattern's literal separators; no regular expression runs.
type Pattern struct {
	segs []segment
	text string
}

// CompilePattern reads a header pattern. {signature} is required and
// {timestamp} optional; no other placeholder is allowed, neither may repeat,
// and two placeholders need literal text between them so a value can be cut.
func CompilePattern(text string) (Pattern, error) {
	segs, err := compileSegments(text, map[string]segKind{
		PlaceholderSignature: segSignature, PlaceholderTimestamp: segTimestamp,
	}, true)
	if err != nil {
		return Pattern{}, err
	}
	if !hasKind(segs, segSignature) {
		return Pattern{}, fmt.Errorf("pattern %q does not name {signature}: %w", text, ErrTemplate)
	}
	return Pattern{segs: segs, text: text}, nil
}

// UsesTimestamp reports whether the header carries the timestamp.
func (p Pattern) UsesTimestamp() bool { return hasKind(p.segs, segTimestamp) }

// Write lays out a header value.
func (p Pattern) Write(signature, timestamp string) string {
	parts := make([]string, 0, len(p.segs))
	for _, s := range p.segs {
		switch s.kind {
		case segSignature:
			parts = append(parts, signature)
		case segTimestamp:
			parts = append(parts, timestamp)
		default:
			parts = append(parts, s.text)
		}
	}
	return strings.Join(parts, "")
}

// Read cuts a header value into its signature and timestamp, and reports
// false for a value that does not have the pattern's layout.
func (p Pattern) Read(value string) (signature, timestamp string, ok bool) {
	rest := value
	for i := 0; i < len(p.segs); i++ {
		s := p.segs[i]
		if s.kind == segLiteral {
			if rest, ok = strings.CutPrefix(rest, s.text); !ok {
				return "", "", false
			}
			continue
		}
		var v string
		if v, rest, ok = p.cutValue(rest, i); !ok {
			return "", "", false
		}
		if s.kind == segSignature {
			signature = v
		} else {
			timestamp = v
		}
		i++ // cutValue consumed the literal that follows, if there is one
	}
	return signature, timestamp, rest == ""
}

// cutValue reads the placeholder at segment i from the front of rest: up to
// the literal that follows it, which it consumes, or the whole of rest for the
// last segment. compileSegments guarantees a literal follows a placeholder
// that is not last. An empty value does not have the layout.
func (p Pattern) cutValue(rest string, i int) (value, after string, ok bool) {
	if i+1 < len(p.segs) {
		value, after, ok = strings.Cut(rest, p.segs[i+1].text)
	} else {
		value, after, ok = rest, "", true
	}
	return value, after, ok && value != ""
}

// compileSegments splits text into literals and the placeholders allowed.
// Every "{" must open an allowed placeholder, none may repeat, and with
// separated set two placeholders may not be adjacent.
func compileSegments(text string, allowed map[string]segKind, separated bool) ([]segment, error) {
	if text == "" {
		return nil, fmt.Errorf("the text is empty: %w", ErrTemplate)
	}
	var segs []segment
	seen := map[segKind]bool{}
	rest := text
	for rest != "" {
		open := strings.IndexByte(rest, '{')
		if open < 0 {
			segs = append(segs, segment{kind: segLiteral, text: rest})
			break
		}
		if open > 0 {
			segs = append(segs, segment{kind: segLiteral, text: rest[:open]})
		}
		kind, width, err := placeholderAt(text, rest[open:], allowed)
		if err != nil {
			return nil, err
		}
		if seen[kind] {
			return nil, fmt.Errorf("text %q names %s more than once: %w", text, rest[open:open+width], ErrTemplate)
		}
		if separated && len(segs) > 0 && segs[len(segs)-1].kind != segLiteral {
			return nil, fmt.Errorf("text %q has two placeholders with no text between them: %w", text, ErrTemplate)
		}
		seen[kind] = true
		segs = append(segs, segment{kind: kind})
		rest = rest[open+width:]
	}
	return segs, nil
}

// placeholderAt reads the placeholder at the front of rest, which begins with
// "{", and returns what it stands for and how many bytes it spans. text is the
// whole template, which a refusal names.
func placeholderAt(text, rest string, allowed map[string]segKind) (segKind, int, error) {
	closing := strings.IndexByte(rest, '}')
	if closing < 0 {
		return 0, 0, fmt.Errorf("text %q has a { with no }: %w", text, ErrTemplate)
	}
	name := rest[:closing+1]
	kind, ok := allowed[name]
	if !ok {
		return 0, 0, fmt.Errorf("text %q names %s, which is not one of %s: %w", text, name, names(allowed), ErrTemplate)
	}
	return kind, closing + 1, nil
}

// names lists the allowed placeholders in a fixed order, for a refusal.
func names(allowed map[string]segKind) string {
	var out []string
	for _, n := range []string{PlaceholderID, PlaceholderTimestamp, PlaceholderBody, PlaceholderSignature} {
		if _, ok := allowed[n]; ok {
			out = append(out, n)
		}
	}
	return strings.Join(out, ", ")
}

func hasKind(segs []segment, k segKind) bool {
	for _, s := range segs {
		if s.kind == k {
			return true
		}
	}
	return false
}
