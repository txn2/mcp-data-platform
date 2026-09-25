// Package whevent turns one accepted webhook request into the events a source
// stores, and renders them as the JSON-lines a segment holds (#1870).
//
// An event is the envelope the receiver adds around what the sender posted:
// when it was received, which replica received it, the sender's id, type and
// key for it when the source says where to find them, and the posted JSON as
// text. The payload is never transformed: a reader extracts what it needs
// from it with json_extract_scalar, and a sender adding a field never changes
// the table.
package whevent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/url"
	"time"

	"github.com/txn2/mcp-data-platform/internal/webhook/jsonpath"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
)

// FormPayloadField is the form field a sender that posts
// application/x-www-form-urlencoded carries its JSON in.
const FormPayloadField = "payload"

// Refusals a body can meet. Each is answered 400 and nothing is stored.
var (
	ErrNotJSON       = errors.New("the body is not JSON")
	ErrNoPayload     = errors.New("the form has no payload field")
	ErrSplitNotArray = errors.New("the split path does not name an array in the body")
)

// Event is one stored event.
type Event struct {
	ReceivedAt  time.Time
	EventID     string
	EventType   string
	Key         string
	ContentHash string
	Replica     string
	// Payload is the event's JSON, compacted.
	Payload []byte
}

// Window is the start of the window of the given length the event is filed
// under: the window it was received in, in UTC.
func (e Event) Window(length time.Duration) time.Time { return e.ReceivedAt.UTC().Truncate(length) }

// Paths are a source's parsed JSON paths. A nil path is unset.
type Paths struct {
	Split, EventID, EventType, Key *jsonpath.Path
}

// ParsePaths reads the paths a source configures. Validation already refused
// a path that does not parse, so an error here is a source written around it.
func ParsePaths(c whsource.Config) (Paths, error) {
	var (
		out Paths
		err error
	)
	for _, f := range []struct {
		text string
		dst  **jsonpath.Path
	}{
		{c.Split, &out.Split},
		{c.EventIDPath, &out.EventID},
		{c.EventTypePath, &out.EventType},
		{c.KeyPath, &out.Key},
	} {
		if f.text == "" {
			continue
		}
		p, perr := jsonpath.Parse(f.text)
		if perr != nil {
			err = errors.Join(err, perr)
			continue
		}
		*f.dst = &p
	}
	if err != nil {
		return out, fmt.Errorf("parsing the source's paths: %w", err)
	}
	return out, nil
}

// JSONBody returns the JSON a request carries: the body itself, or the payload
// field of a form. contentType is the request's Content-Type.
func JSONBody(contentType string, body []byte) ([]byte, error) {
	mediaType, _, _ := mime.ParseMediaType(contentType)
	if mediaType != "application/x-www-form-urlencoded" {
		return body, nil
	}
	form, err := url.ParseQuery(string(body))
	if err != nil {
		return nil, ErrNoPayload
	}
	if !form.Has(FormPayloadField) {
		return nil, ErrNoPayload
	}
	return []byte(form.Get(FormPayloadField)), nil
}

// Build decodes a request's JSON and returns its events, all stamped with
// receivedAt and replica.
func Build(jsonBody []byte, paths Paths, receivedAt time.Time, replica string) ([]Event, error) {
	dec := json.NewDecoder(bytes.NewReader(jsonBody))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return nil, ErrNotJSON
	}
	if dec.More() {
		return nil, ErrNotJSON
	}

	items := []any{doc}
	if paths.Split != nil {
		v, ok := paths.Split.Lookup(doc)
		arr, isArr := v.([]any)
		if !ok || !isArr {
			return nil, ErrSplitNotArray
		}
		items = arr
	}
	out := make([]Event, 0, len(items))
	for _, item := range items {
		ev, err := buildOne(item, paths, receivedAt, replica)
		if err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, nil
}

// buildOne renders one decoded item as an event.
func buildOne(item any, paths Paths, receivedAt time.Time, replica string) (Event, error) {
	payload, err := marshalCompact(item)
	if err != nil {
		return Event{}, err
	}
	sum := sha256.Sum256(payload)
	hash := hex.EncodeToString(sum[:])
	ev := Event{
		ReceivedAt:  receivedAt,
		EventType:   lookup(paths.EventType, item),
		Key:         lookup(paths.Key, item),
		ContentHash: hash,
		Replica:     replica,
		Payload:     payload,
		EventID:     lookup(paths.EventID, item),
	}
	if ev.EventID == "" {
		ev.EventID = hash
	}
	return ev, nil
}

// marshalCompact renders a decoded value as compact JSON with its numbers as
// written. Object keys come out sorted, which is what makes the content hash
// of two deliveries of one event agree however the sender ordered its keys.
func marshalCompact(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("encoding event payload: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// lookup reads a path's value as column text, or "" when the path is unset or
// finds nothing.
func lookup(p *jsonpath.Path, item any) string {
	if p == nil {
		return ""
	}
	v, ok := p.Lookup(item)
	if !ok {
		return ""
	}
	return jsonpath.Scalar(v)
}
