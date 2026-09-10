package graphql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// errRefusedForTest stands for a refusal the platform's own lander would make,
// so a test can assert the words travel rather than restating them.
var errRefusedForTest = errors.New("you cannot write to the global scope, which is administrators only")

// fakeLander stands in for the platform's managed-resource writer, recording
// what it was asked to land and answering with a version that climbs per path.
type fakeLander struct {
	checked  []toolkit.ResourceDestination
	landed   []toolkit.ResourceDestination
	bodies   []string
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
	address := dest.Path + "/" + dest.Filename
	f.versions[address]++
	version := f.versions[address]
	return &toolkit.ResourceLanding{
		ResourceID: "res-1", Reference: "mcp:resource:res-1",
		URI: "mcp://user/u1/" + address, Filename: dest.Filename, Path: dest.Path,
		ContentType: contentType, SizeBytes: int64(len(body)), Version: version,
		Created: version == 1, Tables: f.tables, Message: "Landed.",
	}, nil
}

// landingToolkit is exportToolkit with a managed-resource library behind it,
// over the flat fixture schema every criterion here runs against.
func landingToolkit(t *testing.T, u *upstream) (*Toolkit, *fakeLander) {
	t.Helper()
	tk, _, _ := exportToolkit(t, u, "flat")
	lander := newFakeLander()
	deps := *tk.exportDeps
	deps.ResourceLander = lander
	tk.SetExportDeps(deps)
	return tk, lander
}

func datasetDestination() *toolkit.ResourceDestination {
	return &toolkit.ResourceDestination{Path: "datasets", Filename: "dataset.json"}
}

// The document's result lands in the file at the path, not in a new asset, and
// the result carries the names the next call takes.
func TestExportLandsTheResultInAManagedResource(t *testing.T) {
	u := newUpstream(t)
	u.respond = answer(`{"data":{"dataset":{"urn":"u1","name":"orders"}}}`)
	tk, lander := landingToolkit(t, u)

	out := callExport(t, tk, exportInput{
		Connection: "gql", Query: datasetDocument, Variables: jsonRaw(t, map[string]any{"urn": "u1"}),
		Name: "ACME dataset", Resource: datasetDestination(),
	})

	if out.AssetID != "" {
		t.Errorf("a resource export wrote an asset too: %q", out.AssetID)
	}
	if out.Resource == nil {
		t.Fatal("the result carries no resource")
	}
	if out.Resource.Reference != "mcp:resource:res-1" || out.Resource.Version != 1 || !out.Resource.Created {
		t.Errorf("resource = %+v", out.Resource)
	}
	if len(lander.bodies) != 1 || !strings.Contains(lander.bodies[0], `"orders"`) {
		t.Fatalf("the endpoint's answer did not reach the library: %v", lander.bodies)
	}
	// The landed document is the GraphQL response itself, the same payload the
	// asset arm stores, so whoever opens the file reads what the endpoint said.
	var payload map[string]any
	if err := json.Unmarshal([]byte(lander.bodies[0]), &payload); err != nil {
		t.Fatalf("the landed document is not the response: %v", err)
	}
	if _, ok := payload["data"]; !ok {
		t.Errorf("the landed document has no data key: %v", payload)
	}
	if lander.landed[0].DisplayName != "ACME dataset" {
		t.Errorf("the file was not labeled from the call's name: %+v", lander.landed[0])
	}
}

// A second export to the same path is the next version of the same file.
func TestExportingTwiceToOnePathVersionsOneFile(t *testing.T) {
	u := newUpstream(t)
	u.respond = answer(`{"data":{"dataset":{"urn":"u1","name":"orders"}}}`)
	tk, lander := landingToolkit(t, u)
	lander.tables = []string{"scratch.orders followed onto version 2."}

	in := exportInput{
		Connection: "gql", Query: datasetDocument, Variables: jsonRaw(t, map[string]any{"urn": "u1"}),
		Name: "ACME dataset", Resource: datasetDestination(),
	}
	first := callExport(t, tk, in)
	second := callExport(t, tk, in)

	if first.Resource.ResourceID != second.Resource.ResourceID {
		t.Error("the second export made a different file")
	}
	if second.Resource.Version != 2 || second.Resource.Created {
		t.Errorf("the second export = version %d created %v", second.Resource.Version, second.Resource.Created)
	}
	if len(second.Resource.Tables) != 1 {
		t.Errorf("the tables over the file were not reported: %v", second.Resource.Tables)
	}
}

// The destination is checked before the document is sent, so a mutation whose
// result has nowhere to go is never sent.
func TestExportChecksTheDestinationBeforeSendingTheDocument(t *testing.T) {
	u := newUpstream(t)
	u.respond = answer(`{"data":{"dataset":{"urn":"u1","name":"orders"}}}`)
	tk, lander := landingToolkit(t, u)
	lander.checkErr = errRefusedForTest

	msg := refuseExport(t, tk, exportInput{
		Connection: "gql", Query: datasetDocument, Variables: jsonRaw(t, map[string]any{"urn": "u1"}),
		Name: "ACME dataset", Resource: datasetDestination(),
	})

	if !strings.Contains(msg, errRefusedForTest.Error()) {
		t.Errorf("the refusal does not name the reason: %s", msg)
	}
	if len(u.calls()) != 0 {
		t.Errorf("the endpoint was called %d times before the refusal; want 0", len(u.calls()))
	}
	if len(lander.landed) != 0 {
		t.Error("a refused destination was written to")
	}
}

func TestExportRefusesAssetOnlyArgumentsWithAResourceDestination(t *testing.T) {
	cases := map[string]struct {
		mutate func(*exportInput)
		want   string
	}{
		"an idempotency key": {func(in *exportInput) { in.IdempotencyKey = "k1" }, "idempotency_key"},
		"a public link":      {func(in *exportInput) { in.CreatePublicLink = true }, "create_public_link"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			u := newUpstream(t)
			u.respond = answer(`{"data":{"dataset":{"urn":"u1","name":"orders"}}}`)
			tk, lander := landingToolkit(t, u)
			in := exportInput{
				Connection: "gql", Query: datasetDocument, Variables: jsonRaw(t, map[string]any{"urn": "u1"}),
				Name: "ACME dataset", Resource: datasetDestination(),
			}
			tc.mutate(&in)
			msg := refuseExport(t, tk, in)
			if !strings.Contains(msg, tc.want) {
				t.Errorf("the refusal does not name %q: %s", tc.want, msg)
			}
			if len(lander.landed) != 0 {
				t.Error("the library was written to anyway")
			}
		})
	}
}

// A landing failure fails the call, in the words the library wrote it in.
func TestExportReportsALandingFailure(t *testing.T) {
	u := newUpstream(t)
	u.respond = answer(`{"data":{"dataset":{"urn":"u1","name":"orders"}}}`)
	tk, lander := landingToolkit(t, u)
	lander.landErr = errors.New("the result is larger than 100 MB, the managed-resource upload ceiling")

	msg := refuseExport(t, tk, exportInput{
		Connection: "gql", Query: datasetDocument, Variables: jsonRaw(t, map[string]any{"urn": "u1"}),
		Name: "ACME dataset", Resource: datasetDestination(),
	})

	if !strings.Contains(msg, "upload ceiling") {
		t.Errorf("the failure lost its reason: %s", msg)
	}
}

// A deployment with no managed-resource library says so.
func TestExportReportsADeploymentWithNoLibrary(t *testing.T) {
	u := newUpstream(t)
	u.respond = answer(`{"data":{"dataset":{"urn":"u1","name":"orders"}}}`)
	tk, _, _ := exportToolkit(t, u, "flat")

	msg := refuseExport(t, tk, exportInput{
		Connection: "gql", Query: datasetDocument, Variables: jsonRaw(t, map[string]any{"urn": "u1"}),
		Name: "ACME dataset", Resource: datasetDestination(),
	})

	if !strings.Contains(msg, "managed-resource library") {
		t.Errorf("the refusal does not name the missing piece: %s", msg)
	}
}

// The closed input schema publishes exactly the arguments exportInput decodes
// (#1057). The tool's schema is closed to unknown properties, so an argument the
// struct reads and the schema omits is refused at the boundary and can never
// reach the handler: tags, idempotency_key and create_public_link were in that
// state until #1663 added the destination beside them.
func TestExportInputSchemaPublishesEveryArgumentItDecodes(t *testing.T) {
	var obj struct {
		AdditionalProperties *bool          `json:"additionalProperties"`
		Properties           map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(exportSchema, &obj); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	if obj.AdditionalProperties == nil || *obj.AdditionalProperties {
		t.Error("the graphql_export schema must declare \"additionalProperties\": false")
	}

	fields := map[string]bool{}
	for _, f := range reflect.VisibleFields(reflect.TypeFor[exportInput]()) {
		if tag, _, _ := strings.Cut(f.Tag.Get("json"), ","); tag != "" && tag != "-" {
			fields[tag] = true
		}
	}
	for name := range obj.Properties {
		if !fields[name] {
			t.Errorf("the schema publishes %q but exportInput does not decode it", name)
		}
	}
	for name := range fields {
		if _, published := obj.Properties[name]; !published {
			t.Errorf("exportInput decodes %q but the closed schema does not publish it", name)
		}
	}
}

// The destination schema published here is the shared one, so the four export
// tools describe one capability in one set of words.
func TestExportPublishesTheSharedDestinationSchema(t *testing.T) {
	var published struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(exportSchema, &published); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	var want, got any
	if err := json.Unmarshal([]byte(toolkit.ResourceDestinationSchema), &want); err != nil {
		t.Fatalf("unmarshal the shared schema: %v", err)
	}
	if err := json.Unmarshal(published.Properties["resource"], &got); err != nil {
		t.Fatalf("unmarshal the published property: %v", err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Error("graphql_export publishes its own version of the destination schema")
	}
}
