package graphql

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// exportToolkit builds a toolkit with export wired to in-memory stores
// and a caller identity, and returns the stores so a test can read what
// was written.
func exportToolkit(t *testing.T, u *upstream, fixture string) (*Toolkit, *fakeAssets, *fakeBlobs) {
	t.Helper()
	tk := newToolkit(t, u, fixture, nil)
	assets := &fakeAssets{shareURL: "https://portal.example.com/s/abc"}
	blobs := newFakeBlobs()
	tk.SetExportDeps(ExportDeps{
		AssetStore: assets, VersionStore: assets, S3Client: blobs, ShareCreator: assets,
		S3Bucket: "assets", S3Prefix: "exports/", BaseURL: "https://portal.example.com/",
		GetUserContext: func(context.Context) *ExportUserContext {
			return &ExportUserContext{UserID: "u1", UserEmail: "a@example.com", SessionID: "s1"}
		},
	})
	return tk, assets, blobs
}

func callExport(t *testing.T, tk *Toolkit, in exportInput) *exportOutput {
	t.Helper()
	res, _, err := tk.handleExport(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("handleExport: %v", err)
	}
	if msg := errorMessage(res); msg != "" {
		t.Fatalf("graphql_export refused the call: %s", msg)
	}
	var out exportOutput
	decodeResult(t, res, &out)
	return &out
}

func refuseExport(t *testing.T, tk *Toolkit, in exportInput) string {
	t.Helper()
	res, _, err := tk.handleExport(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("handleExport: %v", err)
	}
	msg := errorMessage(res)
	if msg == "" {
		t.Fatal("the call was not refused")
	}
	return msg
}

func TestExportWritesTheResultToAnAssetAndNotThroughTheModel(t *testing.T) {
	u := newUpstream(t)
	u.respond = answer(`{"data":{"dataset":{"urn":"u1","name":"orders"}}}`)
	tk, assets, blobs := exportToolkit(t, u, "flat")

	out := callExport(t, tk, exportInput{
		Connection: "gql", Query: datasetDocument, Name: "datasets.json",
		Description: "Every dataset.", Tags: []string{"catalog"},
		CreatePublicLink: true,
	})

	if out.AssetID == "" || out.SizeBytes == 0 {
		t.Fatalf("out = %+v", out)
	}
	if out.PortalURL != "https://portal.example.com/portal/assets/"+out.AssetID {
		t.Errorf("portal url = %q", out.PortalURL)
	}
	if out.ShareURL != "https://portal.example.com/s/abc" {
		t.Errorf("share url = %q", out.ShareURL)
	}
	if out.ContentType != exportContentType {
		t.Errorf("content type = %q", out.ContentType)
	}
	// The data is in the asset, not in the response.
	if strings.Contains(resultTextOf(out), "orders") {
		t.Error("the data came back through the model context")
	}
	stored, err := blobs.only()
	if err != nil {
		t.Fatalf("stored object: %v", err)
	}
	var payload exportPayload
	if err := json.Unmarshal(stored, &payload); err != nil {
		t.Fatalf("the asset is not a GraphQL response: %v\n%s", err, stored)
	}
	if !strings.Contains(string(payload.Data), "orders") {
		t.Errorf("asset data = %s", payload.Data)
	}
	if len(assets.inserted) != 1 {
		t.Fatalf("assets = %+v", assets.inserted)
	}
	asset := assets.inserted[0]
	if asset.Name != "datasets.json" || asset.OwnerID != "u1" || asset.SessionID != "s1" {
		t.Errorf("asset = %+v", asset)
	}
	if !strings.HasPrefix(asset.S3Key, "exports/graphql_export/u1/") || !strings.HasSuffix(asset.S3Key, "/content.json") {
		t.Errorf("key = %q", asset.S3Key)
	}
	if len(asset.Provenance.ToolCalls) != 1 || asset.Provenance.ToolCalls[0].Parameters["query"] != datasetDocument {
		t.Errorf("provenance = %+v; the document is what produced the data", asset.Provenance)
	}
	if len(assets.versions) != 1 {
		t.Errorf("versions = %+v", assets.versions)
	}
}

// resultTextOf renders an export output the way the model would see it.
func resultTextOf(out *exportOutput) string {
	raw, _ := json.Marshal(out)
	return string(raw)
}

func TestExportAppliesTheSameGatesAsQuery(t *testing.T) {
	u := newUpstream(t)
	tk, _, _ := exportToolkit(t, u, "flat")
	cases := []struct {
		name, want string
		in         exportInput
	}{
		{"no name", "name is required", exportInput{Connection: "gql", Query: datasetDocument}},
		{"no connection", "connection is required", exportInput{Query: datasetDocument, Name: "x"}},
		{"a document the schema refuses", "does not validate", exportInput{Connection: "gql", Query: `{ dataset(urn:"x") { nope } }`, Name: "x"}},
		{"introspection", "graphql_discover", exportInput{Connection: "gql", Query: `{ __schema { types { name } } }`, Name: "x"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if msg := refuseExport(t, tk, c.in); !strings.Contains(msg, c.want) {
				t.Errorf("msg = %q; want %q", msg, c.want)
			}
		})
	}
}

func TestExportIsNotRegisteredWithoutItsDependencies(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "flat", nil)
	msg := refuseExport(t, tk, exportInput{Connection: "gql", Query: datasetDocument, Name: "x"})
	if !strings.Contains(msg, "not configured") {
		t.Errorf("msg = %q", msg)
	}
}

func TestExportRequiresAnIdentityToOwnTheAsset(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "flat", nil)
	tk.SetExportDeps(ExportDeps{AssetStore: &fakeAssets{}})
	msg := refuseExport(t, tk, exportInput{Connection: "gql", Query: datasetDocument, Name: "x"})
	if !strings.Contains(msg, "authentication required") {
		t.Errorf("msg = %q", msg)
	}
}

func TestExportIsAllOrNothingPastTheCap(t *testing.T) {
	u := newUpstream(t)
	u.respond = answer(`{"data":{"dataset":{"urn":"` + strings.Repeat("x", 2000) + `"}}}`)
	tk, _, blobs := exportToolkit(t, u, "flat")
	tk.SetExportDeps(ExportDeps{
		AssetStore: &fakeAssets{}, S3Client: blobs, Config: ExportConfig{MaxBytes: 256},
		GetUserContext: func(context.Context) *ExportUserContext {
			return &ExportUserContext{UserID: "u1"}
		},
	})
	msg := refuseExport(t, tk, exportInput{Connection: "gql", Query: datasetDocument, Name: "x"})
	if !strings.Contains(msg, "exceeds the graphql_export cap") {
		t.Errorf("msg = %q", msg)
	}
	if len(blobs.objects) != 0 {
		t.Error("a partial asset was written; it would read as a complete answer")
	}
}

func TestExportReturnsTheExistingAssetOnAMatchingIdempotencyKey(t *testing.T) {
	u := newUpstream(t)
	u.respond = answer(`{"data":{}}`)
	tk, assets, _ := exportToolkit(t, u, "flat")
	assets.existing = &ExportAssetRef{ID: "prior", SizeBytes: 42}

	out := callExport(t, tk, exportInput{
		Connection: "gql", Query: datasetDocument, Name: "x", IdempotencyKey: "k1",
	})

	if out.AssetID != "prior" || out.SizeBytes != 42 {
		t.Errorf("out = %+v", out)
	}
	if len(u.calls()) != 0 {
		t.Error("the endpoint was called again for an idempotent repeat")
	}
	// A failed lookup falls through to a fresh run rather than failing
	// closed while the database is degraded.
	assets.existing, assets.lookupErr = nil, errors.New("database is down")
	fresh := callExport(t, tk, exportInput{
		Connection: "gql", Query: datasetDocument, Name: "x", IdempotencyKey: "k1",
	})
	if fresh.AssetID == "prior" {
		t.Error("a failed lookup returned the prior asset")
	}
}

func TestExportSurvivesAShareOrVersionFailure(t *testing.T) {
	u := newUpstream(t)
	u.respond = answer(`{"data":{}}`)
	tk, assets, _ := exportToolkit(t, u, "flat")
	assets.shareErr = errors.New("share store is down")

	out := callExport(t, tk, exportInput{
		Connection: "gql", Query: datasetDocument, Name: "x", CreatePublicLink: true,
	})

	// The asset exists and the caller has its id; the link is what was
	// lost.
	if out.AssetID == "" || out.ShareURL != "" {
		t.Errorf("out = %+v", out)
	}
}

func TestExportReportsAStorageOrAssetWriteFailure(t *testing.T) {
	u := newUpstream(t)
	u.respond = answer(`{"data":{}}`)

	tk, _, blobs := exportToolkit(t, u, "flat")
	blobs.err = errors.New("bucket is gone")
	if msg := refuseExport(t, tk, exportInput{Connection: "gql", Query: datasetDocument, Name: "x"}); !strings.Contains(msg, "storage") {
		t.Errorf("msg = %q", msg)
	}

	tk2, assets2, _ := exportToolkit(t, u, "flat")
	assets2.insertErr = errors.New("assets table is gone")
	if msg := refuseExport(t, tk2, exportInput{Connection: "gql", Query: datasetDocument, Name: "x"}); !strings.Contains(msg, "recording the asset") {
		t.Errorf("msg = %q", msg)
	}
}

func TestExportWalksPagesLikeQueryDoes(t *testing.T) {
	u := newUpstream(t)
	u.respond = func(_ graphQLRequest, callNo int) (int, string) {
		if callNo == 1 {
			return 200, relayPage([]string{"a"}, "c1", true)
		}
		return 200, relayPage([]string{"b"}, "", false)
	}
	tk, _, blobs := exportToolkit(t, u, "namespaced")

	out := callExport(t, tk, exportInput{
		Connection: "gql", Query: pagedDocument, Name: "products.json",
		Paginate: &PaginateInput{Items: "masterData.product.query.edges", CursorVariable: "after"},
	})

	if out.Pagination == nil || out.Pagination.ItemsMerged != 2 {
		t.Fatalf("pagination = %+v", out.Pagination)
	}
	stored, err := blobs.only()
	if err != nil {
		t.Fatalf("stored: %v", err)
	}
	if !strings.Contains(string(stored), `"a"`) || !strings.Contains(string(stored), `"b"`) {
		t.Errorf("the asset does not hold every page: %s", stored)
	}
}

func TestExportDefaultsAndTimeoutResolution(t *testing.T) {
	cfg := applyExportDefaults(ExportConfig{})
	if cfg.MaxBytes != defaultExportMaxBytes || cfg.DefaultTimeout != defaultExportTimeout || cfg.MaxTimeout != defaultMaxExportTimeout {
		t.Errorf("defaults = %+v", cfg)
	}
	if got := resolveExportTimeout(0, cfg); got != cfg.DefaultTimeout {
		t.Errorf("no request = %v", got)
	}
	if got := resolveExportTimeout(99999, cfg); got != cfg.MaxTimeout {
		t.Errorf("an over-large request = %v", got)
	}
	if got := resolveExportTimeout(30, cfg); got.Seconds() != 30 {
		t.Errorf("a request under the ceiling = %v", got)
	}
}

func TestBuildExportPortalURL(t *testing.T) {
	if got := buildExportPortalURL("", "a1"); got != "" {
		t.Errorf("with no base URL the caller gets the id alone; got %q", got)
	}
	if got := buildExportPortalURL("https://p.example.com/", "a1"); got != "https://p.example.com/portal/assets/a1" {
		t.Errorf("url = %q", got)
	}
}

func TestBuildExportS3KeyWithoutAPrefix(t *testing.T) {
	if got := buildExportS3Key("", "u1", "a1"); got != "graphql_export/u1/a1/content.json" {
		t.Errorf("key = %q", got)
	}
}

// TestExportUserContextComesFromTheCallback proves identity is resolved
// through the platform's callback, so the toolkit never imports the
// middleware that holds it.
func TestExportUserContextComesFromTheCallback(t *testing.T) {
	deps := &ExportDeps{}
	if resolveExportUser(context.Background(), deps) != nil {
		t.Error("an unwired callback produced an identity")
	}
	deps.GetUserContext = func(context.Context) *ExportUserContext { return nil }
	if resolveExportUser(context.Background(), deps) != nil {
		t.Error("a callback that resolves nobody produced an identity")
	}
	deps.GetUserContext = func(context.Context) *ExportUserContext {
		return &ExportUserContext{UserID: "u1"}
	}
	if got := resolveExportUser(context.Background(), deps); got == nil || got.UserID != "u1" {
		t.Errorf("identity = %+v", got)
	}
}
