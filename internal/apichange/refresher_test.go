package apichange

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/admin/catalogapi"
	apicatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

// fakeCatalogs holds specs by name.
type fakeCatalogs struct {
	specs map[string]*apicatalog.SpecEntry
	refs  []apicatalog.ConnectionRef
}

func (f *fakeCatalogs) GetSpec(_ context.Context, _, name string) (*apicatalog.SpecEntry, error) {
	s, ok := f.specs[name]
	if !ok {
		return nil, apicatalog.ErrNotFound
	}
	cp := *s
	return &cp, nil
}

func (f *fakeCatalogs) ReferencingConnections(context.Context, string) ([]apicatalog.ConnectionRef, error) {
	return f.refs, nil
}

// fakeChanges is an in-memory ChangeStore.
type fakeChanges struct {
	watches map[SpecKey]Watch
	checks  []CheckRecord
	changes []Change
	addErr  error
}

func newFakeChanges() *fakeChanges { return &fakeChanges{watches: map[SpecKey]Watch{}} }

func (f *fakeChanges) GetWatch(_ context.Context, k SpecKey) (Watch, error) {
	w, ok := f.watches[k]
	if !ok {
		return Watch{SpecKey: k}, nil
	}
	return w, nil
}

func (f *fakeChanges) RecordCheck(_ context.Context, k SpecKey, rec CheckRecord) error {
	f.checks = append(f.checks, rec)
	if rec.Upstream {
		w := f.watches[k]
		w.UpstreamETag, w.UpstreamContent = rec.UpstreamETag, rec.UpstreamBody
		f.watches[k] = w
	}
	return nil
}

func (*fakeChanges) Due(context.Context, int) ([]SpecKey, error) { return nil, nil }

func (f *fakeChanges) AddChange(_ context.Context, c Change) error {
	if f.addErr != nil {
		return f.addErr
	}
	if c.Status == StatusPending {
		for i := range f.changes {
			if f.changes[i].Status == StatusPending {
				f.changes[i].Status = StatusSuperseded
			}
		}
	}
	f.changes = append(f.changes, c)
	return nil
}

func (f *fakeChanges) GetChange(_ context.Context, _ SpecKey, id string) (Change, error) {
	for _, c := range f.changes {
		if c.ID == id {
			return c, nil
		}
	}
	return Change{}, ErrNoChange
}

func (f *fakeChanges) PendingFor(context.Context, string) (map[string]string, error) {
	out := map[string]string{}
	for _, c := range f.changes {
		if c.Status == StatusPending {
			out[c.SpecName] = c.ID
		}
	}
	return out, nil
}

func (f *fakeChanges) Decide(_ context.Context, _ SpecKey, id, status, by string) error {
	for i := range f.changes {
		if f.changes[i].ID == id && f.changes[i].Status == StatusPending {
			f.changes[i].Status, f.changes[i].DecidedBy = status, by
			return nil
		}
	}
	return ErrNotPending
}

// upstream serves one document with an ETag and answers If-None-Match.
type upstream struct {
	body, etag string
	err        error
	calls      int
	lastURL    string
}

func (u *upstream) fetch(_ context.Context, url string, opts apicatalog.FetchOptions) (*apicatalog.FetchResult, error) {
	u.calls++
	u.lastURL = url
	if u.err != nil {
		return nil, u.err
	}
	if opts.IfNoneMatch != "" && opts.IfNoneMatch == u.etag {
		return &apicatalog.FetchResult{ETag: u.etag, NotModified: true}, nil
	}
	return &apicatalog.FetchResult{Content: u.body, ETag: u.etag, FetchedAt: time.Now()}, nil
}

type sent struct{ changes []Change }

func (s *sent) Changed(_ context.Context, c Change) { s.changes = append(s.changes, c) }

// rig is a refresher over fakes with one URL-sourced spec "pets" at v1.
type rig struct {
	r       *Refresher
	cats    *fakeCatalogs
	store   *fakeChanges
	up      *upstream
	written []apicatalog.SpecEntry
	sent    *sent
}

var key = SpecKey{CatalogID: "acme", SpecName: "pets"}

func newRig(t *testing.T) *rig {
	t.Helper()
	g := &rig{
		cats: &fakeCatalogs{specs: map[string]*apicatalog.SpecEntry{
			"pets": {
				SpecName: "pets", Content: specV1, SourceKind: apicatalog.SourceURL,
				SourceURL: "https://vendor.example/openapi.yaml?key=sekret", ETag: `"v1"`,
			},
		}},
		store: newFakeChanges(),
		up:    &upstream{body: specV2, etag: `"v2"`},
		sent:  &sent{},
	}
	g.r = &Refresher{
		Catalogs: g.cats, Prepare: catalogapi.PrepareSpec, Store: g.store, Notify: g.sent, fetcher: g.up.fetch,
		Write: func(_ context.Context, _ string, e apicatalog.SpecEntry) error {
			g.written = append(g.written, e)
			cp := e
			g.cats.specs[e.SpecName] = &cp
			return nil
		},
	}
	return g
}

// #2483: a changed upstream is applied, its report recorded and sent.
func TestCheck_AChangedSpecIsAppliedAndReported(t *testing.T) {
	g := newRig(t)
	res, err := g.r.Check(t.Context(), key, TriggerSchedule, "")
	require.NoError(t, err)
	require.NotNil(t, res.Change)
	assert.Equal(t, StatusApplied, res.Change.Status)
	assert.Equal(t, TriggerSchedule, res.Change.Trigger)
	assert.Equal(t, "1 added, 1 removed, 2 changed, 1 deprecated; version 1.0 to 2.0", res.Change.Summary)
	require.Len(t, g.written, 1)
	assert.Equal(t, specV2, g.written[0].Content)
	assert.Equal(t, `"v2"`, g.written[0].ETag)
	assert.Equal(t, 4, g.written[0].OperationCount, "prepared before it was written")
	require.Len(t, g.store.changes, 1)
	require.Len(t, g.sent.changes, 1)
	require.Len(t, g.store.checks, 1)
	assert.Empty(t, g.store.checks[0].Err)

	// Unchanged since: the conditional fetch answers 304 and nothing more
	// is written or reported.
	res, err = g.r.Check(t.Context(), key, TriggerSchedule, "")
	require.NoError(t, err)
	assert.True(t, res.NotModified)
	assert.Len(t, g.written, 1)
	assert.Len(t, g.store.changes, 1)
}

// #2483: with hold_changes, the spec in use is untouched until applied.
func TestCheck_AHeldChangeWaitsForReview(t *testing.T) {
	g := newRig(t)
	g.store.watches[key] = Watch{SpecKey: key, HoldChanges: true}
	res, err := g.r.Check(t.Context(), key, TriggerManual, "admin@example.com")
	require.NoError(t, err)
	require.NotNil(t, res.Change)
	assert.Equal(t, StatusPending, res.Change.Status)
	assert.Empty(t, g.written, "a held change does not replace the spec in use")
	assert.Equal(t, specV1, g.cats.specs["pets"].Content)

	// The same upstream again: the waiting change is returned, not a second.
	g.up.etag = `"v2b"`
	res, err = g.r.Check(t.Context(), key, TriggerSchedule, "")
	require.NoError(t, err)
	assert.Len(t, g.store.changes, 1)
	assert.Equal(t, g.store.changes[0].ID, res.Change.ID)

	// Applied: the held document is put in use.
	applied, err := g.r.Apply(t.Context(), key, res.Change.ID, "admin@example.com")
	require.NoError(t, err)
	assert.Equal(t, StatusApplied, applied.Change.Status)
	require.Len(t, g.written, 1)
	assert.Equal(t, specV2, g.written[0].Content)
	_, err = g.r.Apply(t.Context(), key, res.Change.ID, "admin@example.com")
	require.ErrorIs(t, err, ErrNotPending, "a change is settled once")
}

func TestCheck_ANewerHeldChangeSupersedesTheWaitingOne(t *testing.T) {
	g := newRig(t)
	g.store.watches[key] = Watch{SpecKey: key, HoldChanges: true}
	first, err := g.r.Check(t.Context(), key, TriggerSchedule, "")
	require.NoError(t, err)
	g.up.body, g.up.etag = strings.Replace(specV2, `version: "2.0"`, `version: "2.1"`, 1), `"v3"`
	second, err := g.r.Check(t.Context(), key, TriggerSchedule, "")
	require.NoError(t, err)
	assert.NotEqual(t, first.Change.ID, second.Change.ID)
	assert.Equal(t, StatusSuperseded, g.store.changes[0].Status)
	assert.Equal(t, StatusPending, g.store.changes[1].Status)
	require.NoError(t, g.r.Discard(t.Context(), key, second.Change.ID, "admin@example.com"))
	assert.Equal(t, StatusDiscarded, g.store.changes[1].Status)
	assert.Empty(t, g.written, "a discarded change leaves the spec in use as it was")
	require.ErrorIs(t, g.r.Discard(t.Context(), key, "nope", "a"), ErrNoChange)
}

func TestCheck_AFormattingChangeIsAppliedWithoutAReport(t *testing.T) {
	g := newRig(t)
	g.up.body = specV1 + "\n# a comment\n"
	res, err := g.r.Check(t.Context(), key, TriggerSchedule, "")
	require.NoError(t, err)
	assert.Nil(t, res.Change)
	assert.Len(t, g.written, 1, "the ETag and fetch time are refreshed")
	assert.Empty(t, g.store.changes)
	assert.Empty(t, g.sent.changes)
}

// The fetch's URL, which can carry a vendor key, is in no error text and so
// in no stored last_error.
func TestCheck_AFailedFetchIsStampedWithoutTheURL(t *testing.T) {
	g := newRig(t)
	g.up.err = fmt.Errorf("fetch: Get %q: dial tcp: lookup vendor.example: no such host: %w",
		"https://vendor.example/openapi.yaml?key=sekret", apicatalog.ErrUpstream)
	_, err := g.r.Check(t.Context(), key, TriggerSchedule, "")
	require.Error(t, err)
	assert.True(t, FetchFailed(err))
	require.ErrorIs(t, err, apicatalog.ErrUpstream, "the cause is kept for the status mapping")
	assert.NotContains(t, err.Error(), "sekret")
	assert.NotContains(t, err.Error(), "vendor.example/openapi")
	assert.Contains(t, err.Error(), "acme/pets")
	require.Len(t, g.store.checks, 1)
	assert.NotContains(t, g.store.checks[0].Err, "sekret")
	assert.NotEmpty(t, g.store.checks[0].Err)
}

func TestCheck_AnInvalidFetchedSpecIsRefused(t *testing.T) {
	g := newRig(t)
	g.up.body = "openapi: 3.0.3\ninfo: {title: x}\npaths: 7\n"
	_, err := g.r.Check(t.Context(), key, TriggerSchedule, "")
	require.ErrorContains(t, err, "is not valid")
	assert.Empty(t, g.written)
}

func TestCheck_NoStoreAppliesAndReturnsTheReport(t *testing.T) {
	g := newRig(t)
	g.r.Store, g.r.Notify = nil, nil
	res, err := g.r.Check(t.Context(), key, TriggerManual, "")
	require.NoError(t, err)
	require.NotNil(t, res.Change)
	assert.Equal(t, StatusApplied, res.Change.Status)
	assert.Len(t, g.written, 1)
	_, err = g.r.Apply(t.Context(), key, "x", "a")
	require.ErrorIs(t, err, ErrNoChange)
	require.ErrorIs(t, g.r.Discard(t.Context(), key, "x", "a"), ErrNoChange)
}

func TestCheck_ASpecWithNothingToCheck(t *testing.T) {
	g := newRig(t)
	g.cats.specs["pets"].SourceKind = apicatalog.SourceUpload
	_, err := g.r.Check(t.Context(), key, TriggerManual, "")
	require.ErrorIs(t, err, ErrNotRefreshable)
	require.Len(t, g.store.checks, 1, "stamped, so a schedule moves on")
	_, err = g.r.Check(t.Context(), SpecKey{CatalogID: "acme", SpecName: "ghost"}, TriggerManual, "")
	require.ErrorIs(t, err, apicatalog.ErrNotFound)
}

// #2484: an uploaded spec is compared with its upstream and never replaced.
func TestCheck_AnUploadedSpecIsComparedWithItsUpstream(t *testing.T) {
	g := newRig(t)
	g.cats.specs["pets"].SourceKind = apicatalog.SourceUpload
	g.cats.specs["pets"].SourceURL = ""
	g.store.watches[key] = Watch{SpecKey: key, UpstreamURL: "https://vendor.example/openapi.yaml"}

	res, err := g.r.Check(t.Context(), key, TriggerManual, "")
	require.NoError(t, err)
	require.NotNil(t, res.Change)
	assert.Equal(t, StatusReported, res.Change.Status)
	assert.Equal(t, "https://vendor.example/openapi.yaml", g.up.lastURL)
	assert.Empty(t, g.written, "a reported change never replaces the stored copy")
	assert.Equal(t, specV1, g.cats.specs["pets"].Content)
	assert.Equal(t, `"v2"`, g.store.watches[key].UpstreamETag)

	// The upstream unchanged since: 304, nothing reported.
	res, err = g.r.Check(t.Context(), key, TriggerSchedule, "")
	require.NoError(t, err)
	assert.True(t, res.NotModified)
	assert.Len(t, g.store.changes, 1)

	// The upstream moves again: compared with the last copy read, not the
	// stored spec, so only the new difference is reported.
	g.up.body, g.up.etag = strings.Replace(specV2, `version: "2.0"`, `version: "2.1"`, 1), `"v3"`
	res, err = g.r.Check(t.Context(), key, TriggerSchedule, "")
	require.NoError(t, err)
	require.NotNil(t, res.Change)
	assert.Equal(t, "0 added, 0 removed, 0 changed, 0 deprecated; version 2.0 to 2.1", res.Change.Summary)
}

func TestCheck_AnUpstreamOnAGraphQLSpecIsRefused(t *testing.T) {
	g := newRig(t)
	g.cats.specs["pets"].SourceKind = apicatalog.SourceInline
	g.cats.specs["pets"].SpecFormat = apicatalog.FormatGraphQL
	g.store.watches[key] = Watch{SpecKey: key, UpstreamURL: "https://vendor.example/schema.graphql"}
	_, err := g.r.Check(t.Context(), key, TriggerManual, "")
	require.ErrorIs(t, err, ErrNotOpenAPI)
}

func TestCheck_AReportNamesWhatUsesEachOperation(t *testing.T) {
	g := newRig(t)
	g.cats.refs = []apicatalog.ConnectionRef{{Kind: "api", Name: "petstore"}}
	g.r.Usage = &Usage{} // no database: nothing is found to use it
	res, err := g.r.Check(t.Context(), key, TriggerSchedule, "")
	require.NoError(t, err)
	assert.Empty(t, res.Change.Report.Removed[0].UsedBy)
}

func TestCheck_AStoreFailureIsReturned(t *testing.T) {
	g := newRig(t)
	g.store.addErr = errors.New("db down")
	_, err := g.r.Check(t.Context(), key, TriggerSchedule, "")
	require.ErrorContains(t, err, "db down")
}

func TestWithoutURL(t *testing.T) {
	raw := "https://user:pw@vendor.example/a?key=sekret&b=1"
	got := withoutURL(`Get "https://user:xxxxx@vendor.example/a?key=sekret&b=1": boom; key=sekret&b=1`, raw)
	assert.NotContains(t, got, "sekret")
	assert.Equal(t, "plain", withoutURL("plain", ""))
}
