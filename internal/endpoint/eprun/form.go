package eprun

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"go.starlark.net/starlark"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epform"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptdialect"
	"github.com/txn2/mcp-data-platform/internal/platform/starlarkconv"
)

// The keys a form's answers carry.
const (
	keyErrors       = "errors"
	keyConfirmation = "confirmation"
	keyRedirect     = "redirect"
)

// invocation is the function a request calls, the arguments it is called
// with, and how what it returns becomes the response.
type invocation struct {
	name    string
	args    starlark.Tuple
	respond func(starlark.Value) (Response, error)
}

// invocationFor is what t calls for req (#2176): handle(req) on a script that
// is not a form; on a form, form_spec(req) on a GET or HEAD and form_submit(
// req, values) on anything else, once the submission passed the spec its
// token carries. A submission refused before the script runs is answered
// with the response returned in place of an invocation.
func (r *Runner) invocationFor(t *Target, req Request, reqValue starlark.Value) (*invocation, *Response) {
	if !t.form {
		return &invocation{name: scriptdialect.HandlerName, args: starlark.Tuple{reqValue}, respond: handlerResponse}, nil
	}
	if req.Method == http.MethodGet || req.Method == http.MethodHead {
		return &invocation{name: epform.SpecName, args: starlark.Tuple{reqValue}, respond: r.specResponse(t)}, nil
	}
	spec, values, refusal := r.checkSubmission(t, req)
	if refusal != nil {
		return nil, refusal
	}
	sv, err := starlarkconv.ToStarlark(values)
	if err != nil {
		resp := errorResponse(http.StatusBadRequest, "the submission could not be read")
		return nil, &resp
	}
	return &invocation{name: epform.SubmitName, args: starlark.Tuple{reqValue, sv}, respond: submitResponse(spec)}, nil
}

// handlerResponse is what handle(req) returned as the response, or 500 for
// something that is not one.
func handlerResponse(v starlark.Value) (Response, error) {
	resp, err := responseFrom(v)
	if err != nil {
		return errorResponse(http.StatusInternalServerError, "the endpoint answered with something that is not a response"), err
	}
	return resp, nil
}

// checkSubmission is the spec a submission's token carries and its checked
// values, or the response that refuses it: 400 for one refused whole, 422
// with the fields it got wrong.
func (r *Runner) checkSubmission(t *Target, req Request) (epform.Spec, map[string]any, *Response) {
	submitted, err := epform.Values(req.Header.Get(headerContentType), req.Body)
	if err != nil {
		resp := errorResponse(http.StatusBadRequest, err.Error())
		return epform.Spec{}, nil, &resp
	}
	now := r.now()
	token := ""
	if v := submitted[epform.TokenField]; len(v) > 0 {
		token = v[0]
	}
	spec, issued, err := epform.Open(r.FormKey, t.Binding.Name, token, now)
	if err != nil {
		resp := errorResponse(http.StatusBadRequest, err.Error())
		return epform.Spec{}, nil, &resp
	}
	values, fieldErrs, err := epform.Check(spec, issued, submitted, now)
	switch {
	case err != nil:
		resp := errorResponse(http.StatusBadRequest, err.Error())
		return epform.Spec{}, nil, &resp
	case len(fieldErrs) > 0:
		resp := jsonResponse(http.StatusUnprocessableEntity, map[string]any{keyErrors: fieldErrs})
		return epform.Spec{}, nil, &resp
	}
	return spec, values, nil
}

// specResponse answers a GET with the spec form_spec returned, checked, and
// the token a submission carries it back in.
func (r *Runner) specResponse(t *Target) func(starlark.Value) (Response, error) {
	return func(v starlark.Value) (Response, error) {
		spec, err := epform.SpecFrom(v)
		if err != nil {
			return errorResponse(http.StatusInternalServerError, "the form's spec is not one this platform renders"), err
		}
		token, err := epform.Sign(r.FormKey, t.Binding.Name, spec, r.now())
		if err != nil {
			return errorResponse(http.StatusInternalServerError, "the form could not be signed"), err
		}
		return jsonResponse(http.StatusOK, map[string]any{
			"spec": spec, "form_token": token, "token_field": epform.TokenField, "honeypot_field": epform.HoneypotField,
		}), nil
	}
}

// submitResponse answers a submission with what form_submit returned: None
// accepts it with the spec's confirmation and redirect; {"errors": {...}}
// refuses it field by field (422); {"confirmation": ..., "redirect": ...}
// accepts it with its own.
func submitResponse(spec epform.Spec) func(starlark.Value) (Response, error) {
	return func(v starlark.Value) (Response, error) {
		accepted := map[string]any{"ok": true, keyConfirmation: spec.Confirmation, keyRedirect: spec.Redirect}
		if v == starlark.None {
			return jsonResponse(http.StatusOK, accepted), nil
		}
		d, ok := v.(*starlark.Dict)
		if !ok {
			return errorResponse(http.StatusInternalServerError, "the form failed"),
				fmt.Errorf("the submit handler %s(req, values) returned %s; it returns None or a dict of errors, confirmation and redirect", epform.SubmitName, v.Type())
		}
		plain, err := starlarkconv.FromStarlark(d)
		if err != nil {
			return errorResponse(http.StatusInternalServerError, "the form failed"), fmt.Errorf("reading what %s returned: %w", epform.SubmitName, err)
		}
		return acceptedOrRefused(accepted, plain)
	}
}

// acceptedOrRefused reads form_submit's dict: errors refuse the submission,
// confirmation and redirect replace the spec's.
func acceptedOrRefused(accepted map[string]any, plain any) (Response, error) {
	m, _ := plain.(map[string]any)
	if errs, ok := m[keyErrors].(map[string]any); ok && len(errs) > 0 {
		return jsonResponse(http.StatusUnprocessableEntity, map[string]any{keyErrors: errs}), nil
	}
	for _, key := range []string{keyConfirmation, keyRedirect} {
		if v, ok := m[key].(string); ok {
			accepted[key] = v
		}
	}
	for key := range m {
		if key != keyErrors && key != keyConfirmation && key != keyRedirect {
			return errorResponse(http.StatusInternalServerError, "the form failed"),
				fmt.Errorf("the submit handler %s(req, values) returned the key %q; it returns errors, confirmation and redirect", epform.SubmitName, key)
		}
	}
	return jsonResponse(http.StatusOK, accepted), nil
}

// jsonResponse is v as a JSON answer.
func jsonResponse(status int, v any) Response {
	body, err := json.Marshal(v)
	if err != nil {
		return errorResponse(http.StatusInternalServerError, "the endpoint could not answer")
	}
	return Response{Status: status, Header: http.Header{headerContentType: {mediaJSON}}, Body: body}
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// errNoForm is a form request on a deployment with no key to sign its token.
var errNoForm = errors.New("this deployment cannot sign a form token: it needs auth.browser_session.signing_key")
