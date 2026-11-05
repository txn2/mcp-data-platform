package apichange

import (
	"context"
	"errors"
	"fmt"
	neturl "net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	apicatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

// ErrNotRefreshable is a check of a spec with nowhere to read it from: not
// URL-sourced, and no upstream_url.
var ErrNotRefreshable = errors.New("apichange: the spec has no source URL and no upstream URL to check")

// ErrNotOpenAPI is an upstream_url on a spec whose operations are not read
// from an OpenAPI document (a GraphQL schema), which a report cannot compare.
var ErrNotOpenAPI = errors.New("apichange: only an OpenAPI or WSDL spec can be compared with an upstream")

// Catalogs reads specs and the connections that reference a catalog
// (apicatalog.Store).
type Catalogs interface {
	GetSpec(ctx context.Context, catalogID, specName string) (*apicatalog.SpecEntry, error)
	ReferencingConnections(ctx context.Context, catalogID string) ([]apicatalog.ConnectionRef, error)
}

// Writer saves a spec as the copy in use: stores it, re-embeds its
// operations and reloads the connections that read it, on every replica.
// catalogapi.Config.WriteSpec is the one implementation, shared with the
// admin API's own spec writes.
type Writer func(ctx context.Context, catalogID string, entry apicatalog.SpecEntry) error

// Preparer validates a spec and renders the OpenAPI document it serves
// (catalogapi.PrepareSpec).
type Preparer func(entry *apicatalog.SpecEntry) error

// Notifier sends a change report to the channels an administrator chose.
type Notifier interface {
	Changed(ctx context.Context, c Change)
}

// ChangeStore keeps watches and change reports (*Store).
type ChangeStore interface {
	GetWatch(ctx context.Context, k SpecKey) (Watch, error)
	RecordCheck(ctx context.Context, k SpecKey, rec CheckRecord) error
	Due(ctx context.Context, limit int) ([]SpecKey, error)
	AddChange(ctx context.Context, c Change) error
	GetChange(ctx context.Context, k SpecKey, id string) (Change, error)
	PendingFor(ctx context.Context, catalogID string) (map[string]string, error)
	Decide(ctx context.Context, k SpecKey, id, status, by string) error
}

// Refresher checks a spec's upstream and acts on what changed. The manual
// refresh route and the schedule both go through Check.
type Refresher struct {
	Catalogs Catalogs
	Write    Writer
	Prepare  Preparer
	// Store keeps watches and reports; nil (no database) checks and applies
	// with no report kept and no hold.
	Store ChangeStore
	// Usage fills each reported operation's callers; nil leaves them out.
	Usage *Usage
	// Notify sends each stored report; nil sends nothing.
	Notify Notifier
	// Fetch is the base fetch options: the operator's private prefixes.
	Fetch apicatalog.FetchOptions
	// fetcher replaces apicatalog.FetchFromURL in tests.
	fetcher func(ctx context.Context, url string, opts apicatalog.FetchOptions) (*apicatalog.FetchResult, error)
}

// Result is what one check did. Spec is the copy in use afterwards; Change is
// the report recorded, or the held one an unchanged check found waiting.
type Result struct {
	Spec        *apicatalog.SpecEntry
	Change      *Change
	NotModified bool
}

// Check reads the spec's upstream and acts on it: a URL-sourced spec that
// changed is applied, or held when the spec holds changes; an uploaded or
// pasted spec with an upstream_url is compared with it and the difference
// reported, never applied. Every check is stamped on the spec's watch,
// failure included.
func (r *Refresher) Check(ctx context.Context, k SpecKey, trigger, _ string) (Result, error) {
	spec, err := r.Catalogs.GetSpec(ctx, k.CatalogID, k.SpecName)
	if err != nil {
		return Result{}, err //nolint:wrapcheck // the store's not-found is the caller's 404
	}
	watch, err := r.watch(ctx, k)
	if err != nil {
		return Result{}, err
	}
	switch {
	case spec.SourceKind == apicatalog.SourceURL:
		res, err := r.checkSource(ctx, k, spec, watch, trigger)
		return res, r.stamp(ctx, k, err, CheckRecord{})
	case watch.UpstreamURL != "":
		return r.checkUpstream(ctx, k, spec, watch, trigger)
	default:
		// Stamped like any failure, so a schedule left on a spec that lost
		// its source moves on to its next interval rather than every pass.
		return Result{Spec: spec}, r.stamp(ctx, k, ErrNotRefreshable, CheckRecord{})
	}
}

func (r *Refresher) watch(ctx context.Context, k SpecKey) (Watch, error) {
	if r.Store == nil {
		return Watch{SpecKey: k}, nil
	}
	return r.Store.GetWatch(ctx, k) //nolint:wrapcheck // the store names the spec
}

// stamp records a check on the watch and returns the check's own error. A
// failure to stamp is returned only when the check itself succeeded.
func (r *Refresher) stamp(ctx context.Context, k SpecKey, checkErr error, rec CheckRecord) error {
	if r.Store == nil {
		return checkErr
	}
	if checkErr != nil {
		rec.Err = checkErr.Error()
	}
	if err := r.Store.RecordCheck(ctx, k, rec); err != nil && checkErr == nil {
		return err //nolint:wrapcheck // the store names the spec
	}
	return checkErr
}

func (r *Refresher) get(ctx context.Context, k SpecKey, url, etag string) (*apicatalog.FetchResult, error) {
	opts := r.Fetch
	opts.IfNoneMatch = etag
	fetch := r.fetcher
	if fetch == nil {
		fetch = apicatalog.FetchFromURL
	}
	res, err := fetch(ctx, url, opts)
	if err != nil {
		// The URL is left out: a vendor key in its query would otherwise
		// reach the watch's last_error and the response that repeats it.
		return nil, &fetchError{spec: k, err: err, text: withoutURL(err.Error(), url)}
	}
	return res, nil
}

// fetchError is a failed fetch named by its spec, with the URL removed from
// the text and the cause kept for errors.Is.
type fetchError struct {
	spec SpecKey
	err  error
	text string
}

func (e *fetchError) Error() string {
	return "fetching the upstream of " + e.spec.CatalogID + "/" + e.spec.SpecName + ": " + e.text
}

func (e *fetchError) Unwrap() error { return e.err }

// FetchFailed reports whether err is a check that could not read the
// upstream; its text names the spec and never the URL.
func FetchFailed(err error) bool {
	var fe *fetchError
	return errors.As(err, &fe)
}

// withoutURL removes every spelling of raw from text: as written, and as Go
// prints it back after parsing.
func withoutURL(text, raw string) string {
	if raw == "" {
		return text
	}
	text = strings.ReplaceAll(text, raw, "<url>")
	if u, err := neturl.Parse(raw); err == nil {
		text = strings.ReplaceAll(text, u.String(), "<url>")
		text = strings.ReplaceAll(text, u.Redacted(), "<url>")
		if u.RawQuery != "" {
			text = strings.ReplaceAll(text, u.RawQuery, "<query>")
		}
	}
	return text
}

// checkSource is a URL-sourced spec's check.
func (r *Refresher) checkSource(ctx context.Context, k SpecKey, spec *apicatalog.SpecEntry, watch Watch, trigger string) (Result, error) {
	held, err := r.held(ctx, k)
	if err != nil {
		return Result{}, err
	}
	next, unchanged, err := r.fetchSource(ctx, k, spec)
	if err != nil {
		return Result{Spec: spec}, err
	}
	switch {
	case unchanged:
		return Result{Spec: spec, Change: held, NotModified: true}, nil
	case held != nil && held.PendingContent == next.Content:
		return Result{Spec: spec, Change: held}, nil
	case !next.ServesOpenAPI() || next.Effective() == spec.Effective():
		// A GraphQL schema has no operations a report reads; an unchanged
		// document still refreshes its ETag and fetch time.
		return r.applyEntry(ctx, k, next)
	}
	report, err := Compare(spec.Effective(), next.Effective())
	if err != nil {
		return Result{Spec: spec}, err
	}
	if report.Empty() {
		return r.applyEntry(ctx, k, next)
	}
	r.attachCallers(ctx, k, spec, next, &report)
	return r.settle(ctx, spec, next, watch.HoldChanges, newChange(k, report, trigger))
}

// fetchSource reads a URL-sourced spec's upstream, conditionally on its ETag,
// and prepares what it fetched. unchanged is a 304.
func (r *Refresher) fetchSource(ctx context.Context, k SpecKey, spec *apicatalog.SpecEntry) (next apicatalog.SpecEntry, unchanged bool, err error) {
	res, err := r.get(ctx, k, spec.SourceURL, spec.ETag)
	if err != nil {
		return next, false, err
	}
	if res.NotModified {
		return next, true, nil
	}
	next = *spec
	next.Content, next.OpenAPIContent, next.ETag, next.LastFetchedAt = res.Content, "", res.ETag, res.FetchedAt
	if err := r.Prepare(&next); err != nil {
		return next, false, fmt.Errorf("the fetched spec is not valid: %w", err)
	}
	return next, false, nil
}

// newChange is a change of the spec reported as report.
func newChange(k SpecKey, report Report, trigger string) Change {
	return Change{
		ID: uuid.NewString(), CatalogID: k.CatalogID, SpecName: k.SpecName,
		Trigger: trigger, Report: report, Summary: report.Counts(), CreatedAt: time.Now().UTC(),
	}
}

// settle holds the changed spec for review when the spec holds changes, and
// applies it otherwise, recording the change either way.
func (r *Refresher) settle(ctx context.Context, spec *apicatalog.SpecEntry, next apicatalog.SpecEntry, hold bool, change Change) (Result, error) {
	k := SpecKey{CatalogID: change.CatalogID, SpecName: change.SpecName}
	if hold && r.Store != nil {
		change.Status, change.PendingContent, change.PendingETag = StatusPending, next.Content, next.ETag
		return r.record(ctx, Result{Spec: spec}, change)
	}
	applied, err := r.applyEntry(ctx, k, next)
	if err != nil {
		return applied, err
	}
	change.Status = StatusApplied
	return r.record(ctx, applied, change)
}

// held is the change a spec holds for review, nil when none waits.
func (r *Refresher) held(ctx context.Context, k SpecKey) (*Change, error) {
	if r.Store == nil {
		return nil, nil //nolint:nilnil // no store holds no change, and that is no failure
	}
	pending, err := r.Store.PendingFor(ctx, k.CatalogID)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the catalog
	}
	id, ok := pending[k.SpecName]
	if !ok {
		return nil, nil //nolint:nilnil // nothing waits, which is no failure
	}
	c, err := r.Store.GetChange(ctx, k, id)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the change
	}
	return &c, nil
}

// applyEntry saves next as the copy in use.
func (r *Refresher) applyEntry(ctx context.Context, k SpecKey, next apicatalog.SpecEntry) (Result, error) {
	if err := r.Write(ctx, k.CatalogID, next); err != nil {
		return Result{}, fmt.Errorf("saving the refreshed spec: %w", err)
	}
	return Result{Spec: &next}, nil
}

// record stores a change and sends it; with no store the report is only
// returned.
func (r *Refresher) record(ctx context.Context, res Result, c Change) (Result, error) {
	if r.Store != nil {
		if err := r.Store.AddChange(ctx, c); err != nil {
			return res, err //nolint:wrapcheck // the store names the spec
		}
		if r.Notify != nil {
			r.Notify.Changed(ctx, c)
		}
	}
	res.Change = &c
	return res, nil
}

// checkUpstream compares an uploaded or pasted spec's upstream with the
// last copy read from it, or with the spec itself the first time.
func (r *Refresher) checkUpstream(ctx context.Context, k SpecKey, spec *apicatalog.SpecEntry, watch Watch, trigger string) (Result, error) {
	if !spec.ServesOpenAPI() {
		return Result{Spec: spec}, r.stamp(ctx, k, ErrNotOpenAPI, CheckRecord{})
	}
	res, err := r.get(ctx, k, watch.UpstreamURL, watch.UpstreamETag)
	if err != nil {
		return Result{Spec: spec}, r.stamp(ctx, k, err, CheckRecord{})
	}
	if res.NotModified {
		return Result{Spec: spec, NotModified: true}, r.stamp(ctx, k, nil, CheckRecord{})
	}
	upstream := apicatalog.SpecEntry{SpecName: spec.SpecName, Content: res.Content, SpecFormat: spec.SpecFormat}
	if err := r.Prepare(&upstream); err != nil {
		return Result{Spec: spec}, r.stamp(ctx, k, fmt.Errorf("the upstream spec is not valid: %w", err), CheckRecord{})
	}
	base := watch.UpstreamContent
	if base == "" {
		base = spec.Effective()
	}
	keep := CheckRecord{Upstream: true, UpstreamETag: res.ETag, UpstreamBody: upstream.Effective()}
	report, err := Compare(base, upstream.Effective())
	if err != nil {
		return Result{Spec: spec}, r.stamp(ctx, k, err, CheckRecord{})
	}
	if err := r.stamp(ctx, k, nil, keep); err != nil {
		return Result{Spec: spec}, err
	}
	if report.Empty() {
		return Result{Spec: spec}, nil
	}
	r.attachCallers(ctx, k, spec, *spec, &report)
	return r.record(ctx, Result{Spec: spec}, Change{
		ID: uuid.NewString(), CatalogID: k.CatalogID, SpecName: k.SpecName, Status: StatusReported,
		Trigger: trigger, Report: report, Summary: report.Counts(), CreatedAt: time.Now().UTC(),
	})
}

// Apply settles a held change by putting its document in use.
func (r *Refresher) Apply(ctx context.Context, k SpecKey, id, by string) (Result, error) {
	if r.Store == nil {
		return Result{}, ErrNoChange
	}
	c, err := r.Store.GetChange(ctx, k, id)
	if err != nil {
		return Result{}, err //nolint:wrapcheck // the store's ErrNoChange is the caller's 404
	}
	if c.Status != StatusPending {
		return Result{}, ErrNotPending
	}
	spec, err := r.Catalogs.GetSpec(ctx, k.CatalogID, k.SpecName)
	if err != nil {
		return Result{}, err //nolint:wrapcheck // the store's not-found is the caller's 404
	}
	next := *spec
	next.Content, next.OpenAPIContent, next.ETag, next.LastFetchedAt = c.PendingContent, "", c.PendingETag, time.Now().UTC()
	if err := r.Prepare(&next); err != nil {
		return Result{}, fmt.Errorf("the held spec is not valid: %w", err)
	}
	res, err := r.applyEntry(ctx, k, next)
	if err != nil {
		return res, err
	}
	if err := r.Store.Decide(ctx, k, id, StatusApplied, by); err != nil {
		return res, err //nolint:wrapcheck // the store's ErrNotPending is the caller's 409
	}
	c.Status, c.DecidedBy = StatusApplied, by
	res.Change = &c
	return res, nil
}

// Discard settles a held change by dropping its document.
func (r *Refresher) Discard(ctx context.Context, k SpecKey, id, by string) error {
	if r.Store == nil {
		return ErrNoChange
	}
	if _, err := r.Store.GetChange(ctx, k, id); err != nil {
		return err //nolint:wrapcheck // the store's ErrNoChange is the caller's 404
	}
	return r.Store.Decide(ctx, k, id, StatusDiscarded, by) //nolint:wrapcheck // the store's ErrNotPending is the caller's 409
}

// attachCallers fills each reported operation's callers, read through the
// connections that reference the catalog. A failure leaves the report
// without them rather than failing the check.
func (r *Refresher) attachCallers(ctx context.Context, k SpecKey, before *apicatalog.SpecEntry, after apicatalog.SpecEntry, report *Report) {
	if r.Usage == nil {
		return
	}
	refs, err := r.Catalogs.ReferencingConnections(ctx, k.CatalogID)
	if err != nil || len(refs) == 0 {
		return
	}
	conns := make([]string, 0, len(refs))
	for _, ref := range refs {
		conns = append(conns, ref.Name)
	}
	callers, err := r.Usage.Callers(ctx, conns, NewOperationIndex(before.Effective(), after.Effective()))
	if err != nil {
		return
	}
	fill := func(ops []Op) {
		for i := range ops {
			ops[i].UsedBy = callers[ops[i].ID]
		}
	}
	fill(report.Removed)
	fill(report.Deprecated)
	fill(report.Added)
	for i := range report.Changed {
		report.Changed[i].UsedBy = callers[report.Changed[i].ID]
	}
}
