package eprun

import (
	"context"
	"net/http"

	"go.starlark.net/starlark"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epform"
	"github.com/txn2/mcp-data-platform/internal/platform/starlarkconv"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// PreviewPrefix opens the name a preview's form token is bound to. An
// endpoint's name never starts with "_" (epbinding), so a token a preview
// signed never opens at an endpoint, nor one an endpoint signed at a
// preview.
const PreviewPrefix = "_preview-"

// Executor calls one of a preview's functions as a draft of its script, under
// the identity of the person previewing it (internal/platform/scriptdraft):
// their persona authorizes every host call, and the draft's write barrier
// stops every write.
type Executor func(ctx context.Context, name string, args starlark.Tuple) (starlark.Value, error)

// Preview is one form as its owner previews it (#2494): a saved version or an
// unsaved source, with no endpoint bound to it.
type Preview struct {
	// ID is the preview's id; its form tokens are bound to PreviewPrefix+ID.
	ID      string
	Script  *script.Script
	Version int
	Source  string
	Exec    Executor
}

// ServePreview answers one request to a form's preview the way its endpoint
// would answer it, with three differences: form_spec and form_submit run as
// drafts under the previewer's identity; an accepted submission is answered
// and not stored, and its files are described to form_submit and not saved;
// and nothing is recorded, neither the endpoint's audit row and metrics nor a
// failed request. A failure is returned beside the response so the preview
// can show its author what went wrong, its sensitive values replaced.
func (r *Runner) ServePreview(ctx context.Context, p Preview, req Request) (Response, error) {
	t := &Target{
		Binding: &epbinding.Binding{Name: PreviewPrefix + p.ID, AuthMode: epbinding.AuthAuthenticated},
		Script:  p.Script, Version: &script.Version{Version: p.Version, Source: p.Source},
		Form: epform.DefinesSource(p.Source),
	}
	switch {
	case !t.Form:
		return errorResponse(http.StatusUnprocessableEntity, "this source is not a form: a form defines form_spec(req) and form_submit(req, values)"), nil
	case r.FormKey == nil:
		return errorResponse(http.StatusServiceUnavailable, "this form is not available"), errNoForm
	}
	reqValue, err := requestValue(req)
	if err != nil {
		return errorResponse(http.StatusBadRequest, "the request could not be read"), err
	}
	if req.Method == http.MethodGet || req.Method == http.MethodHead {
		v, err := p.Exec(ctx, epform.SpecName, starlark.Tuple{reqValue})
		if err != nil {
			return errorResponse(http.StatusInternalServerError, "form_spec failed: "+err.Error()), err
		}
		return r.specResponse(t)(v)
	}
	ok, refusal := r.checkSubmission(t, req)
	if refusal != nil {
		return *refusal, nil
	}
	describeUnsaved(ok.values, ok.uploads)
	sv, err := starlarkconv.ToStarlark(ok.values)
	if err != nil {
		return errorResponse(http.StatusBadRequest, "the submission could not be read"), err
	}
	sensitive := sensitiveValues(ok.spec.Sensitive(), ok.values)
	v, err := p.Exec(ctx, epform.SubmitName, starlark.Tuple{reqValue, sv})
	if err != nil {
		err = redactFailure(sensitive, err)
		return errorResponse(http.StatusInternalServerError, "form_submit failed: "+err.Error()), err
	}
	resp, err := formAnswer(ok.spec, v)
	return resp, redactFailure(sensitive, err)
}

// describeUnsaved gives each file a preview's submission carries the address
// key a saved file has, empty: a preview saves no file (#2494), and a
// form_submit reading file["uri"] reads "" rather than failing on a key a
// published form always has.
func describeUnsaved(values map[string]any, uploads []epform.Upload) {
	for _, u := range uploads {
		list, _ := values[u.Field].([]any)
		for _, item := range list {
			if m, ok := item.(map[string]any); ok {
				if _, has := m[epform.FileKeyURI]; !has {
					m[epform.FileKeyURI] = ""
				}
			}
		}
	}
}
