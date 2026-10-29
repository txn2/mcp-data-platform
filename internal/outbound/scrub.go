package outbound

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"

	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/txn2/mcp-data-platform/internal/secretref"
)

// redactedQueryValue stands for every query value in a URL the chain reports.
const redactedQueryValue = "REDACTED"

// ScrubURL is u as the chain reports it in an error or a span (#2358): no
// userinfo, every query value replaced (a query can carry an api_key or a
// filled {{secret:}}), and every stored secret ctx's call filled into the
// path replaced by its name. The query's keys are kept, so which parameters
// were sent stays visible.
func ScrubURL(ctx context.Context, u *url.URL) string {
	if u == nil {
		return ""
	}
	c := *u
	c.User = nil
	if c.RawQuery != "" {
		q := c.Query()
		keys := make([]string, 0, len(q))
		for k := range q {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, url.QueryEscape(k)+"="+redactedQueryValue)
		}
		c.RawQuery = strings.Join(parts, "&")
	}
	return secretref.FromContext(ctx).String(c.String())
}

// ScrubError rewrites the URL the *url.Error in err's chain names with
// ScrubURL, in place, so errors.As on it keeps working, and returns an error
// whose message carries no credential. http.Client.Do wraps a transport
// failure in a *url.Error of its own naming the request's whole URL, so a
// caller that shows an error from Do passes it through here first (#2358).
// A message an outer wrap already rendered is rewritten too.
func ScrubError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	ue, ok := errors.AsType[*url.Error](err)
	if !ok {
		return err
	}
	msg, orig := err.Error(), ue.URL
	// An error whose message is ue's own renders it afresh after the rewrite;
	// a wrap that rendered its message earlier is given the rewritten text.
	rendersAfresh := msg == ue.Error()
	if parsed, perr := url.Parse(orig); perr != nil {
		ue.URL = "[unparsed URL]"
	} else {
		ue.URL = ScrubURL(ctx, parsed)
	}
	if rendersAfresh {
		return err
	}
	return scrubbedError{msg: strings.ReplaceAll(msg, orig, ue.URL), err: err}
}

// scrubbedError is an error whose message ScrubError rewrote; Unwrap keeps
// the chain.
type scrubbedError struct {
	msg string
	err error
}

func (e scrubbedError) Error() string { return e.msg }
func (e scrubbedError) Unwrap() error { return e.err }

// spanURL sits under otelhttp and replaces the url.full it recorded from the
// request with the scrubbed URL (#2358): otelhttp removes userinfo and keeps
// the query.
type spanURL struct{ base http.RoundTripper }

// RoundTrip records the scrubbed URL on the client span and sends req.
func (s spanURL) RoundTrip(req *http.Request) (*http.Response, error) {
	if span := trace.SpanFromContext(req.Context()); span.IsRecording() {
		span.SetAttributes(semconv.URLFull(ScrubURL(req.Context(), req.URL)))
	}
	return s.base.RoundTrip(req) //nolint:wrapcheck // the base transport's error, scrubbed by the chain's outer layer
}
