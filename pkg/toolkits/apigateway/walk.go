package apigateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/pagewalk"
)

// pageRequester is the gateway's side of a page walk (issue #1535): it
// builds each page's request through buildUpstreamRequest, so every
// page gets the SSRF guards, reserved-header checks, and credential
// injection the first page got, and reads each page under the caps a
// single call has.
type pageRequester struct {
	inv  invocation
	base InvokeInput
	// last is the input the most recent page was requested with; the
	// budget refusal names its path.
	last InvokeInput
}

func withTarget(in InvokeInput, t pagewalk.Target) InvokeInput {
	in.Path, in.Query, in.Body = t.Path, t.Query, t.Body
	return in
}

// Do implements pagewalk.Requester.
func (r *pageRequester) Do(ctx context.Context, t pagewalk.Target) (*http.Response, error) {
	r.last = withTarget(r.base, t)
	req, err := buildUpstreamRequest(ctx, r.inv.cfg, r.inv.auth, catalogView{specs: r.inv.specs, webdavRoutes: r.inv.webdavRoutes}, r.last)
	if err != nil {
		return nil, err
	}
	// #nosec G107 G704 -- req.URL is built by buildUpstreamRequest from the
	// connection's base_url and a path the address derived under it, with the
	// same host pinning and validatePath checks as a single call.
	resp, err := r.inv.client.Do(req)
	if err != nil {
		return nil, errors.New(scrubTransportError(err))
	}
	return resp, nil
}

// ReadPage buffers one page under the connection's max_response_bytes,
// reserved against the shared in-flight budget like an inline call. A
// page is what a single api_invoke_endpoint call would return, so it is
// held to the same cap; a page past it is a page the caller must ask
// the upstream to make smaller, not one the walk can truncate.
func (r *pageRequester) ReadPage(resp *http.Response) (pagewalk.Page, error) {
	defer resp.Body.Close() //nolint:errcheck // best-effort cleanup
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return pagewalk.Page{}, fmt.Errorf("upstream returned %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	readCap := r.inv.cfg.MaxResponseBytes
	if readCap <= 0 {
		readCap = DefaultMaxResponseBytes
	}
	reserved, ok := reserveBodyBudget(r.inv.budget, resp.ContentLength, readCap)
	if !ok {
		return pagewalk.Page{}, &budgetError{
			limit: r.inv.budget.Max(), requested: reserved, inUse: r.inv.budget.InUse(),
			connection: r.inv.cfg.ConnectionName, path: r.last.Path,
		}
	}
	defer r.inv.budget.Release(reserved)
	body, truncated, err := readBody(resp.Body, readCap)
	if err != nil {
		return pagewalk.Page{}, err
	}
	if truncated {
		return pagewalk.Page{}, fmt.Errorf("page exceeds the connection's max_response_bytes (%d); request a smaller page from the upstream", readCap)
	}
	return pagewalk.Page{
		Status: resp.StatusCode, Header: resp.Header,
		ContentType: resp.Header.Get(headerContentType), Body: body,
	}, nil
}

// newPageWalk binds a walk to a connection: the request template, the
// address kind (the util connection's fetch_url walks the url in its
// body), the route policy check for every page, and the sink.
func newPageWalk(inv invocation, in InvokeInput, authorize func(InvokeInput) error, sink pagewalk.Sink) (*pagewalk.Walk, error) {
	if in.Paginate == nil {
		return nil, errors.New("apigateway: paginate block is required for a page walk")
	}
	var authorizeTarget func(pagewalk.Target) error
	if authorize != nil {
		authorizeTarget = func(t pagewalk.Target) error { return authorize(withTarget(in, t)) }
	}
	return pagewalk.New(pagewalk.Options{ //nolint:wrapcheck // the block's own validation text is the message
		Paginate: *in.Paginate,
		Address: pagewalk.AddressSpec{
			BaseURL: inv.cfg.BaseURL, Path: in.Path, Query: in.Query, Body: in.Body,
			WalkBodyURL: inv.cfg.Handler == HandlerInternal, ValidatePath: validatePath,
		},
		Requester: &pageRequester{inv: inv, base: in},
		Authorize: authorizeTarget,
		Sink:      sink,
	})
}
