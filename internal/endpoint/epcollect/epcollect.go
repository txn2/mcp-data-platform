// Package epcollect stores a form's accepted submissions (#2178): each one
// becomes one event in the webhook source the platform made for the form, so
// it lands in webhook_{name} the way any source's events do, with that
// source's retention. A sensitive field is encrypted here, before the event
// reaches the receiver's spool, so the spool file, the raw segment, the
// compacted Parquet and the view all hold its ciphertext (#2179). The
// submitter is the identity the platform found for the request, never a form
// field.
package epcollect

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"time"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epform"
	"github.com/txn2/mcp-data-platform/internal/webhook/whevent"
)

// EventType is the event type of every form submission.
const EventType = "form_submission"

// The keys of a submission's stored payload.
const (
	KeyEndpoint  = "endpoint"
	KeyVersion   = "version"
	KeyValues    = "values"
	KeyFields    = "fields"
	KeySensitive = "sensitive"
	KeySubmitter = "submitter"
)

// eventIDBytes is how many random bytes a submission's event id holds.
const eventIDBytes = 16

// ErrNoSource is a form whose source this replica cannot find: the form was
// bound before it defined both handlers, or its source was deleted or
// disabled.
var ErrNoSource = errors.New("this form has no enabled webhook source to store its submissions in; bind it again with manage_script endpoint_set")

// ErrCannotSeal is a form declaring a sensitive field on a deployment that
// cannot encrypt it.
var ErrCannotSeal = errors.New("the form declares sensitive fields, which are encrypted when stored, and this deployment has no ENCRYPTION_KEY")

// Submitter is who sent a submission to a signed-in form.
type Submitter struct {
	UserID   string `json:"user_id"`
	Email    string `json:"email"`
	AuthType string `json:"auth_type,omitempty"`
}

// Submission is one accepted submission: the form, the spec its token
// carried, the values that passed it, and who sent it (nil on a public form).
type Submission struct {
	ScriptID  string
	Endpoint  string
	Version   int
	Spec      epform.Spec
	Values    map[string]any
	Submitter *Submitter
}

// Collector stores accepted submissions.
type Collector struct {
	Sources Sources
	Writer  Writer
	Sealer  Sealer
	// Now is the clock a submission is received by; nil is time.Now.
	Now func() time.Time
}

// CanSeal reports whether a form's sensitive fields can be encrypted here.
func (c *Collector) CanSeal() bool {
	return c != nil && c.Sealer != nil && c.Sealer.Enabled()
}

// Store writes sub to its form's source and returns once it is durable.
func (c *Collector) Store(ctx context.Context, sub Submission) error {
	src, ok := c.Sources.FormSource(ctx, sub.ScriptID)
	if !ok {
		return ErrNoSource
	}
	event, body, err := c.Event(sub)
	if err != nil {
		return err
	}
	if err := c.Writer.Deliver(ctx, src, event, body); err != nil {
		return fmt.Errorf("storing the submission in webhook source %s: %w", src.Name, err)
	}
	return nil
}

// Event is the event sub is stored as, and the payload's JSON: its values
// with each sensitive one sealed, the fields in the order the spec declared
// them, the sensitive fields' names, and the submitter.
func (c *Collector) Event(sub Submission) (whevent.Event, []byte, error) {
	sensitive := sub.Spec.Sensitive()
	values, err := c.seal(sub.Values, sensitive)
	if err != nil {
		return whevent.Event{}, nil, err
	}
	fields := make([]string, 0, len(sub.Spec.Fields))
	for _, f := range sub.Spec.Fields {
		fields = append(fields, f.Name)
	}
	payload := map[string]any{KeyEndpoint: sub.Endpoint, KeyVersion: sub.Version, KeyValues: values, KeyFields: fields}
	if len(sensitive) > 0 {
		payload[KeySensitive] = sensitive
	}
	key := ""
	if sub.Submitter != nil {
		payload[KeySubmitter] = sub.Submitter
		key = sub.Submitter.Email
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return whevent.Event{}, nil, fmt.Errorf("encoding the submission: %w", err)
	}
	id, err := newEventID()
	if err != nil {
		return whevent.Event{}, nil, err
	}
	return whevent.Event{
		ReceivedAt: c.now().UTC(), EventID: id, EventType: EventType, Key: key,
		ContentHash: contentHash(body), Payload: body,
	}, body, nil
}

// seal is values with each sensitive one replaced by the ciphertext of its
// JSON, so a number, a box or a list of choices reads back as what it was.
func (c *Collector) seal(values map[string]any, sensitive []string) (map[string]any, error) {
	if len(sensitive) == 0 {
		return values, nil
	}
	if !c.CanSeal() {
		return nil, ErrCannotSeal
	}
	out := maps.Clone(values)
	for _, name := range sensitive {
		plain, err := json.Marshal(values[name])
		if err != nil {
			return nil, fmt.Errorf("encoding sensitive field %q: %w", name, err)
		}
		sealed, err := c.Sealer.Seal(string(plain))
		if err != nil {
			return nil, fmt.Errorf("encrypting sensitive field %q: %w", name, err)
		}
		out[name] = sealed
	}
	return out, nil
}

// Reveal is values with each sensitive field opened: what Seal wrote, read
// back as the value it was. A field that is not a sealed string is refused,
// since a sensitive field was never stored any other way.
func Reveal(s Sealer, values map[string]any, sensitive []string) (map[string]any, error) {
	if len(sensitive) == 0 {
		return values, nil
	}
	if s == nil || !s.Enabled() {
		return nil, errors.New("this deployment has no ENCRYPTION_KEY to decrypt sensitive fields with")
	}
	out := maps.Clone(values)
	for _, name := range sensitive {
		v, ok := values[name]
		if !ok {
			continue
		}
		sealed, isString := v.(string)
		if !isString {
			return nil, fmt.Errorf("sensitive field %q is not encrypted", name)
		}
		plain, err := s.Open(sealed)
		if err != nil {
			return nil, fmt.Errorf("decrypting sensitive field %q: %w", name, err)
		}
		var decoded any
		if err := json.Unmarshal([]byte(plain), &decoded); err != nil {
			return nil, fmt.Errorf("reading sensitive field %q: %w", name, err)
		}
		out[name] = decoded
	}
	return out, nil
}

func (c *Collector) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// contentHash is the SHA-256 of a payload, as the receiver hashes every
// event's.
func contentHash(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func newEventID() (string, error) {
	b := make([]byte, eventIDBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating the submission's id: %w", err)
	}
	return hex.EncodeToString(b), nil
}
