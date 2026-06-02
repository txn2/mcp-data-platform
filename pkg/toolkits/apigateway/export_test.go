package apigateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeExportAssetStore captures InsertExportAsset / idempotency
// calls so tests can assert what would have been written to the
// portal asset store. Concurrent invocations are not expected for
// these tests.
type fakeExportAssetStore struct {
	inserted     []ExportAsset
	insertErr    error
	idempLookups map[string]*ExportAssetRef
	idempErr     error
}

func (f *fakeExportAssetStore) InsertExportAsset(_ context.Context, asset ExportAsset) error {
	if f.insertErr != nil {
		return f.insertErr
	}
	f.inserted = append(f.inserted, asset)
	return nil
}

// errFakeAssetNotFound is returned by GetByIdempotencyKey when the
// (owner, key) tuple is not in the fake store. Distinct sentinel
// keeps the (nil, nil) "no value, no error" anti-pattern out of
// the lint output and matches the real Postgres adapter's "not
// found" surface (which returns a non-nil error).
var errFakeAssetNotFound = errors.New("fakeExportAssetStore: not found")

func (f *fakeExportAssetStore) GetByIdempotencyKey(_ context.Context, ownerID, key string) (*ExportAssetRef, error) {
	if f.idempErr != nil {
		return nil, f.idempErr
	}
	if f.idempLookups == nil {
		return nil, errFakeAssetNotFound
	}
	if ref, ok := f.idempLookups[ownerID+":"+key]; ok {
		return ref, nil
	}
	return nil, errFakeAssetNotFound
}

type fakeExportVersionStore struct {
	createdVersions []ExportVersion
	createErr       error
}

func (f *fakeExportVersionStore) CreateExportVersion(_ context.Context, ver ExportVersion) (int, error) {
	if f.createErr != nil {
		return 0, f.createErr
	}
	f.createdVersions = append(f.createdVersions, ver)
	return len(f.createdVersions), nil
}

type fakeExportS3Client struct {
	puts    []s3Put
	putErr  error
	lastKey string
}

type s3Put struct {
	Bucket, Key, ContentType string
	Data                     []byte
}

// PutObjectStream drains the streamed body so the captured Data matches
// what a real upload would persist. The size cap is enforced by the
// caller's cappedReader (the body it receives), which errors past the
// cap exactly as the real transfer manager would see it — so a read
// error here means "over cap" and no put is recorded, mirroring the
// abort-on-error contract.
func (f *fakeExportS3Client) PutObjectStream(_ context.Context, bucket, key string, body io.Reader, contentType string) (int64, error) {
	if f.putErr != nil {
		return 0, f.putErr
	}
	data, err := io.ReadAll(body)
	if err != nil {
		return int64(len(data)), fmt.Errorf("fakeExportS3Client: read body: %w", err)
	}
	f.puts = append(f.puts, s3Put{Bucket: bucket, Key: key, ContentType: contentType, Data: data})
	f.lastKey = key
	return int64(len(data)), nil
}

type fakeExportShareCreator struct {
	created []string
	url     string
	err     error
}

func (f *fakeExportShareCreator) CreatePublicShare(_ context.Context, assetID, _ string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.created = append(f.created, assetID)
	return f.url, nil
}

// buildExportTestToolkit assembles a Toolkit + connection wired
// against in-memory fakes. The upstream server's handler is the
// caller's contract — typically a small httptest server returning
// canned bytes so the test can assert what got persisted.
func buildExportTestToolkit(t *testing.T, upstreamURL string, deps *ExportDeps) *Toolkit {
	t.Helper()
	tk := New("primary")
	if err := tk.AddConnection("crm", map[string]any{
		"base_url": upstreamURL,
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	if deps != nil {
		tk.SetExportDeps(*deps)
	}
	return tk
}

func defaultExportDeps(store *fakeExportAssetStore, ver *fakeExportVersionStore, s3 *fakeExportS3Client) ExportDeps {
	return ExportDeps{
		AssetStore:   store,
		VersionStore: ver,
		S3Client:     s3,
		S3Bucket:     "exports",
		S3Prefix:     "data",
		BaseURL:      "https://platform.example.com",
		GetUserContext: func(_ context.Context) *ExportUserContext {
			return &ExportUserContext{
				UserID:    "u1",
				UserEmail: "alice@example.com",
				SessionID: "s-1",
			}
		},
	}
}

// TestHandleExport_HappyPath drives the full export flow and
// asserts the asset row, version row, and S3 PutObject were each
// invoked with the expected fields. The response must NOT carry
// the upstream body — only metadata.
func TestHandleExport_HappyPath(t *testing.T) {
	const wantBody = `{"items":[1,2,3]}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(wantBody))
	}))
	t.Cleanup(upstream.Close)

	store := &fakeExportAssetStore{}
	ver := &fakeExportVersionStore{}
	s3 := &fakeExportS3Client{}
	deps := defaultExportDeps(store, ver, s3)
	tk := buildExportTestToolkit(t, upstream.URL, &deps)

	r, payload, _ := tk.handleExport(context.Background(), &mcp.CallToolRequest{}, exportInput{
		Connection: "crm",
		Method:     "GET",
		Path:       "/v1/items",
		Name:       "items dump",
	})
	if r == nil || r.IsError {
		t.Fatalf("handleExport: error result: %+v", r)
	}
	out, ok := payload.(*exportOutput)
	if !ok {
		t.Fatalf("payload is not *exportOutput: %T", payload)
	}
	if out.AssetID == "" {
		t.Error("AssetID is empty")
	}
	if out.SizeBytes != int64(len(wantBody)) {
		t.Errorf("SizeBytes = %d; want %d", out.SizeBytes, len(wantBody))
	}
	if out.ContentType != "application/json" {
		t.Errorf("ContentType = %q", out.ContentType)
	}
	if out.PortalURL == "" || !strings.Contains(out.PortalURL, out.AssetID) {
		t.Errorf("PortalURL = %q; want it to contain asset id", out.PortalURL)
	}
	// Body bytes must NOT be in the response.
	resultText := textContent(r)
	if strings.Contains(resultText, wantBody) {
		t.Errorf("api_export response leaked upstream body bytes: %s", resultText)
	}

	// S3, asset, version each got one row with matching size.
	if len(s3.puts) != 1 {
		t.Fatalf("S3 PutObject called %d times; want 1", len(s3.puts))
	}
	if string(s3.puts[0].Data) != wantBody {
		t.Errorf("S3 data mismatch: %q", s3.puts[0].Data)
	}
	if len(store.inserted) != 1 {
		t.Fatalf("AssetStore.Insert called %d times; want 1", len(store.inserted))
	}
	if store.inserted[0].SizeBytes != int64(len(wantBody)) {
		t.Errorf("inserted asset SizeBytes = %d", store.inserted[0].SizeBytes)
	}
	if store.inserted[0].OwnerEmail != "alice@example.com" {
		t.Errorf("inserted asset OwnerEmail = %q", store.inserted[0].OwnerEmail)
	}
	if len(ver.createdVersions) != 1 {
		t.Errorf("VersionStore.Create called %d times; want 1", len(ver.createdVersions))
	}
}

// TestPersistExportAsset_VersionRowHasIDAndChangeSummary proves
// the regression fix from gate review round 1: portal_asset_versions.id
// is a TEXT PRIMARY KEY (migration #22), so without an explicit
// ID the second api_export call would collide with the first on
// the empty-string PK. ChangeSummary is set so the portal version
// view renders meaningful entries instead of blank rows.
func TestPersistExportAsset_VersionRowHasIDAndChangeSummary(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(upstream.Close)

	ver := &fakeExportVersionStore{}
	deps := defaultExportDeps(&fakeExportAssetStore{}, ver, &fakeExportS3Client{})
	tk := buildExportTestToolkit(t, upstream.URL, &deps)

	r, _, _ := tk.handleExport(context.Background(), &mcp.CallToolRequest{}, exportInput{
		Connection: "crm", Method: "GET", Path: "/x", Name: "x",
	})
	if r == nil || r.IsError {
		t.Fatalf("export error: %+v", r)
	}
	if len(ver.createdVersions) != 1 {
		t.Fatalf("CreateExportVersion called %d times; want 1", len(ver.createdVersions))
	}
	v := ver.createdVersions[0]
	if v.ID == "" {
		t.Error("version row ID is empty — would collide on the TEXT PRIMARY KEY on second call")
	}
	if v.ChangeSummary == "" {
		t.Error("version row ChangeSummary is empty — portal version list renders blank")
	}
}

// TestHandleExport_IdempotencyShortCircuit proves an existing
// (user, key) lookup match returns the existing asset without
// re-running the upstream call. Critical for retry-safety: the
// model can re-issue api_export with the same idempotency_key
// after a network blip and not produce duplicate assets.
func TestHandleExport_IdempotencyShortCircuit(t *testing.T) {
	upstreamHits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamHits++
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(upstream.Close)

	store := &fakeExportAssetStore{
		idempLookups: map[string]*ExportAssetRef{
			"u1:my-key": {ID: "existing-id", SizeBytes: 999},
		},
	}
	ver := &fakeExportVersionStore{}
	s3 := &fakeExportS3Client{}
	deps := defaultExportDeps(store, ver, s3)
	tk := buildExportTestToolkit(t, upstream.URL, &deps)

	r, payload, _ := tk.handleExport(context.Background(), &mcp.CallToolRequest{}, exportInput{
		Connection:     "crm",
		Method:         "GET",
		Path:           "/v1/items",
		Name:           "x",
		IdempotencyKey: "my-key",
	})
	if r == nil || r.IsError {
		t.Fatalf("idempotent return: error result: %+v", r)
	}
	out, _ := payload.(*exportOutput)
	if out.AssetID != "existing-id" {
		t.Errorf("AssetID = %q; want existing-id", out.AssetID)
	}
	if out.SizeBytes != 999 {
		t.Errorf("SizeBytes = %d; want 999 (existing asset)", out.SizeBytes)
	}
	if upstreamHits != 0 {
		t.Errorf("upstream hit %d times; idempotent return must NOT re-run the call", upstreamHits)
	}
	if len(s3.puts) != 0 {
		t.Errorf("S3 PutObject called %d times on idempotent return; want 0", len(s3.puts))
	}
}

// TestHandleExport_AuthRequired proves an unwired
// GetUserContext (or one returning nil) refuses the export. An
// asset row with no OwnerID would be unreachable from the portal
// "my exports" view, so failing closed is correct.
func TestHandleExport_AuthRequired(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(upstream.Close)

	deps := defaultExportDeps(&fakeExportAssetStore{}, &fakeExportVersionStore{}, &fakeExportS3Client{})
	deps.GetUserContext = func(_ context.Context) *ExportUserContext { return nil }
	tk := buildExportTestToolkit(t, upstream.URL, &deps)

	r, _, _ := tk.handleExport(context.Background(), &mcp.CallToolRequest{}, exportInput{
		Connection: "crm",
		Method:     "GET",
		Path:       "/v1/x",
		Name:       "x",
	})
	if r == nil || !r.IsError {
		t.Errorf("missing user context should produce IsError; got %+v", r)
	}
	if !strings.Contains(textContent(r), "authentication required") {
		t.Errorf("error message should mention authentication: %s", textContent(r))
	}
}

// TestHandleExport_DepsNotConfigured proves an unwired toolkit
// surfaces a clear error rather than panicking.
func TestHandleExport_DepsNotConfigured(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	t.Cleanup(upstream.Close)
	tk := buildExportTestToolkit(t, upstream.URL, nil) // no SetExportDeps
	r, _, _ := tk.handleExport(context.Background(), &mcp.CallToolRequest{}, exportInput{
		Connection: "crm",
		Method:     "GET",
		Path:       "/x",
		Name:       "x",
	})
	if r == nil || !r.IsError {
		t.Errorf("unwired deps should produce IsError; got %+v", r)
	}
}

// TestHandleExport_CapExceededRefusesAsset proves the ">cap →
// reject, do not write a partial asset" contract for a response with a
// declared Content-Length: the export is refused by the pre-check in
// runExport BEFORE any S3 write. A truncated portal asset would
// mislead the operator who clicks the URL.
func TestHandleExport_CapExceededRefusesAsset(t *testing.T) {
	const upstreamSize = 2048
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", upstreamSize))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(make([]byte, upstreamSize))
	}))
	t.Cleanup(upstream.Close)

	store := &fakeExportAssetStore{}
	ver := &fakeExportVersionStore{}
	s3 := &fakeExportS3Client{}
	deps := defaultExportDeps(store, ver, s3)
	deps.Config = ExportConfig{MaxBytes: 512} // tighter than upstream
	deps.Config = applyExportDefaults(deps.Config)
	deps.Config.MaxBytes = 512 // override the default applied above
	tk := buildExportTestToolkit(t, upstream.URL, &deps)

	r, _, _ := tk.handleExport(context.Background(), &mcp.CallToolRequest{}, exportInput{
		Connection: "crm",
		Method:     "GET",
		Path:       "/big",
		Name:       "big",
	})
	if r == nil || !r.IsError {
		t.Errorf("cap-exceeded should produce IsError; got %+v", r)
	}
	if len(s3.puts) != 0 {
		t.Errorf("cap-exceeded must NOT write to S3; got %d puts", len(s3.puts))
	}
	if len(store.inserted) != 0 {
		t.Errorf("cap-exceeded must NOT insert asset row; got %d inserts", len(store.inserted))
	}
}

// TestHandleExport_CapExceededChunkedAborts proves the over-cap path
// for a chunked response with no Content-Length: the pre-check cannot
// catch it, so the streaming upload enforces MaxBytes, aborts, and no
// asset row is written. (The real transfer manager also aborts the
// incomplete multipart upload; the fake models the no-asset outcome.)
func TestHandleExport_CapExceededChunkedAborts(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		// No Content-Length => chunked. Flush so the length stays undeclared.
		flusher, _ := w.(http.Flusher)
		for range 4 {
			_, _ = w.Write(make([]byte, 512))
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	t.Cleanup(upstream.Close)

	store := &fakeExportAssetStore{}
	ver := &fakeExportVersionStore{}
	s3 := &fakeExportS3Client{}
	deps := defaultExportDeps(store, ver, s3)
	deps.Config = applyExportDefaults(ExportConfig{MaxBytes: 512})
	deps.Config.MaxBytes = 512
	tk := buildExportTestToolkit(t, upstream.URL, &deps)

	r, _, _ := tk.handleExport(context.Background(), &mcp.CallToolRequest{}, exportInput{
		Connection: "crm", Method: "GET", Path: "/stream", Name: "big",
	})
	if r == nil || !r.IsError {
		t.Errorf("chunked over-cap should produce IsError; got %+v", r)
	}
	if len(s3.puts) != 0 {
		t.Errorf("chunked over-cap must NOT record a completed put; got %d", len(s3.puts))
	}
	if len(store.inserted) != 0 {
		t.Errorf("chunked over-cap must NOT insert asset row; got %d inserts", len(store.inserted))
	}
}

// TestHandleExport_StreamErrorNoAsset proves a storage/stream failure
// during the upload surfaces as an error and writes no asset or version
// row — the streamed body never produced a usable object.
func TestHandleExport_StreamErrorNoAsset(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"items":[1,2,3]}`)
	}))
	t.Cleanup(upstream.Close)

	store := &fakeExportAssetStore{}
	ver := &fakeExportVersionStore{}
	s3 := &fakeExportS3Client{putErr: errors.New("s3 unavailable")}
	deps := defaultExportDeps(store, ver, s3)
	tk := buildExportTestToolkit(t, upstream.URL, &deps)

	r, _, _ := tk.handleExport(context.Background(), &mcp.CallToolRequest{}, exportInput{
		Connection: "crm", Method: "GET", Path: "/v1/items", Name: "dump",
	})
	if r == nil || !r.IsError {
		t.Fatalf("stream error should produce IsError; got %+v", r)
	}
	if len(store.inserted) != 0 {
		t.Errorf("stream error must NOT insert asset row; got %d", len(store.inserted))
	}
	if len(ver.createdVersions) != 0 {
		t.Errorf("stream error must NOT insert version row; got %d", len(ver.createdVersions))
	}
}

// TestHandleExport_InsertAssetError proves a failed asset-row insert
// (after a successful stream) surfaces as an error result.
func TestHandleExport_InsertAssetError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(upstream.Close)

	store := &fakeExportAssetStore{insertErr: errors.New("db down")}
	deps := defaultExportDeps(store, &fakeExportVersionStore{}, &fakeExportS3Client{})
	tk := buildExportTestToolkit(t, upstream.URL, &deps)

	r, _, _ := tk.handleExport(context.Background(), &mcp.CallToolRequest{}, exportInput{
		Connection: "crm", Method: "GET", Path: "/x", Name: "x",
	})
	if r == nil || !r.IsError {
		t.Fatalf("insert error should produce IsError; got %+v", r)
	}
	if !strings.Contains(textContent(r), "insert asset row") {
		t.Errorf("payload = %s; want it to mention the insert failure", textContent(r))
	}
}

// TestHandleExport_VersionRowErrorIsNonFatal proves a failed version-row
// insert does NOT fail the export: the asset row is already in place and
// the model still gets a usable asset id.
func TestHandleExport_VersionRowErrorIsNonFatal(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(upstream.Close)

	store := &fakeExportAssetStore{}
	ver := &fakeExportVersionStore{createErr: errors.New("version table down")}
	deps := defaultExportDeps(store, ver, &fakeExportS3Client{})
	tk := buildExportTestToolkit(t, upstream.URL, &deps)

	r, payload, _ := tk.handleExport(context.Background(), &mcp.CallToolRequest{}, exportInput{
		Connection: "crm", Method: "GET", Path: "/x", Name: "x",
	})
	if r == nil || r.IsError {
		t.Fatalf("version-row failure must be non-fatal; got %+v", r)
	}
	out, _ := payload.(*exportOutput)
	if out == nil || out.AssetID == "" {
		t.Errorf("export should still return a usable asset id; got %+v", out)
	}
	if len(store.inserted) != 1 {
		t.Errorf("asset row should still be inserted; got %d", len(store.inserted))
	}
}

// TestHandleExport_RoutePolicyDenies proves the same persona-
// scoped policy gating api_invoke_endpoint also gates api_export.
// Without this check, an export would be a privilege-escalation
// path: get the bytes via api_export of a route the persona
// cannot api_invoke_endpoint.
func TestHandleExport_RoutePolicyDenies(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`secret`))
	}))
	t.Cleanup(upstream.Close)

	store := &fakeExportAssetStore{}
	deps := defaultExportDeps(store, &fakeExportVersionStore{}, &fakeExportS3Client{})
	tk := buildExportTestToolkit(t, upstream.URL, &deps)
	tk.SetRoutePolicy(denyAllRoutePolicy{})

	r, _, _ := tk.handleExport(context.Background(), &mcp.CallToolRequest{}, exportInput{
		Connection: "crm",
		Method:     "GET",
		Path:       "/secret",
		Name:       "x",
	})
	if r == nil || !r.IsError {
		t.Errorf("route policy denial should produce IsError; got %+v", r)
	}
	if len(store.inserted) != 0 {
		t.Errorf("denied call must NOT write asset row")
	}
}

type denyAllRoutePolicy struct{}

func (denyAllRoutePolicy) Allow(_ context.Context, _, _, _ string) (allowed bool, reason string) {
	return false, "test policy denies all"
}

// TestHandleExport_TransportErrorReturnsError proves an
// unreachable upstream surfaces the scrubbed error without
// touching S3 or the asset store.
func TestHandleExport_TransportErrorReturnsError(t *testing.T) {
	store := &fakeExportAssetStore{}
	s3 := &fakeExportS3Client{}
	deps := defaultExportDeps(store, &fakeExportVersionStore{}, s3)
	// Point at a port that nothing is listening on.
	tk := buildExportTestToolkit(t, "http://127.0.0.1:1", &deps)

	r, _, _ := tk.handleExport(context.Background(), &mcp.CallToolRequest{}, exportInput{
		Connection: "crm",
		Method:     "GET",
		Path:       "/x",
		Name:       "x",
	})
	if r == nil || !r.IsError {
		t.Errorf("transport error should produce IsError; got %+v", r)
	}
	if len(s3.puts) != 0 || len(store.inserted) != 0 {
		t.Errorf("transport failure must not touch S3 / asset store")
	}
}

// TestHandleExport_PublicLinkCreated proves create_public_link
// triggers the share creator and propagates the share URL into
// the response. Failures from CreatePublicShare are non-fatal —
// the asset is already created.
func TestHandleExport_PublicLinkCreated(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(upstream.Close)

	share := &fakeExportShareCreator{url: "https://platform.example.com/portal/view/tok-123"}
	deps := defaultExportDeps(&fakeExportAssetStore{}, &fakeExportVersionStore{}, &fakeExportS3Client{})
	deps.ShareCreator = share
	tk := buildExportTestToolkit(t, upstream.URL, &deps)

	_, payload, _ := tk.handleExport(context.Background(), &mcp.CallToolRequest{}, exportInput{
		Connection:       "crm",
		Method:           "GET",
		Path:             "/x",
		Name:             "x",
		CreatePublicLink: true,
	})
	out, _ := payload.(*exportOutput)
	if out.ShareURL != share.url {
		t.Errorf("ShareURL = %q; want %q", out.ShareURL, share.url)
	}
}

func TestHandleExport_PublicLinkErrorIsNonFatal(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(upstream.Close)

	share := &fakeExportShareCreator{err: errors.New("share store down")}
	deps := defaultExportDeps(&fakeExportAssetStore{}, &fakeExportVersionStore{}, &fakeExportS3Client{})
	deps.ShareCreator = share
	tk := buildExportTestToolkit(t, upstream.URL, &deps)

	r, payload, _ := tk.handleExport(context.Background(), &mcp.CallToolRequest{}, exportInput{
		Connection:       "crm",
		Method:           "GET",
		Path:             "/x",
		Name:             "x",
		CreatePublicLink: true,
	})
	if r == nil || r.IsError {
		t.Fatalf("share error should NOT fail the export: %+v", r)
	}
	out, _ := payload.(*exportOutput)
	if out.ShareURL != "" {
		t.Errorf("share error should leave ShareURL empty; got %q", out.ShareURL)
	}
	if out.AssetID == "" {
		t.Error("asset should still be created when share fails")
	}
}

func TestExtensionForContentType(t *testing.T) {
	cases := map[string]string{
		"application/json":                "json",
		"application/json; charset=utf-8": "json",
		"text/csv":                        "csv",
		"application/octet-stream":        "bin",
		"":                                "bin",
		"not/a-real-type":                 "bin",
	}
	for in, want := range cases {
		got := extensionForContentType(in)
		if got != want {
			// extension lookups can vary by platform mime DB; only
			// fail when the std lib returns nothing AND our
			// hand-rolled fallback should have caught it.
			if want == "json" || want == "csv" || want == "bin" {
				t.Errorf("extensionForContentType(%q) = %q; want %q", in, got, want)
			}
		}
	}
}

func TestBuildExportS3Key_FormatStable(t *testing.T) {
	got := buildExportS3Key("my-prefix", "user-1", "asset-abc", "application/json")
	want := "my-prefix/api_export/user-1/asset-abc.json"
	if got != want {
		t.Errorf("S3 key = %q; want %q", got, want)
	}
}

func TestBuildExportPortalURL_StripsTrailingSlash(t *testing.T) {
	got := buildExportPortalURL("https://platform.example.com/", "asset-1")
	want := "https://platform.example.com/portal/assets/asset-1"
	if got != want {
		t.Errorf("PortalURL = %q; want %q", got, want)
	}
	if got := buildExportPortalURL("", "asset-1"); got != "" {
		t.Errorf("empty BaseURL should yield empty PortalURL; got %q", got)
	}
}

// TestRegisterExportTool_AddsToToolListWhenDepsWired exercises
// the registerExportTool body. With ExportDeps wired, the tool
// must register on the MCP server AND appear in tk.Tools().
// Without ExportDeps, both must skip silently.
func TestRegisterExportTool_AddsToToolListWhenDepsWired(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	t.Cleanup(upstream.Close)
	deps := defaultExportDeps(&fakeExportAssetStore{}, &fakeExportVersionStore{}, &fakeExportS3Client{})

	wired := buildExportTestToolkit(t, upstream.URL, &deps)
	if !contains(wired.Tools(), exportToolName) {
		t.Errorf("Tools() missing %q after wiring; got %v", exportToolName, wired.Tools())
	}

	unwired := buildExportTestToolkit(t, upstream.URL, nil)
	if contains(unwired.Tools(), exportToolName) {
		t.Errorf("Tools() includes %q without wiring; got %v", exportToolName, unwired.Tools())
	}

	// And the actual MCP registration: registerExportTool with deps
	// wired must call AddTool; without deps wired must skip. We
	// can't introspect the server's tool list easily without a real
	// MCP transport, so the contract assertion is via Tools()
	// above. RegisterTools also must not panic in either state.
	wired.RegisterTools(mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil))
	unwired.RegisterTools(mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil))
}

func contains(haystack []string, needle string) bool {
	return slices.Contains(haystack, needle)
}

func TestResolveExportTimeout_CapsAtMax(t *testing.T) {
	cfg := applyExportDefaults(ExportConfig{})
	// Zero input falls back to the default.
	if got := resolveExportTimeout(0, cfg); got != cfg.DefaultTimeout {
		t.Errorf("zero input should fall back to default; got %v", got)
	}
	// 30s — under MaxTimeout, passes through.
	if got := resolveExportTimeout(30, cfg); got.Seconds() != 30 {
		t.Errorf("30s input should pass through; got %v", got)
	}
	// Way over MaxTimeout caps to MaxTimeout.
	if got := resolveExportTimeout(int(cfg.MaxTimeout.Seconds())+10000, cfg); got != cfg.MaxTimeout {
		t.Errorf("oversized input should cap at MaxTimeout; got %v", got)
	}
}
