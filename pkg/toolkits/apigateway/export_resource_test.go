package apigateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// fakeLander stands in for the platform's managed-resource writer: it records
// what it was asked to land, reads the body to its end the way a storage write
// does, and answers with a version that climbs per path so a second landing at
// one address reads as the version it would really be.
type fakeLander struct {
	checked  []toolkit.ResourceDestination
	landed   []toolkit.ResourceDestination
	bodies   []string
	types    []string
	versions map[string]int
	checkErr error
	landErr  error
	tables   []string
}

func newFakeLander() *fakeLander { return &fakeLander{versions: map[string]int{}} }

func (f *fakeLander) CheckResourceDestination(_ context.Context, dest toolkit.ResourceDestination) error {
	f.checked = append(f.checked, dest)
	return f.checkErr
}

func (f *fakeLander) LandResource(
	_ context.Context, dest toolkit.ResourceDestination, content io.Reader, contentType string,
) (*toolkit.ResourceLanding, error) {
	body, err := io.ReadAll(content)
	if err != nil {
		return nil, fmt.Errorf("reading the content the caller streamed: %w", err)
	}
	if f.landErr != nil {
		return nil, f.landErr
	}
	f.landed = append(f.landed, dest)
	f.bodies = append(f.bodies, string(body))
	f.types = append(f.types, contentType)
	address := dest.Path + "/" + dest.Filename
	f.versions[address]++
	version := f.versions[address]
	return &toolkit.ResourceLanding{
		ResourceID: "res-" + address, Reference: "mcp:resource:res-" + address,
		URI: "mcp://user/u1/" + address, Filename: dest.Filename, Path: dest.Path,
		Scope: "user", ScopeID: "u1", ContentType: contentType,
		SizeBytes: int64(len(body)), Version: version, Created: version == 1,
		TableChanges: f.tables, Message: "Landed.",
	}, nil
}

// ordersDestination is the destination an api_export call names.
func ordersDestination() *toolkit.ResourceDestination {
	return &toolkit.ResourceDestination{Path: "datasets", Filename: "orders.csv"}
}

// exportToResource drives one api_export call at a resource destination against
// an upstream that answers what the test says.
func exportToResource(t *testing.T, status int, contentType, body string, in exportInput) (*mcp.CallToolResult, any, *fakeLander) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(upstream.Close)

	lander := newFakeLander()
	deps := defaultExportDeps(&fakeExportAssetStore{}, &fakeExportVersionStore{}, &fakeExportS3Client{})
	deps.ResourceLander = lander
	tk := buildExportTestToolkit(t, upstream.URL, &deps)

	in.Connection, in.Method = "crm", "GET"
	if in.Path == "" {
		in.Path = "/v1/orders"
	}
	if in.Name == "" {
		in.Name = "ACME orders"
	}
	res, payload, err := tk.handleExport(context.Background(), &mcp.CallToolRequest{}, in)
	if err != nil {
		t.Fatalf("handleExport: %v", err)
	}
	return res, payload, lander
}

// The response streams into the managed resource and the result carries the two
// names the caller hands to the next call, with no asset in sight.
func TestExportLandsInAManagedResource(t *testing.T) {
	const body = "id,total\n1,10\n"
	res, payload, lander := exportToResource(t, http.StatusOK, "text/csv", body, exportInput{
		Resource: ordersDestination(), Description: "Nightly pull", Tags: []string{"orders"},
	})

	if res.IsError {
		t.Fatalf("refused: %s", textContent(res))
	}
	out, ok := payload.(*exportOutput)
	if !ok {
		t.Fatalf("payload is not *exportOutput: %T", payload)
	}
	if out.AssetID != "" {
		t.Errorf("a resource export wrote an asset too: %q", out.AssetID)
	}
	if out.Resource == nil {
		t.Fatal("the result carries no resource")
	}
	if out.Resource.Reference != "mcp:resource:res-datasets/orders.csv" {
		t.Errorf("reference = %q", out.Resource.Reference)
	}
	if out.Resource.URI != "mcp://user/u1/datasets/orders.csv" {
		t.Errorf("uri = %q", out.Resource.URI)
	}
	if out.Resource.Version != 1 || !out.Resource.Created {
		t.Errorf("version = %d created = %v; want the first version of a new file", out.Resource.Version, out.Resource.Created)
	}
	if out.SizeBytes != int64(len(body)) {
		t.Errorf("size = %d; want %d", out.SizeBytes, len(body))
	}
	if len(lander.bodies) != 1 || lander.bodies[0] != body {
		t.Fatalf("the upstream body did not reach the library: %v", lander.bodies)
	}
	// The labels come from the export's own metadata fields, so one call has one
	// set of labels rather than two that can disagree.
	if lander.landed[0].DisplayName != "ACME orders" || lander.landed[0].Description != "Nightly pull" {
		t.Errorf("labels = %+v", lander.landed[0])
	}
	if strings.Contains(textContent(res), body) {
		t.Error("the response leaked the upstream body")
	}
}

// The destination is checked BEFORE the upstream is called, so a call that
// cannot land anywhere never reaches an endpoint that may change something.
func TestExportChecksTheDestinationBeforeCallingTheUpstream(t *testing.T) {
	var hits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	lander := newFakeLander()
	lander.checkErr = errors.New("you cannot write to the global scope")
	deps := defaultExportDeps(&fakeExportAssetStore{}, &fakeExportVersionStore{}, &fakeExportS3Client{})
	deps.ResourceLander = lander
	tk := buildExportTestToolkit(t, upstream.URL, &deps)

	res, _, _ := tk.handleExport(context.Background(), &mcp.CallToolRequest{}, exportInput{
		Connection: "crm", Method: "POST", Path: "/v1/orders", Name: "orders",
		Resource: ordersDestination(),
	})

	if res == nil || !res.IsError {
		t.Fatal("a destination the platform refuses was accepted")
	}
	if !strings.Contains(textContent(res), "global scope") {
		t.Errorf("the refusal does not name the reason: %s", textContent(res))
	}
	if hits != 0 {
		t.Errorf("the upstream was called %d times before the refusal; want 0", hits)
	}
	if len(lander.landed) != 0 {
		t.Error("a refused destination was written to")
	}
}

// An upstream that did not answer successfully is not landed: the file at that
// path has readers, and an error page must not become its next version. The
// answer is the result rather than an error (#1859), so a script can read the
// status and carry on.
func TestExportReturnsAnUnsuccessfulResponseWithoutLandingIt(t *testing.T) {
	res, payload, lander := exportToResource(t, http.StatusServiceUnavailable, "text/html",
		"<html>Service Unavailable</html>", exportInput{Resource: ordersDestination()})

	if res == nil || res.IsError {
		t.Fatalf("a 503 was reported as a tool error: %s", textContent(res))
	}
	out, _ := payload.(*exportOutput)
	if out == nil || out.Status != http.StatusServiceUnavailable || !out.ResourceUnchanged || out.Resource != nil {
		t.Fatalf("the result does not report an unchanged resource and the status: %+v", out)
	}
	if !out.Retryable {
		t.Error("a GET answered 503 is not marked retryable")
	}
	if got := out.UpstreamHeaders["Content-Type"]; len(got) != 1 || got[0] != "text/html" {
		t.Errorf("the upstream's headers are not reported: %v", out.UpstreamHeaders)
	}
	for _, want := range []string{"503", "datasets/orders.csv", "unchanged"} {
		if !strings.Contains(out.Message, want) {
			t.Errorf("the message does not say %q: %s", want, out.Message)
		}
	}
	if len(lander.landed) != 0 {
		t.Error("the library was written to anyway")
	}
}

// A 429 carries the interval the upstream asked for, which a script's host
// waits before asking again.
func TestExportReportsTheUpstreamsRetryAfter(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(upstream.Close)
	deps := defaultExportDeps(&fakeExportAssetStore{}, &fakeExportVersionStore{}, &fakeExportS3Client{})
	deps.ResourceLander = newFakeLander()
	tk := buildExportTestToolkit(t, upstream.URL, &deps)
	_, payload, err := tk.handleExport(context.Background(), &mcp.CallToolRequest{}, exportInput{
		Connection: "crm", Method: "POST", Path: "/v1/orders", Name: "orders", Resource: ordersDestination(),
	})
	if err != nil {
		t.Fatal(err)
	}
	out, _ := payload.(*exportOutput)
	if out == nil || !out.Retryable || out.RetryAfterSeconds != 7 {
		t.Fatalf("a 429 with Retry-After 7 = %+v; want retryable after 7 seconds, whatever the method", out)
	}
}

// A connection the upstream refuses is a transport failure, stamped with the
// outcome the error contract reads as an upstream that did not answer.
func TestExportStampsATransportFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := upstream.URL
	upstream.Close()
	deps := defaultExportDeps(&fakeExportAssetStore{}, &fakeExportVersionStore{}, &fakeExportS3Client{})
	deps.ResourceLander = newFakeLander()
	tk := buildExportTestToolkit(t, url, &deps)
	res, _, err := tk.handleExport(context.Background(), &mcp.CallToolRequest{}, exportInput{
		Connection: "crm", Method: "GET", Path: "/v1/orders", Name: "orders", Resource: ordersDestination(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res == nil || !res.IsError {
		t.Fatal("an unreachable upstream was not a tool error")
	}
	if got := res.Meta[observability.MetaAuditOutcome]; got != observability.OutcomeTransportErr {
		t.Errorf("audit outcome = %v; want %s", got, observability.OutcomeTransportErr)
	}
}

// The asset destination is unchanged by that rule: a failed response is a new
// file every time, the status is in the result, and keeping it is evidence.
func TestAnAssetExportStillKeepsAnUnsuccessfulResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("upstream is down"))
	}))
	t.Cleanup(upstream.Close)
	store := &fakeExportAssetStore{}
	deps := defaultExportDeps(store, &fakeExportVersionStore{}, &fakeExportS3Client{})
	tk := buildExportTestToolkit(t, upstream.URL, &deps)

	res, payload, err := tk.handleExport(context.Background(), &mcp.CallToolRequest{}, exportInput{
		Connection: "crm", Method: "GET", Path: "/v1/orders", Name: "orders",
	})
	if err != nil || res.IsError {
		t.Fatalf("the asset export was refused: %v %s", err, textContent(res))
	}
	out, _ := payload.(*exportOutput)
	if out == nil || out.Status != http.StatusServiceUnavailable || len(store.inserted) != 1 {
		t.Fatalf("the failed response was not kept as an asset: %+v", out)
	}
}

// Asset-only arguments are refused by name rather than ignored.
func TestExportRefusesAssetOnlyArgumentsWithAResourceDestination(t *testing.T) {
	cases := map[string]struct {
		in   exportInput
		want string
	}{
		"an idempotency key": {exportInput{Resource: ordersDestination(), IdempotencyKey: "k1"}, "idempotency_key"},
		"a public link":      {exportInput{Resource: ordersDestination(), CreatePublicLink: true}, "create_public_link"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			res, _, lander := exportToResource(t, http.StatusOK, "text/csv", "id\n1\n", tc.in)
			if res == nil || !res.IsError {
				t.Fatal("the call was not refused")
			}
			if !strings.Contains(textContent(res), tc.want) {
				t.Errorf("the refusal does not name %q: %s", tc.want, textContent(res))
			}
			if len(lander.landed) != 0 {
				t.Error("the library was written to anyway")
			}
		})
	}
}

// A deployment with no managed-resource library says so, and says what does work
// here.
func TestExportReportsADeploymentWithNoLibrary(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)
	deps := defaultExportDeps(&fakeExportAssetStore{}, &fakeExportVersionStore{}, &fakeExportS3Client{})
	tk := buildExportTestToolkit(t, upstream.URL, &deps)

	res, _, _ := tk.handleExport(context.Background(), &mcp.CallToolRequest{}, exportInput{
		Connection: "crm", Method: "GET", Path: "/v1/orders", Name: "orders",
		Resource: ordersDestination(),
	})

	if res == nil || !res.IsError {
		t.Fatal("a resource destination was accepted with no library to land in")
	}
	if !strings.Contains(textContent(res), "managed-resource library") {
		t.Errorf("the refusal does not name the missing piece: %s", textContent(res))
	}
}

// A landing failure is the caller's to read, in the words the library wrote it
// in.
func TestExportReportsALandingFailure(t *testing.T) {
	res, _, _ := exportToResource(t, http.StatusOK, "text/csv", "id\n1\n", exportInput{
		Resource: ordersDestination(),
	})
	if res.IsError {
		t.Fatalf("the control call was refused: %s", textContent(res))
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("id\n1\n"))
	}))
	t.Cleanup(upstream.Close)
	lander := newFakeLander()
	lander.landErr = errors.New("the response is larger than 100 MB, the managed-resource upload ceiling")
	deps := defaultExportDeps(&fakeExportAssetStore{}, &fakeExportVersionStore{}, &fakeExportS3Client{})
	deps.ResourceLander = lander
	tk := buildExportTestToolkit(t, upstream.URL, &deps)

	failed, _, _ := tk.handleExport(context.Background(), &mcp.CallToolRequest{}, exportInput{
		Connection: "crm", Method: "GET", Path: "/v1/orders", Name: "orders",
		Resource: ordersDestination(),
	})
	if failed == nil || !failed.IsError {
		t.Fatal("a failed landing was reported as a success")
	}
	if !strings.Contains(textContent(failed), "upload ceiling") {
		t.Errorf("the failure lost its reason: %s", textContent(failed))
	}
}

// A page walk lands in the library too, streaming the merged document into the
// file rather than into an asset, and reports both halves of what happened.
func TestExportWalkLandsInAManagedResource(t *testing.T) {
	var page int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		page++
		w.Header().Set("Content-Type", "application/json")
		if page == 1 {
			_, _ = w.Write([]byte(`{"data":[{"id":1}],"next":"c2"}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":2}]}`))
	}))
	t.Cleanup(upstream.Close)

	lander := newFakeLander()
	deps := defaultExportDeps(&fakeExportAssetStore{}, &fakeExportVersionStore{}, &fakeExportS3Client{})
	deps.ResourceLander = lander
	tk := buildExportTestToolkit(t, upstream.URL, &deps)

	res, payload, err := tk.handleExport(context.Background(), &mcp.CallToolRequest{}, exportInput{
		Connection: "crm", Method: "GET", Path: "/v1/orders", Name: "orders",
		Resource: &toolkit.ResourceDestination{Path: "datasets", Filename: "orders.json"},
		Paginate: &PaginateInput{Items: "data", CursorParam: "cursor", MaxPages: 5},
	})
	if err != nil || res.IsError {
		t.Fatalf("the walk was refused: %v %s", err, textContent(res))
	}
	out, ok := payload.(*exportOutput)
	if !ok || out.Resource == nil || out.WalkStats == nil {
		t.Fatalf("the walk did not report both halves: %+v", payload)
	}
	if out.Resource.Version != 1 || out.ItemsMerged != 2 {
		t.Errorf("version = %d items = %d", out.Resource.Version, out.ItemsMerged)
	}
	var merged []map[string]any
	if err := json.Unmarshal([]byte(lander.bodies[0]), &merged); err != nil {
		t.Fatalf("the landed document is not the merged array: %v (%q)", err, lander.bodies[0])
	}
	if len(merged) != 2 {
		t.Errorf("the landed document holds %d items; want 2", len(merged))
	}
	if lander.types[0] != applicationJSON {
		t.Errorf("a merged walk landed as %q", lander.types[0])
	}
}

// Landing again at the same path is the next version of the same file, which is
// the whole reason this destination exists.
func TestExportingTwiceToOnePathVersionsOneFile(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		_, _ = w.Write([]byte("id\n1\n"))
	}))
	t.Cleanup(upstream.Close)
	lander := newFakeLander()
	lander.tables = []string{"scratch.orders followed onto version 2."}
	deps := defaultExportDeps(&fakeExportAssetStore{}, &fakeExportVersionStore{}, &fakeExportS3Client{})
	deps.ResourceLander = lander
	tk := buildExportTestToolkit(t, upstream.URL, &deps)

	call := func() *exportOutput {
		res, payload, err := tk.handleExport(context.Background(), &mcp.CallToolRequest{}, exportInput{
			Connection: "crm", Method: "GET", Path: "/v1/orders", Name: "orders",
			Resource: ordersDestination(),
		})
		if err != nil || res.IsError {
			t.Fatalf("refused: %v %s", err, textContent(res))
		}
		out, _ := payload.(*exportOutput)
		return out
	}

	first, second := call(), call()
	if first.Resource.ResourceID != second.Resource.ResourceID {
		t.Errorf("the second export made a different file: %q vs %q",
			first.Resource.ResourceID, second.Resource.ResourceID)
	}
	if second.Resource.Version != 2 || second.Resource.Created {
		t.Errorf("the second export = version %d created %v", second.Resource.Version, second.Resource.Created)
	}
	if len(second.Resource.TableChanges) != 1 {
		t.Errorf("the tables over the file were not reported: %v", second.Resource.TableChanges)
	}
}
