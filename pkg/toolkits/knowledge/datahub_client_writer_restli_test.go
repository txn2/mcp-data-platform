package knowledge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	dhclient "github.com/txn2/mcp-datahub/pkg/client"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// restliFQCN maps an aspect name to the fully-qualified PDL class name the legacy
// Rest.li aspect GET keys its response envelope by.
var restliFQCN = map[string]string{
	"editableSchemaMetadata": "com.linkedin.schema.EditableSchemaMetadata",
	"globalTags":             "com.linkedin.common.GlobalTags",
	"glossaryTerms":          "com.linkedin.common.GlossaryTerms",
}

// restliGMS is a fake DataHub GMS speaking the legacy Rest.li protocol that the
// client selects whenever APIVersion is not "v3" - which is every deployment,
// since nothing sets it. Aspect GETs answer with the
// {"version":0,"aspect":{"<FQCN>": ...}} envelope a real Rest.li endpoint
// returns, and ingestProposal POSTs store the aspect so a later GET observes it.
//
// Modeling the real envelope is the point of this fake: one that answers the
// OpenAPI v3 {"value": ...} shape hides every read-modify-write defect on the v1
// path, because the merge base parses as empty and a full-replace write then
// looks indistinguishable from a merge (#1102).
type restliGMS struct {
	mu      sync.Mutex
	aspects map[string]json.RawMessage
}

func newRestliGMS() *restliGMS {
	return &restliGMS{aspects: map[string]json.RawMessage{}}
}

// seed stores an aspect as if a previous write had persisted it.
func (g *restliGMS) seed(aspectName, value string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.aspects[aspectName] = json.RawMessage(value)
}

// stored returns the aspect value currently held, or "" when absent.
func (g *restliGMS) stored(aspectName string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return string(g.aspects[aspectName])
}

func (g *restliGMS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodGet {
		g.serveGet(w, r)
		return
	}
	g.servePost(w, r)
}

func (g *restliGMS) serveGet(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("aspect")
	g.mu.Lock()
	value, ok := g.aspects[name]
	g.mu.Unlock()
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	fqcn := restliFQCN[name]
	if fqcn == "" {
		fqcn = "com.linkedin.Unknown"
	}
	_, _ = w.Write([]byte(`{"version":0,"aspect":{"` + fqcn + `":` + string(value) + `}}`))
}

// servePost decodes an ingestProposal and stores its GenericAspect value.
func (g *restliGMS) servePost(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var req struct {
		Proposal struct {
			AspectName string `json:"aspectName"`
			Aspect     struct {
				Value string `json:"value"`
			} `json:"aspect"`
		} `json:"proposal"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	// A real GMS rejects raw non-ASCII in GenericAspect.value: the PDL type is
	// bytes and Rest.li applies Avro-style encoding that admits U+0000-U+00FF
	// only. Reject it here so a write that would fail in production fails here.
	for i := range len(req.Proposal.Aspect.Value) {
		if req.Proposal.Aspect.Value[i] > maxASCIIRune {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"message":"Failed to parse GenericAspect value"}`))
			return
		}
	}
	g.mu.Lock()
	g.aspects[req.Proposal.AspectName] = json.RawMessage(req.Proposal.Aspect.Value)
	g.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

// newRestliWriter starts a fake Rest.li GMS and returns a writer bound to it.
func newRestliWriter(t *testing.T) (*DataHubClientWriter, *restliGMS) {
	t.Helper()
	gms := newRestliGMS()
	server := httptest.NewServer(gms)
	t.Cleanup(server.Close)
	return NewDataHubClientWriter(newTestClient(t, server.URL)), gms
}

// columnDescriptions reads back the stored editableSchemaMetadata as fieldPath ->
// description.
func columnDescriptions(t *testing.T, gms *restliGMS) map[string]string {
	t.Helper()
	raw := gms.stored("editableSchemaMetadata")
	require.NotEmpty(t, raw, "editableSchemaMetadata was never written")
	var aspect editableSchemaAspect
	require.NoError(t, json.Unmarshal([]byte(raw), &aspect))
	out := make(map[string]string, len(aspect.EditableSchemaFieldInfo))
	for _, f := range aspect.EditableSchemaFieldInfo {
		out[f.FieldPath] = f.Description
	}
	return out
}

// TestUpdateColumnDescriptionBatch_MergesAcrossCalls is the #1102 reproduction: a
// second batch must add to the columns an earlier batch documented, not replace
// them. Replacing caps a table at one batch (20 changes) of documented columns
// and silently discards everything written before.
func TestUpdateColumnDescriptionBatch_MergesAcrossCalls(t *testing.T) {
	writer, gms := newRestliWriter(t)
	ctx := context.Background()

	require.NoError(t, writer.UpdateColumnDescriptionBatch(ctx, testURN, map[string]string{
		"order_id":   "Order identifier",
		"customer":   "Customer name",
		"order_date": "Date the order was placed",
	}))
	require.NoError(t, writer.UpdateColumnDescriptionBatch(ctx, testURN, map[string]string{
		"amount": "Gross amount",
		"status": "Fulfillment status",
	}))

	assert.Equal(t, map[string]string{
		"order_id":   "Order identifier",
		"customer":   "Customer name",
		"order_date": "Date the order was placed",
		"amount":     "Gross amount",
		"status":     "Fulfillment status",
	}, columnDescriptions(t, gms))
}

// TestUpdateColumnDescriptionBatch_OverwritesSameColumn confirms merging does not
// duplicate a re-documented column: the newest description wins, once.
func TestUpdateColumnDescriptionBatch_OverwritesSameColumn(t *testing.T) {
	writer, gms := newRestliWriter(t)
	ctx := context.Background()

	require.NoError(t, writer.UpdateColumnDescriptionBatch(ctx, testURN, map[string]string{
		"order_id": "Old text",
		"customer": "Customer name",
	}))
	require.NoError(t, writer.UpdateColumnDescriptionBatch(ctx, testURN, map[string]string{
		"order_id": "New text",
		"amount":   "Gross amount",
	}))

	assert.Equal(t, map[string]string{
		"order_id": "New text",
		"customer": "Customer name",
		"amount":   "Gross amount",
	}, columnDescriptions(t, gms))
}

// TestUpdateColumnDescriptionBatch_NonASCII covers descriptions carrying non-ASCII
// text (curly quotes, em dashes, accented names). The Rest.li GenericAspect value
// must be \u-escaped or the proposal is rejected.
func TestUpdateColumnDescriptionBatch_NonASCII(t *testing.T) {
	writer, gms := newRestliWriter(t)

	require.NoError(t, writer.UpdateColumnDescriptionBatch(context.Background(), testURN, map[string]string{
		"customer": "Customer’s legal name — as filed",
		"region":   "Región de ventas",
		"status":   "Fulfillment state 🚚 (supplementary plane)",
	}))

	assert.Equal(t, map[string]string{
		"customer": "Customer’s legal name — as filed",
		"region":   "Región de ventas",
		"status":   "Fulfillment state 🚚 (supplementary plane)",
	}, columnDescriptions(t, gms))
}

// TestApplyTagChanges_PreservesExistingTags covers the same merge base for the
// globalTags aspect: adding a tag must not drop the tags already on the entity.
func TestApplyTagChanges_PreservesExistingTags(t *testing.T) {
	writer, gms := newRestliWriter(t)
	gms.seed("globalTags", `{"tags":[{"tag":"urn:li:tag:PII"},{"tag":"urn:li:tag:Curated"}]}`)

	require.NoError(t, writer.ApplyTagChanges(context.Background(), testURN, []string{"urn:li:tag:Reviewed"}, nil))

	var aspect globalTagsAspect
	require.NoError(t, json.Unmarshal([]byte(gms.stored("globalTags")), &aspect))
	urns := make([]string, 0, len(aspect.Tags))
	for _, raw := range aspect.Tags {
		urns = append(urns, tagURNOf(raw))
	}
	assert.Equal(t, []string{"urn:li:tag:PII", "urn:li:tag:Curated", "urn:li:tag:Reviewed"}, urns)
}

// TestApplyGlossaryTermChanges_PreservesExistingTerms is the glossaryTerms
// counterpart of TestApplyTagChanges_PreservesExistingTags.
func TestApplyGlossaryTermChanges_PreservesExistingTerms(t *testing.T) {
	writer, gms := newRestliWriter(t)
	gms.seed("glossaryTerms", `{"terms":[{"urn":"urn:li:glossaryTerm:Revenue"}]}`)

	require.NoError(t, writer.ApplyGlossaryTermChanges(
		context.Background(), testURN, []string{"urn:li:glossaryTerm:Margin"}, nil))

	var aspect glossaryTermsAspect
	require.NoError(t, json.Unmarshal([]byte(gms.stored("glossaryTerms")), &aspect))
	urns := make([]string, 0, len(aspect.Terms))
	for _, raw := range aspect.Terms {
		urns = append(urns, glossaryTermURNOf(raw))
	}
	assert.Equal(t, []string{"urn:li:glossaryTerm:Revenue", "urn:li:glossaryTerm:Margin"}, urns)
}

// TestReadTagURNs_RestliEnvelope covers the read that feeds resulting_state and the
// rollback before-image: an empty before-image lets a rollback strip tags the
// entity already carried.
func TestReadTagURNs_RestliEnvelope(t *testing.T) {
	writer, gms := newRestliWriter(t)
	gms.seed("globalTags", `{"tags":[{"tag":"urn:li:tag:PII"}]}`)

	urns, err := writer.readTagURNs(context.Background(), "dataset", testURN)

	require.NoError(t, err)
	assert.Equal(t, []string{"urn:li:tag:PII"}, urns)
}

func TestParseAspect_Envelopes(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "restli envelope keyed by fully-qualified class name",
			body: `{"version":0,"aspect":{"com.linkedin.common.GlobalTags":{"tags":[{"tag":"urn:li:tag:a"}]}}}`,
			want: []string{"urn:li:tag:a"},
		},
		{
			name: "openapi v3 value envelope",
			body: `{"value":{"tags":[{"tag":"urn:li:tag:b"}]}}`,
			want: []string{"urn:li:tag:b"},
		},
		{
			name: "restli envelope with an absent aspect",
			body: `{"version":0,"aspect":{}}`,
			want: []string{},
		},
		{
			name: "null value",
			body: `{"value":null}`,
			want: []string{},
		},
		{
			name: "null restli aspect body",
			body: `{"version":0,"aspect":{"com.linkedin.common.GlobalTags":null}}`,
			want: []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			aspect, err := parseGlobalTags([]byte(tt.body))
			require.NoError(t, err)
			urns := make([]string, 0, len(aspect.Tags))
			for _, raw := range aspect.Tags {
				urns = append(urns, tagURNOf(raw))
			}
			assert.Equal(t, tt.want, urns)
		})
	}
}

func TestParseAspect_MalformedRestliAspect(t *testing.T) {
	_, err := parseGlobalTags([]byte(`{"aspect":{"com.linkedin.common.GlobalTags":"not-an-object"}}`))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal aspect")
}

// TestPostIngestProposal_V3ForcesUpsert pins the OpenAPI v3 write to UPSERT
// semantics. DataHub defaults the v3 aspect write to CREATE, which fails with
// HTTP 400 once the aspect exists - and every write here is a read-modify-write.
func TestPostIngestProposal_V3ForcesUpsert(t *testing.T) {
	var gotURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			gotURL = r.URL.String()
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	cfg := dhclient.DefaultConfig()
	cfg.URL = server.URL
	cfg.Token = "test-token"
	cfg.RetryMax = 0
	cfg.APIVersion = dhclient.APIVersionV3
	c, err := dhclient.New(cfg)
	require.NoError(t, err)
	writer := NewDataHubClientWriter(c)

	require.NoError(t, writer.UpdateColumnDescriptionBatch(context.Background(), testURN, map[string]string{
		"order_id": "Order identifier",
		"customer": "Customer name",
	}))

	assert.Contains(t, gotURL, "createIfNotExists=false", "v3 aspect write must force UPSERT")
}
