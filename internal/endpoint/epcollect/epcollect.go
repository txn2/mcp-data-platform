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
	"strings"
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
	// EventID and ReceivedAt name the event a submission that carries files
	// is stored as, chosen by Prepare so its files' addresses are known
	// before form_submit runs; empty, Event chooses them.
	EventID    string
	ReceivedAt time.Time
	// Files are the submission's files, saved by Store before its event
	// (#2333).
	Files []File
}

// Collector stores accepted submissions.
type Collector struct {
	Sources Sources
	Writer  Writer
	Sealer  Sealer
	// Files saves a submission's files (#2333); nil saves none, and a form
	// with a file field is refused.
	Files Files
	// MaxFileBytes is the most a saved file may hold
	// (resources.managed.max_upload_bytes); zero bounds none here.
	MaxFileBytes int64
	// Now is the clock a submission is received by; nil is time.Now.
	Now func() time.Time
}

// CanSeal reports whether a form's sensitive fields can be encrypted here.
func (c *Collector) CanSeal() bool {
	return c != nil && c.Sealer != nil && c.Sealer.Enabled()
}

// Store writes sub to its form's source and returns once it is durable. Its
// files are saved first, and removed again when the event is not stored, so
// a submission that was not stored keeps none of them.
func (c *Collector) Store(ctx context.Context, sub Submission) error {
	src, ok := c.Sources.FormSource(ctx, sub.ScriptID)
	if !ok {
		return ErrNoSource
	}
	var saved []string
	if len(sub.Files) > 0 {
		if !c.CanSaveFiles() {
			return ErrCannotSaveFiles
		}
		ids, err := c.saveFiles(ctx, src, sub)
		if err != nil {
			return err
		}
		saved = ids
	}
	event, body, err := c.Event(sub, src.Name)
	if err == nil {
		err = c.Writer.Deliver(ctx, src, event, body)
		if err != nil {
			err = fmt.Errorf("storing the submission in webhook source %s: %w", src.Name, err)
		}
	}
	if err != nil {
		c.removeFiles(ctx, saved)
		return err
	}
	return nil
}

// Event is the event sub is stored as in the form's source named source, and
// the payload's JSON: its values with each sensitive one sealed bound to that
// source and its field, the fields in the order the spec declared them, the
// sensitive fields' names, and the submitter.
func (c *Collector) Event(sub Submission, source string) (whevent.Event, []byte, error) {
	sensitive := sub.Spec.Sensitive()
	values, err := c.seal(source, sub.Values, sensitive)
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
	id, at := sub.EventID, sub.ReceivedAt
	if id == "" {
		if id, err = newEventID(); err != nil {
			return whevent.Event{}, nil, err
		}
	}
	if at.IsZero() {
		at = c.now().UTC()
	}
	return whevent.Event{
		ReceivedAt: at, EventID: id, EventType: EventType, Key: key,
		ContentHash: contentHash(body), Payload: body,
	}, body, nil
}

// seal is values with each sensitive one replaced by the ciphertext of its
// JSON, so a number, a box or a list of choices reads back as what it was,
// bound to the form's source and the field.
func (c *Collector) seal(source string, values map[string]any, sensitive []string) (map[string]any, error) {
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
		sealed, err := c.Sealer.SealBound(Binding(source, name), string(plain))
		if err != nil {
			return nil, fmt.Errorf("encrypting sensitive field %q: %w", name, err)
		}
		out[name] = sealed
	}
	return out, nil
}

// Binding is what a sensitive field of the form whose source is named source
// is sealed bound to (#2355).
func Binding(source, field string) string {
	return "form:" + source + "\x00field:" + field
}

// Reveal is values with each sensitive field opened: what the form whose
// source is named source sealed, read back as the value it was. Only a value
// sealed bound to that form and field opens, so a payload a script assembles
// opens nothing sealed for anything else (#2355). A field that is not a
// sealed string is refused, since a sensitive field was never stored any
// other way.
func Reveal(s Sealer, source string, values map[string]any, sensitive []string) (map[string]any, error) {
	return openFields(s, source, values, sensitive, false)
}

// SealedUnbound stands in the owner's Submissions view for a sensitive value
// sealed before fields were bound to their form (#2355). It is never
// decrypted: a value sealed without its form is indistinguishable from any
// other ciphertext the platform holds, which a row written into the form's
// table could carry.
const SealedUnbound = "[sealed before this release; not readable]"

// RevealStored is Reveal for rows read from the form's own source: a value
// sealed before fields were bound reads as SealedUnbound rather than failing
// the row.
func RevealStored(s Sealer, source string, values map[string]any, sensitive []string) (map[string]any, error) {
	return openFields(s, source, values, sensitive, true)
}

// openFields opens each of sensitive in values; with unbound, a value sealed
// before fields were bound to their form reads as SealedUnbound.
func openFields(s Sealer, source string, values map[string]any, sensitive []string, unbound bool) (map[string]any, error) {
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
		decoded, err := openField(s, source, name, v, unbound)
		if err != nil {
			return nil, err
		}
		out[name] = decoded
	}
	return out, nil
}

// openField opens one sensitive field's stored value v.
func openField(s Sealer, source, name string, v any, unbound bool) (any, error) {
	sealed, isString := v.(string)
	if !isString {
		return nil, fmt.Errorf("sensitive field %q is not encrypted", name)
	}
	if unbound && strings.HasPrefix(sealed, unboundPrefix) {
		return SealedUnbound, nil
	}
	plain, err := s.OpenBound(Binding(source, name), sealed)
	if err != nil {
		return nil, fmt.Errorf("sensitive field %q was not sealed by this form, or not with a key this deployment holds", name)
	}
	var decoded any
	if json.Unmarshal([]byte(plain), &decoded) != nil {
		// The decoder's error quotes what it read; this one never does.
		return nil, fmt.Errorf("sensitive field %q does not hold a submitted value", name)
	}
	return decoded, nil
}

// unboundPrefix begins a value sealed before fields were bound to their form.
const unboundPrefix = "enc:"

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
