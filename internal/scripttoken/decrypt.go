package scripttoken

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"

	"go.starlark.net/starlark"

	"github.com/txn2/mcp-data-platform/internal/platform/starlarkconv"
)

// CapabilityDecrypt is the member a script reads a form's sensitive fields
// with (#2179): platform.decrypt(form, payload). It is answered here, beside
// the token members, because it is bound to the run the same way: the persona
// the run's author's roles resolve to is what the form's decrypt_personas is
// checked against.
const CapabilityDecrypt = "platform.decrypt"

// The keys of a decrypt's answer.
const (
	keyValues = "values"
	keyOpened = "opened"
)

// Redacted is what a recording keeps in place of each value platform.decrypt
// opened, so no recording, replay or test holds a sensitive value.
const Redacted = "[encrypted]"

// DecryptRequest is one platform.decrypt: the form named, the stored payload
// handed in, and who the run is.
type DecryptRequest struct {
	Form    string
	Payload map[string]any
	Persona string
	Admin   bool
	At      Binding
}

// Decrypter opens a form's sensitive fields for a run whose persona the form
// admits, recording the read.
type Decrypter interface {
	Decrypt(ctx context.Context, req DecryptRequest) (values map[string]any, opened []string, err error)
}

// SetDecrypter is what answers platform.decrypt from here on: the form
// sources, assembled after the issuer. Nil answers it with a refusal.
func (i *Issuer) SetDecrypter(d Decrypter) {
	if i == nil {
		return
	}
	i.decrypter.Store(&d)
}

func (b *bound) decrypt(ctx context.Context, args map[string]any) (map[string]any, error) {
	p := b.issuer.decrypter.Load()
	if p == nil || *p == nil {
		return nil, errors.New("this deployment stores no form submissions to decrypt")
	}
	form, _ := args["form"].(string)
	payload, _ := args["payload"].(map[string]any)
	values, opened, err := (*p).Decrypt(ctx, DecryptRequest{Form: form, Payload: payload, Persona: b.persona, Admin: b.admin, At: b.at})
	if err != nil {
		return nil, err //nolint:wrapcheck // the refusal names the form and the persona
	}
	return map[string]any{keyValues: values, keyOpened: opened}, nil
}

// Decrypt is platform.decrypt(form, payload): the values of a stored form
// submission with each sensitive field decrypted. payload is the payload
// column of webhook_form_<endpoint> as the script read it: the dict
// platform.query returns, or its JSON text. form is the form's endpoint name.
func (h Host) Decrypt(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var (
		form    string
		payload starlark.Value
	)
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "form", &form, "payload", &payload); err != nil {
		return nil, fmt.Errorf(inBinding, b.Name(), err)
	}
	if form == "" {
		return nil, fmt.Errorf("in %s: form is empty; name the form's endpoint", b.Name())
	}
	p, err := payloadOf(payload)
	if err != nil {
		return nil, fmt.Errorf("in %s: payload: %w", b.Name(), err)
	}
	out, err := h.callRedacted(CapabilityDecrypt, map[string]any{"form": form, "payload": p})
	if err != nil {
		return nil, fmt.Errorf(inBinding, b.Name(), err)
	}
	values, _ := out[keyValues].(map[string]any)
	v, err := starlarkconv.ResultToStarlark(values)
	if err != nil {
		return nil, fmt.Errorf(inBinding, b.Name(), err)
	}
	return v, nil
}

// callRedacted is call, recording the answer with every value it opened
// replaced by Redacted.
func (h Host) callRedacted(tool string, args map[string]any) (map[string]any, error) {
	rec := h.Record
	quiet := h
	quiet.Record = nil
	out, err := quiet.call(tool, args)
	if rec != nil && !h.Testing {
		rec(tool, args, redact(out), err)
	}
	return out, err
}

// redact is a decrypt answer as a recording keeps it.
func redact(out map[string]any) map[string]any {
	if out == nil {
		return nil
	}
	values, _ := out[keyValues].(map[string]any)
	opened := names(out[keyOpened])
	kept := maps.Clone(values)
	if kept == nil {
		kept = map[string]any{}
	}
	for _, name := range opened {
		if _, ok := kept[name]; ok {
			kept[name] = Redacted
		}
	}
	return map[string]any{keyValues: kept, keyOpened: opened}
}

// names reads the opened fields, a []string as answered or a []any once a
// recording's JSON round trip has been through it.
func names(v any) []string {
	switch list := v.(type) {
	case []string:
		return list
	case []any:
		out := make([]string, 0, len(list))
		for _, item := range list {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// payloadOf reads a stored payload handed in as a dict or as its JSON text.
func payloadOf(v starlark.Value) (map[string]any, error) {
	switch p := v.(type) {
	case *starlark.Dict:
		return starlarkconv.Arguments(p) //nolint:wrapcheck // named by the caller
	case starlark.String:
		var out map[string]any
		if err := json.Unmarshal([]byte(p), &out); err != nil {
			return nil, fmt.Errorf("the text is not a JSON object: %w", err)
		}
		return out, nil
	}
	return nil, fmt.Errorf("a payload is a dict or its JSON text, not a %s", v.Type())
}
