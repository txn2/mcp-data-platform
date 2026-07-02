package knowledge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dhclient "github.com/txn2/mcp-datahub/pkg/client"
	"github.com/txn2/mcp-datahub/pkg/types"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testURN is a valid DataHub dataset URN used across tests.
const testURN = "urn:li:dataset:(urn:li:dataPlatform:trino,catalog.schema.table,PROD)"

// newTestClient creates a dhclient.Client pointing at the given test server.
func newTestClient(t *testing.T, serverURL string) *dhclient.Client {
	t.Helper()

	cfg := dhclient.DefaultConfig()
	cfg.URL = serverURL
	cfg.Token = "test-token"
	cfg.RetryMax = 0 // no retries in tests

	c, err := dhclient.New(cfg)
	require.NoError(t, err)
	return c
}

// graphQLResponse is a minimal GraphQL response for tests.
type graphQLResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []any           `json:"errors,omitempty"`
}

func TestDataHubClientWriter_GetCurrentMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Tags and glossary terms are read from the REST aspects (GET); the entity
		// description/owners come from the GraphQL entity query (POST).
		if r.Method == http.MethodGet {
			switch {
			case strings.Contains(r.URL.String(), "globalTags"):
				_, _ = w.Write([]byte(`{"value":{"tags":[{"tag":"urn:li:tag:PII"},{"tag":"urn:li:tag:Sensitive"}]}}`))
			case strings.Contains(r.URL.String(), "glossaryTerms"):
				_, _ = w.Write([]byte(`{"value":{"terms":[{"urn":"urn:li:glossaryTerm:Revenue"}]}}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
			return
		}
		resp := graphQLResponse{
			Data: json.RawMessage(`{
				"entity": {
					"urn": "` + testURN + `",
					"type": "DATASET",
					"name": "table",
					"description": "A test table",
					"platform": {"name": "trino"},
					"properties": {"name": "", "description": "", "customProperties": []},
					"editableProperties": {"description": "Editable desc"},
					"subTypes": {"typeNames": ["table"]},
					"ownership": {
						"owners": [
							{
								"owner": {
									"urn": "urn:li:corpuser:alice",
									"username": "alice",
									"name": "Alice",
									"info": {"displayName": "Alice Smith", "email": "alice@example.com"}
								},
								"type": "TECHNICAL_OWNER"
							}
						]
					},
					"tags": {
						"tags": [
							{"tag": {"urn": "urn:li:tag:PII", "name": "PII", "description": ""}},
							{"tag": {"urn": "urn:li:tag:Sensitive", "name": "Sensitive", "description": ""}}
						]
					},
					"glossaryTerms": {
						"terms": [
							{"term": {"urn": "urn:li:glossaryTerm:Revenue", "properties": {"name": "Revenue", "description": ""}}}
						]
					},
					"domain": {"domain": {"urn": "", "properties": {"name": "", "description": ""}}},
					"deprecation": {"deprecated": false}
				}
			}`),
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	meta, err := writer.GetCurrentMetadata(context.Background(), testURN)

	require.NoError(t, err)
	require.NotNil(t, meta)

	// Description should be the editable one (takes precedence in client)
	assert.Equal(t, "Editable desc", meta.Description)
	assert.Equal(t, []string{"urn:li:tag:PII", "urn:li:tag:Sensitive"}, meta.Tags)
	assert.Equal(t, []string{"urn:li:glossaryTerm:Revenue"}, meta.GlossaryTerms)
	assert.Equal(t, []string{"urn:li:corpuser:alice"}, meta.Owners)
}

func TestDataHubClientWriter_GetCurrentMetadata_Empty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := graphQLResponse{
			Data: json.RawMessage(`{
				"entity": {
					"urn": "` + testURN + `",
					"type": "DATASET",
					"name": "table",
					"platform": {"name": "trino"},
					"properties": {"name": "", "description": "", "customProperties": []},
					"editableProperties": {"description": ""},
					"subTypes": {"typeNames": []},
					"ownership": {"owners": []},
					"tags": {"tags": []},
					"glossaryTerms": {"terms": []},
					"domain": {"domain": {"urn": "", "properties": {"name": "", "description": ""}}},
					"deprecation": {"deprecated": false}
				}
			}`),
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	meta, err := writer.GetCurrentMetadata(context.Background(), testURN)

	require.NoError(t, err)
	assert.Empty(t, meta.Description)
	assert.Empty(t, meta.Tags)
	assert.Empty(t, meta.GlossaryTerms)
	assert.Empty(t, meta.Owners)
}

func TestDataHubClientWriter_GetCurrentMetadata_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	_, err := writer.GetCurrentMetadata(context.Background(), testURN)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "getting entity")
}

// TestDataHubClientWriter_GetCurrentMetadata_GlossaryTerm is the #723 regression for
// glossaryTerm URNs: description and owners come from the dedicated getter, and tags
// and glossary terms come from the entity query (mcp-datahub v1.10.2 surfaces them
// via the experimental aspects read). All reads are GraphQL; no REST aspect GET is
// issued because glossaryTerm does not expose those aspects over REST.
func TestDataHubClientWriter_GetCurrentMetadata_GlossaryTerm(t *testing.T) {
	const termURN = "urn:li:glossaryTerm:Revenue"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method, "glossaryTerm must not issue REST aspect GETs")
		body, _ := io.ReadAll(r.Body)
		s := string(body)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(s, "getEntityAspects"):
			// Raw-aspect read (v1.10.2): payloads are escaped JSON strings.
			_, _ = w.Write([]byte(`{"data":{"entity":{"aspects":[` +
				`{"aspectName":"globalTags","payload":"{\"tags\":[{\"tag\":\"urn:li:tag:Curated\"}]}"},` +
				`{"aspectName":"glossaryTerms","payload":"{\"terms\":[{\"urn\":\"urn:li:glossaryTerm:Related\"}]}"}` +
				`]}}}`))
		case strings.Contains(s, "getGlossaryTerm"):
			_ = json.NewEncoder(w).Encode(graphQLResponse{
				Data: json.RawMessage(`{"glossaryTerm":{"urn":"` + termURN + `","name":"Revenue",` +
					`"properties":{"name":"Revenue","description":"Recognized revenue"},` +
					`"ownership":{"owners":[{"owner":{"urn":"urn:li:corpuser:alice"},"type":"BUSINESS_OWNER"}]}}}`),
			})
		default: // getEntity
			_ = json.NewEncoder(w).Encode(graphQLResponse{
				Data: json.RawMessage(`{"entity":{"urn":"` + termURN + `","type":"GLOSSARY_TERM"}}`),
			})
		}
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	meta, err := writer.GetCurrentMetadata(context.Background(), termURN)
	require.NoError(t, err)
	assert.Equal(t, "Recognized revenue", meta.Description)
	assert.Equal(t, []string{"urn:li:corpuser:alice"}, meta.Owners)
	assert.Equal(t, []string{"urn:li:tag:Curated"}, meta.Tags)
	assert.Equal(t, []string{"urn:li:glossaryTerm:Related"}, meta.GlossaryTerms)
}

// TestDataHubClientWriter_GetCurrentMetadata_DataProduct is the #723 regression for
// dataProduct URNs: description and owners come from the dedicated getter, while
// tags and glossary terms are read from the authoritative REST aspects. Complete
// metadata here is what keeps a rollback before-image from being empty and stripping
// pre-existing tags/terms.
func TestDataHubClientWriter_GetCurrentMetadata_DataProduct(t *testing.T) {
	const productURN = "urn:li:dataProduct:revenue_analytics"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			switch {
			case strings.Contains(r.URL.String(), "globalTags"):
				_, _ = w.Write([]byte(`{"value":{"tags":[{"tag":"urn:li:tag:Curated"}]}}`))
			case strings.Contains(r.URL.String(), "glossaryTerms"):
				_, _ = w.Write([]byte(`{"value":{"terms":[{"urn":"urn:li:glossaryTerm:Revenue"}]}}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
			return
		}
		_ = json.NewEncoder(w).Encode(graphQLResponse{
			Data: json.RawMessage(`{"dataProduct":{"urn":"` + productURN + `",` +
				`"properties":{"name":"Revenue Analytics","description":"Curated revenue datasets"},` +
				`"ownership":{"owners":[{"owner":{"urn":"urn:li:corpuser:bob"},"type":"TECHNICAL_OWNER"}]}}}`),
		})
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	meta, err := writer.GetCurrentMetadata(context.Background(), productURN)
	require.NoError(t, err)
	assert.Equal(t, "Curated revenue datasets", meta.Description)
	assert.Equal(t, []string{"urn:li:corpuser:bob"}, meta.Owners)
	assert.Equal(t, []string{"urn:li:tag:Curated"}, meta.Tags)
	assert.Equal(t, []string{"urn:li:glossaryTerm:Revenue"}, meta.GlossaryTerms)
}

// TestDataHubClientWriter_GetCurrentMetadata_Domain verifies that a domain (a
// GraphQL-only type) gets its tags/glossary terms from the entity query (no REST
// aspect GET), as surfaced by mcp-datahub v1.10.2.
func TestDataHubClientWriter_GetCurrentMetadata_Domain(t *testing.T) {
	const domainURN = "urn:li:domain:sales"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method, "domain must not issue REST aspect GETs")
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(string(body), "getEntityAspects") {
			_, _ = w.Write([]byte(`{"data":{"entity":{"aspects":[` +
				`{"aspectName":"globalTags","payload":"{\"tags\":[{\"tag\":\"urn:li:tag:Org\"}]}"}` +
				`]}}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(graphQLResponse{
			Data: json.RawMessage(`{"entity":{"urn":"` + domainURN + `","type":"DOMAIN"}}`),
		})
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	meta, err := writer.GetCurrentMetadata(context.Background(), domainURN)
	require.NoError(t, err)
	assert.Equal(t, []string{"urn:li:tag:Org"}, meta.Tags)
}

// TestDataHubClientWriter_GetCurrentMetadata_GlossaryTermEntityError verifies that a
// failed entity read (the source of a glossaryTerm's tags/terms) propagates.
func TestDataHubClientWriter_GetCurrentMetadata_GlossaryTermEntityError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(string(body), "getGlossaryTerm") {
			_ = json.NewEncoder(w).Encode(graphQLResponse{
				Data: json.RawMessage(`{"glossaryTerm":{"urn":"urn:li:glossaryTerm:Revenue","properties":{"description":"d"}}}`),
			})
			return
		}
		// The entity read (for tags/terms) fails.
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	_, err := writer.GetCurrentMetadata(context.Background(), "urn:li:glossaryTerm:Revenue")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "getting entity")
}

// TestDataHubClientWriter_GetCurrentMetadata_DatasetTagReadError verifies that a
// failed REST tag read on the generic (dataset) path propagates.
func TestDataHubClientWriter_GetCurrentMetadata_DatasetTagReadError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			// Fail only the globalTags read so the error is deterministically the tag
			// read (the two aspect reads run concurrently).
			if strings.Contains(r.URL.String(), "globalTags") {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"value":{"terms":[]}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(graphQLResponse{
			Data: json.RawMessage(`{"entity":{"urn":"` + testURN + `","type":"DATASET","editableProperties":{"description":"d"}}}`),
		})
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	_, err := writer.GetCurrentMetadata(context.Background(), testURN)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading tags")
}

// TestDataHubClientWriter_GetCurrentMetadata_TagReadError verifies that a failed
// REST tag read propagates rather than silently yielding an empty tag set (an empty
// before-image would let rollback strip pre-existing tags).
func TestDataHubClientWriter_GetCurrentMetadata_TagReadError(t *testing.T) {
	const productURN = "urn:li:dataProduct:x"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			// Fail only the globalTags read (the two aspect reads run concurrently).
			if strings.Contains(r.URL.String(), "globalTags") {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"value":{"terms":[]}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(graphQLResponse{
			Data: json.RawMessage(`{"dataProduct":{"urn":"` + productURN + `","properties":{"description":"d"}}}`),
		})
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	_, err := writer.GetCurrentMetadata(context.Background(), productURN)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading tags")
}

// TestDataHubClientWriter_GetCurrentMetadata_TermReadError verifies the glossary-term
// read error path propagates (globalTags succeeds, glossaryTerms fails).
func TestDataHubClientWriter_GetCurrentMetadata_TermReadError(t *testing.T) {
	const productURN = "urn:li:dataProduct:x"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if strings.Contains(r.URL.String(), "glossaryTerms") {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"value":{"tags":[]}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(graphQLResponse{
			Data: json.RawMessage(`{"dataProduct":{"urn":"` + productURN + `","properties":{"description":"d"}}}`),
		})
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	_, err := writer.GetCurrentMetadata(context.Background(), productURN)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading glossary terms")
}

// TestDataHubClientWriter_GetCurrentMetadata_UnparseableURN verifies that a URN the
// parser rejects falls back to the generic entity read rather than erroring early.
func TestDataHubClientWriter_GetCurrentMetadata_UnparseableURN(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(graphQLResponse{
			Data: json.RawMessage(`{"entity":{"urn":"x","type":"DATASET","editableProperties":{"description":"D"}}}`),
		})
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	meta, err := writer.GetCurrentMetadata(context.Background(), "not-a-urn")
	require.NoError(t, err)
	assert.Equal(t, "D", meta.Description)
}

func TestDataHubClientWriter_GetCurrentMetadata_GlossaryTermError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	_, err := writer.GetCurrentMetadata(context.Background(), "urn:li:glossaryTerm:Revenue")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "getting glossary term")
}

func TestDataHubClientWriter_GetCurrentMetadata_DataProductError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	_, err := writer.GetCurrentMetadata(context.Background(), "urn:li:dataProduct:x")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "getting data product")
}

func TestDataHubClientWriter_UpdateDescription(t *testing.T) {
	var receivedBody json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// UpdateDescription uses REST POST to /aspects?action=ingestProposal
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/aspects") {
			_ = json.NewDecoder(r.Body).Decode(&receivedBody)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.UpdateDescription(context.Background(), testURN, "Updated description")

	require.NoError(t, err)
	assert.NotEmpty(t, receivedBody)
}

func TestDataHubClientWriter_UpdateDescription_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`server error`))
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.UpdateDescription(context.Background(), testURN, "desc")

	assert.Error(t, err)
}

// globalTagsServer returns a test server that serves the given existing tags on
// GET (as the globalTags aspect) and captures the POSTed aspect body. A nil
// existing slice responds 404 (no aspect yet). It records the number of GET and
// POST requests so tests can assert a single read-modify-write.
func globalTagsServer(t *testing.T, existing []string, posted *[]byte, gets, posts *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			*gets++
			if existing == nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			assocs := make([]string, 0, len(existing))
			for _, tag := range existing {
				assocs = append(assocs, `{"tag":"`+tag+`"}`)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"value":{"tags":[` + strings.Join(assocs, ",") + `]}}`))
		case http.MethodPost:
			*posts++
			*posted, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
		}
	}))
}

// graphQLMutationServer records every GraphQL mutation body posted to it and
// replies with a generic success payload for the owner/domain mutations. Each
// captured body is the raw {"query":...,"variables":...} envelope so tests can
// assert which operation fired, in what order, and with which URNs.
func graphQLMutationServer(t *testing.T, bodies *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		*bodies = append(*bodies, string(raw))
		w.Header().Set("Content-Type", "application/json")
		// All four mutations return a single boolean field; supplying every field
		// keeps one handler valid for add/remove owner and set/unset domain.
		_, _ = w.Write([]byte(`{"data":{"addOwner":true,"removeOwner":true,"setDomain":true,"unsetDomain":true}}`))
	}))
}

func TestDataHubClientWriter_ApplyOwnerChanges_RemovesThenAdds(t *testing.T) {
	var bodies []string
	server := graphQLMutationServer(t, &bodies)
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.ApplyOwnerChanges(context.Background(), testURN,
		[]OwnerChange{{OwnerURN: "urn:li:corpuser:alice", OwnershipType: "TECHNICAL_OWNER"}},
		[]string{"urn:li:corpuser:bob"},
	)
	require.NoError(t, err)
	require.Len(t, bodies, 2, "one remove then one add")
	assert.Contains(t, bodies[0], "removeOwner", "remove must be applied first")
	assert.Contains(t, bodies[0], "urn:li:corpuser:bob")
	assert.Contains(t, bodies[1], "addOwner", "add must follow the remove")
	assert.Contains(t, bodies[1], "urn:li:corpuser:alice")
}

// TestDataHubClientWriter_ApplyOwnerChanges_SkipsAddInRemove verifies an owner
// present in both add and remove is left removed (never re-added).
func TestDataHubClientWriter_ApplyOwnerChanges_SkipsAddInRemove(t *testing.T) {
	var bodies []string
	server := graphQLMutationServer(t, &bodies)
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.ApplyOwnerChanges(context.Background(), testURN,
		[]OwnerChange{{OwnerURN: "urn:li:corpuser:carol"}},
		[]string{"urn:li:corpuser:carol"},
	)
	require.NoError(t, err)
	require.Len(t, bodies, 1, "only the remove fires; the add is skipped")
	assert.Contains(t, bodies[0], "removeOwner")
}

func TestDataHubClientWriter_ApplyOwnerChanges_NoChanges(t *testing.T) {
	var bodies []string
	server := graphQLMutationServer(t, &bodies)
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	require.NoError(t, writer.ApplyOwnerChanges(context.Background(), testURN, nil, nil))
	assert.Empty(t, bodies, "no-op must not call DataHub")
}

func TestDataHubClientWriter_ApplyOwnerChanges_RemoveError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.ApplyOwnerChanges(context.Background(), testURN, nil, []string{"urn:li:corpuser:bob"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "removing owner")
}

func TestDataHubClientWriter_SetDomain(t *testing.T) {
	var bodies []string
	server := graphQLMutationServer(t, &bodies)
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.SetDomain(context.Background(), testURN, "urn:li:domain:finance")
	require.NoError(t, err)
	require.Len(t, bodies, 1)
	assert.Contains(t, bodies[0], "setDomain")
	assert.Contains(t, bodies[0], "urn:li:domain:finance")
}

func TestDataHubClientWriter_SetDomain_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.SetDomain(context.Background(), testURN, "urn:li:domain:finance")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "setting domain")
}

func TestDataHubClientWriter_UnsetDomain(t *testing.T) {
	var bodies []string
	server := graphQLMutationServer(t, &bodies)
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.UnsetDomain(context.Background(), testURN)
	require.NoError(t, err)
	require.Len(t, bodies, 1)
	assert.Contains(t, bodies[0], "unsetDomain")
}

func TestDataHubClientWriter_UnsetDomain_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.UnsetDomain(context.Background(), testURN)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsetting domain")
}

func TestDataHubClientWriter_ApplyTagChanges_AddMergesExisting(t *testing.T) {
	var posted []byte
	var gets, posts int
	server := globalTagsServer(t, []string{"urn:li:tag:Existing"}, &posted, &gets, &posts)
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.ApplyTagChanges(context.Background(), testURN, []string{"urn:li:tag:NewTag"}, nil)
	require.NoError(t, err)

	body := string(posted)
	assert.Contains(t, body, "urn:li:tag:Existing", "existing tag must be preserved")
	assert.Contains(t, body, "urn:li:tag:NewTag", "new tag must be added")
	assert.Equal(t, 1, gets, "exactly one read")
	assert.Equal(t, 1, posts, "exactly one write")
}

// TestDataHubClientWriter_ApplyTagChanges_NoClobber is the #721 regression: adding
// several tags in one call must read once and write all of them, not clobber down
// to a single tag the way back-to-back per-tag read-modify-writes did.
func TestDataHubClientWriter_ApplyTagChanges_NoClobber(t *testing.T) {
	var posted []byte
	var gets, posts int
	server := globalTagsServer(t, nil, &posted, &gets, &posts)
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	tags := []string{"urn:li:tag:a", "urn:li:tag:b", "urn:li:tag:c", "urn:li:tag:d"}
	err := writer.ApplyTagChanges(context.Background(), testURN, tags, nil)
	require.NoError(t, err)

	assert.Equal(t, 1, gets, "a batched apply reads once")
	assert.Equal(t, 1, posts, "a batched apply writes once")
	body := string(posted)
	for _, tag := range tags {
		assert.Contains(t, body, tag, "all tags must survive the single write")
	}
}

func TestDataHubClientWriter_ApplyTagChanges_Remove(t *testing.T) {
	var posted []byte
	var gets, posts int
	server := globalTagsServer(t, []string{"urn:li:tag:Keep", "urn:li:tag:Remove"}, &posted, &gets, &posts)
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.ApplyTagChanges(context.Background(), testURN, nil, []string{"urn:li:tag:Remove"})
	require.NoError(t, err)

	body := string(posted)
	assert.Contains(t, body, "urn:li:tag:Keep")
	assert.NotContains(t, body, "urn:li:tag:Remove")
}

// TestDataHubClientWriter_ApplyTagChanges_AddAndRemove verifies a mixed delta (as
// produced by a rollback containing both add_tag and remove_tag) is applied in a
// single read-modify-write.
func TestDataHubClientWriter_ApplyTagChanges_AddAndRemove(t *testing.T) {
	var posted []byte
	var gets, posts int
	server := globalTagsServer(t, []string{"urn:li:tag:Old"}, &posted, &gets, &posts)
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.ApplyTagChanges(context.Background(), testURN, []string{"urn:li:tag:New"}, []string{"urn:li:tag:Old"})
	require.NoError(t, err)

	body := string(posted)
	assert.Contains(t, body, "urn:li:tag:New")
	assert.NotContains(t, body, "urn:li:tag:Old")
	assert.Equal(t, 1, gets)
	assert.Equal(t, 1, posts)
}

// TestDataHubClientWriter_ApplyTagChanges_Dedup ensures adding a tag that already
// exists does not duplicate it, and a tag in both add and remove is removed.
func TestDataHubClientWriter_ApplyTagChanges_Dedup(t *testing.T) {
	var posted []byte
	var gets, posts int
	server := globalTagsServer(t, []string{"urn:li:tag:Dup"}, &posted, &gets, &posts)
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.ApplyTagChanges(context.Background(), testURN,
		[]string{"urn:li:tag:Dup", "urn:li:tag:Conflict"}, []string{"urn:li:tag:Conflict"})
	require.NoError(t, err)

	body := string(posted)
	assert.Equal(t, 1, strings.Count(body, "urn:li:tag:Dup"), "existing tag must not be duplicated")
	assert.NotContains(t, body, "urn:li:tag:Conflict", "a tag in both add and remove is removed")
}

func TestDataHubClientWriter_ApplyTagChanges_NoChanges(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("ApplyTagChanges with no add/remove must not hit DataHub")
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	require.NoError(t, writer.ApplyTagChanges(context.Background(), testURN, nil, nil))
}

func TestDataHubClientWriter_ApplyTagChanges_InvalidURN(t *testing.T) {
	writer := NewDataHubClientWriter(newTestClient(t, "http://unused"))
	err := writer.ApplyTagChanges(context.Background(), "not-a-urn", []string{"urn:li:tag:x"}, nil)
	assert.Error(t, err)
}

func TestDataHubClientWriter_ApplyTagChanges_ReadError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`boom`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.ApplyTagChanges(context.Background(), testURN, []string{"urn:li:tag:x"}, nil)
	assert.Error(t, err)
}

func TestDataHubClientWriter_ApplyTagChanges_WriteError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`boom`))
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.ApplyTagChanges(context.Background(), testURN, []string{"urn:li:tag:x"}, nil)
	assert.Error(t, err)
}

// TestDataHubClientWriter_ApplyTagChanges_GraphQLType verifies that for entity
// types whose globalTags aspect is GraphQL-only (e.g. glossaryTerm), tag changes
// go through the upstream per-tag GraphQL mutation rather than a REST aspect write.
func TestDataHubClientWriter_ApplyTagChanges_GraphQLType(t *testing.T) {
	var graphqlCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method, "GraphQL types must not issue REST aspect GETs")
		graphqlCalls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(graphQLResponse{Data: json.RawMessage(`{"addTag":true}`)})
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.ApplyTagChanges(context.Background(),
		"urn:li:glossaryTerm:revenue", []string{"urn:li:tag:Curated"}, nil)
	require.NoError(t, err)
	assert.Positive(t, graphqlCalls)
}

// TestDataHubClientWriter_ApplyTagChanges_GraphQLTypeRemove covers the remove path
// for GraphQL-only entity types.
func TestDataHubClientWriter_ApplyTagChanges_GraphQLTypeRemove(t *testing.T) {
	var ops []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		// The mutation name distinguishes add from remove.
		if strings.Contains(string(body), "removeTag") {
			ops = append(ops, "remove")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(graphQLResponse{Data: json.RawMessage(`{"removeTag":true}`)})
			return
		}
		ops = append(ops, "add")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(graphQLResponse{Data: json.RawMessage(`{"addTag":true}`)})
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.ApplyTagChanges(context.Background(),
		"urn:li:domain:sales", []string{"urn:li:tag:Add"}, []string{"urn:li:tag:Drop"})
	require.NoError(t, err)
	// Removes are applied before adds.
	assert.Equal(t, []string{"remove", "add"}, ops)
}

func TestDataHubClientWriter_ApplyTagChanges_GraphQLTypeError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`boom`))
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	addErr := writer.ApplyTagChanges(context.Background(), "urn:li:domain:sales", []string{"urn:li:tag:x"}, nil)
	assert.Error(t, addErr)
	removeErr := writer.ApplyTagChanges(context.Background(), "urn:li:domain:sales", nil, []string{"urn:li:tag:x"})
	assert.Error(t, removeErr)
}

// TestDataHubClientWriter_ApplyTagChanges_PreservesAssociationFields verifies that
// a read-modify-write preserves fields beyond the tag URN (e.g. propagation
// context/attribution) on existing associations, rather than stripping them.
func TestDataHubClientWriter_ApplyTagChanges_PreservesAssociationFields(t *testing.T) {
	var posted []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			// An existing association carrying a propagation context field.
			_, _ = w.Write([]byte(`{"value":{"tags":[{"tag":"urn:li:tag:Propagated","context":"lineage"}]}}`))
			return
		}
		posted, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.ApplyTagChanges(context.Background(), testURN, []string{"urn:li:tag:New"}, nil)
	require.NoError(t, err)

	// The aspect is embedded as an escaped JSON string in the v2 ingest body, so
	// match the unescaped substrings rather than exact quoting.
	body := string(posted)
	assert.Contains(t, body, "lineage", "existing association context must be preserved")
	assert.Contains(t, body, "urn:li:tag:Propagated")
	assert.Contains(t, body, "urn:li:tag:New")
}

// TestDataHubClientWriter_ApplyTagChanges_PreservesUnparseableAssociation verifies
// that an existing association whose URN cannot be extracted is preserved on the
// merged write rather than silently dropped.
func TestDataHubClientWriter_ApplyTagChanges_PreservesUnparseableAssociation(t *testing.T) {
	var posted []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			// One well-formed association and one without a parseable tag URN.
			_, _ = w.Write([]byte(`{"value":{"tags":[{"tag":"urn:li:tag:Keep"},{"weird":true}]}}`))
			return
		}
		posted, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.ApplyTagChanges(context.Background(), testURN, []string{"urn:li:tag:New"}, nil)
	require.NoError(t, err)

	body := string(posted)
	assert.Contains(t, body, "urn:li:tag:Keep")
	assert.Contains(t, body, "weird", "an association without a parseable URN must be preserved, not dropped")
	assert.Contains(t, body, "urn:li:tag:New")
}

func TestDataHubClientWriter_ApplyTagChanges_UnsupportedType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("unsupported entity types must be rejected before any DataHub call")
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.ApplyTagChanges(context.Background(),
		"urn:li:mlModel:(urn:li:dataPlatform:science,model,PROD)", []string{"urn:li:tag:x"}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not support tag operations")
}

// TestDataHubClientWriter_ApplyTagChanges_GraphQLDedup verifies that on a GraphQL
// entity type, a tag in both add and remove ends up removed (matching the REST path
// and the documented contract): it is never re-added.
func TestDataHubClientWriter_ApplyTagChanges_GraphQLDedup(t *testing.T) {
	var addedTags []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s := string(body)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(s, "removeTag") {
			_ = json.NewEncoder(w).Encode(graphQLResponse{Data: json.RawMessage(`{"removeTag":true}`)})
			return
		}
		// Capture which tag was added.
		if strings.Contains(s, "urn:li:tag:Both") {
			addedTags = append(addedTags, "urn:li:tag:Both")
		}
		_ = json.NewEncoder(w).Encode(graphQLResponse{Data: json.RawMessage(`{"addTag":true}`)})
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.ApplyTagChanges(context.Background(),
		"urn:li:domain:sales", []string{"urn:li:tag:Both"}, []string{"urn:li:tag:Both"})
	require.NoError(t, err)
	assert.Empty(t, addedTags, "a tag in both add and remove must not be re-added")
}

func TestTagURNOf(t *testing.T) {
	assert.Equal(t, "urn:li:tag:a", tagURNOf([]byte(`{"tag":"urn:li:tag:a"}`)))
	assert.Empty(t, tagURNOf([]byte(`{"notag":1}`)), "missing tag field yields empty")
	assert.Empty(t, tagURNOf([]byte(`not json`)), "unparseable association yields empty")
}

func TestParseGlobalTags(t *testing.T) {
	aspect, err := parseGlobalTags([]byte(`{"value":{"tags":[{"tag":"urn:li:tag:a"}]}}`))
	require.NoError(t, err)
	require.Len(t, aspect.Tags, 1)
	assert.Equal(t, "urn:li:tag:a", tagURNOf(aspect.Tags[0]))
}

func TestParseGlobalTags_NullValue(t *testing.T) {
	aspect, err := parseGlobalTags([]byte(`{"value":null}`))
	require.NoError(t, err)
	assert.Empty(t, aspect.Tags)
}

func TestParseGlobalTags_InvalidJSON(t *testing.T) {
	_, err := parseGlobalTags([]byte(`not json`))
	assert.Error(t, err)
}

// glossaryTermsServer mirrors globalTagsServer for the glossaryTerms aspect: it
// serves the given existing terms on GET and captures the POSTed aspect body,
// counting reads and writes.
func glossaryTermsServer(t *testing.T, existing []string, posted *[]byte, gets, posts *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			*gets++
			if existing == nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			assocs := make([]string, 0, len(existing))
			for _, term := range existing {
				assocs = append(assocs, `{"urn":"`+term+`"}`)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"value":{"terms":[` + strings.Join(assocs, ",") + `]}}`))
		case http.MethodPost:
			*posts++
			*posted, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
		}
	}))
}

// TestDataHubClientWriter_ApplyGlossaryTermChanges_NoClobber is the #729 regression:
// adding several terms in one call must read once and write all of them, with the
// required auditStamp, not clobber down to a single term.
func TestDataHubClientWriter_ApplyGlossaryTermChanges_NoClobber(t *testing.T) {
	var posted []byte
	var gets, posts int
	server := glossaryTermsServer(t, nil, &posted, &gets, &posts)
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	terms := []string{"urn:li:glossaryTerm:a", "urn:li:glossaryTerm:b", "urn:li:glossaryTerm:c"}
	err := writer.ApplyGlossaryTermChanges(context.Background(), testURN, terms, nil)
	require.NoError(t, err)

	assert.Equal(t, 1, gets, "a batched apply reads once")
	assert.Equal(t, 1, posts, "a batched apply writes once")
	body := string(posted)
	for _, term := range terms {
		assert.Contains(t, body, term, "all terms must survive the single write")
	}
	assert.Contains(t, body, "auditStamp", "the required auditStamp must be written")
	assert.Contains(t, body, glossaryAuditActor)
}

func TestDataHubClientWriter_ApplyGlossaryTermChanges_AddMergesExisting(t *testing.T) {
	var posted []byte
	var gets, posts int
	server := glossaryTermsServer(t, []string{"urn:li:glossaryTerm:Existing"}, &posted, &gets, &posts)
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.ApplyGlossaryTermChanges(context.Background(), testURN, []string{"urn:li:glossaryTerm:New"}, nil)
	require.NoError(t, err)

	body := string(posted)
	assert.Contains(t, body, "urn:li:glossaryTerm:Existing", "existing term must be preserved")
	assert.Contains(t, body, "urn:li:glossaryTerm:New")
	assert.Equal(t, 1, gets)
	assert.Equal(t, 1, posts)
}

func TestDataHubClientWriter_ApplyGlossaryTermChanges_Remove(t *testing.T) {
	var posted []byte
	var gets, posts int
	server := glossaryTermsServer(t, []string{"urn:li:glossaryTerm:Keep", "urn:li:glossaryTerm:Drop"}, &posted, &gets, &posts)
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.ApplyGlossaryTermChanges(context.Background(), testURN, nil, []string{"urn:li:glossaryTerm:Drop"})
	require.NoError(t, err)

	body := string(posted)
	assert.Contains(t, body, "urn:li:glossaryTerm:Keep")
	assert.NotContains(t, body, "urn:li:glossaryTerm:Drop")
}

func TestDataHubClientWriter_ApplyGlossaryTermChanges_NoChanges(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("ApplyGlossaryTermChanges with no add/remove must not hit DataHub")
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	require.NoError(t, writer.ApplyGlossaryTermChanges(context.Background(), testURN, nil, nil))
}

func TestDataHubClientWriter_ApplyGlossaryTermChanges_UnsupportedType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("unsupported entity types must be rejected before any DataHub call")
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.ApplyGlossaryTermChanges(context.Background(),
		"urn:li:mlModel:(urn:li:dataPlatform:science,model,PROD)", []string{"urn:li:glossaryTerm:x"}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not support glossary term operations")
}

func TestDataHubClientWriter_ApplyGlossaryTermChanges_InvalidURN(t *testing.T) {
	writer := NewDataHubClientWriter(newTestClient(t, "http://unused"))
	err := writer.ApplyGlossaryTermChanges(context.Background(), "not-a-urn", []string{"urn:li:glossaryTerm:x"}, nil)
	assert.Error(t, err)
}

func TestDataHubClientWriter_ApplyGlossaryTermChanges_ReadError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`boom`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.ApplyGlossaryTermChanges(context.Background(), testURN, []string{"urn:li:glossaryTerm:x"}, nil)
	assert.Error(t, err)
}

func TestDataHubClientWriter_ApplyGlossaryTermChanges_WriteError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`boom`))
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.ApplyGlossaryTermChanges(context.Background(), testURN, []string{"urn:li:glossaryTerm:x"}, nil)
	assert.Error(t, err)
}

// TestDataHubClientWriter_ApplyGlossaryTermChanges_GraphQLType verifies that for
// entity types whose glossaryTerms aspect is GraphQL-only, term changes go through
// the upstream per-term GraphQL mutation rather than a REST aspect write, and a term
// in both add and remove is left removed.
func TestDataHubClientWriter_ApplyGlossaryTermChanges_GraphQLType(t *testing.T) {
	var addedTerms []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method, "GraphQL types must not issue REST aspect GETs")
		body, _ := io.ReadAll(r.Body)
		s := string(body)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(s, "removeTerm") {
			_ = json.NewEncoder(w).Encode(graphQLResponse{Data: json.RawMessage(`{"removeTerm":true}`)})
			return
		}
		if strings.Contains(s, "urn:li:glossaryTerm:Both") {
			addedTerms = append(addedTerms, "urn:li:glossaryTerm:Both")
		}
		_ = json.NewEncoder(w).Encode(graphQLResponse{Data: json.RawMessage(`{"addTerm":true}`)})
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.ApplyGlossaryTermChanges(context.Background(),
		"urn:li:domain:sales", []string{"urn:li:glossaryTerm:Both"}, []string{"urn:li:glossaryTerm:Both"})
	require.NoError(t, err)
	assert.Empty(t, addedTerms, "a term in both add and remove must not be re-added")
}

func TestDataHubClientWriter_ApplyGlossaryTermChanges_GraphQLTypeError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`boom`))
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	addErr := writer.ApplyGlossaryTermChanges(context.Background(), "urn:li:domain:sales", []string{"urn:li:glossaryTerm:x"}, nil)
	assert.Error(t, addErr)
	removeErr := writer.ApplyGlossaryTermChanges(context.Background(), "urn:li:domain:sales", nil, []string{"urn:li:glossaryTerm:x"})
	assert.Error(t, removeErr)
}

func TestGlossaryTermURNOf(t *testing.T) {
	assert.Equal(t, "urn:li:glossaryTerm:a", glossaryTermURNOf([]byte(`{"urn":"urn:li:glossaryTerm:a"}`)))
	assert.Empty(t, glossaryTermURNOf([]byte(`{"nourn":1}`)))
	assert.Empty(t, glossaryTermURNOf([]byte(`not json`)))
}

func TestParseGlossaryTerms(t *testing.T) {
	aspect, err := parseGlossaryTerms([]byte(`{"value":{"terms":[{"urn":"urn:li:glossaryTerm:a"}]}}`))
	require.NoError(t, err)
	require.Len(t, aspect.Terms, 1)
	assert.Equal(t, "urn:li:glossaryTerm:a", glossaryTermURNOf(aspect.Terms[0]))
}

func TestParseGlossaryTerms_NullValue(t *testing.T) {
	aspect, err := parseGlossaryTerms([]byte(`{"value":null}`))
	require.NoError(t, err)
	assert.Empty(t, aspect.Terms)
}

func TestParseGlossaryTerms_InvalidJSON(t *testing.T) {
	_, err := parseGlossaryTerms([]byte(`not json`))
	assert.Error(t, err)
}

func TestDataHubClientWriter_AddDocumentationLink(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.WriteHeader(http.StatusNotFound)
		case http.MethodPost:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.AddDocumentationLink(context.Background(), testURN, "https://docs.example.com", "API Docs")

	require.NoError(t, err)
}

func TestDataHubClientWriter_AddDocumentationLink_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`server error`))
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.AddDocumentationLink(context.Background(), testURN, "https://docs.example.com", "Docs")

	assert.Error(t, err)
}

func TestDataHubClientWriter_UpdateColumnDescription(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			// Return empty editableSchemaMetadata (aspect not found)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.UpdateColumnDescription(context.Background(), testURN, "email", "Email address")

	require.NoError(t, err)
}

func TestDataHubClientWriter_UpdateColumnDescription_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`server error`))
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.UpdateColumnDescription(context.Background(), testURN, "email", "desc")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "updating column description")
}

func TestDataHubClientWriter_UpdateColumnDescriptionBatch_MultipleColumns(t *testing.T) {
	var postedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			// Return existing schema with one field already present.
			resp := struct {
				Value json.RawMessage `json:"value"`
			}{
				Value: json.RawMessage(`{"editableSchemaFieldInfo":[{"fieldPath":"existing","description":"old desc"}]}`),
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		// Capture the POST body.
		postedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.UpdateColumnDescriptionBatch(context.Background(), testURN, map[string]string{
		"email": "Email address",
		"name":  "Full name",
	})
	require.NoError(t, err)

	// Verify the POST body contains all three fields (existing + 2 new).
	require.NotEmpty(t, postedBody)
	body := string(postedBody)
	assert.Contains(t, body, "existing")
	assert.Contains(t, body, "email")
	assert.Contains(t, body, "name")
	assert.Contains(t, body, "Email address")
	assert.Contains(t, body, "Full name")
}

func TestDataHubClientWriter_UpdateColumnDescriptionBatch_SingleColumn(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.UpdateColumnDescriptionBatch(context.Background(), testURN, map[string]string{
		"email": "Updated email description",
	})
	require.NoError(t, err)
	// Single column delegates to UpdateColumnDescription (upstream).
}

func TestDataHubClientWriter_UpdateColumnDescriptionBatch_EmptyMap(t *testing.T) {
	writer := NewDataHubClientWriter(newTestClient(t, "http://unused"))
	err := writer.UpdateColumnDescriptionBatch(context.Background(), testURN, map[string]string{})
	require.NoError(t, err)
}

func TestDataHubClientWriter_UpdateColumnDescriptionBatch_InvalidURN(t *testing.T) {
	writer := NewDataHubClientWriter(newTestClient(t, "http://unused"))
	err := writer.UpdateColumnDescriptionBatch(context.Background(), "not-a-urn", map[string]string{
		"a": "1",
		"b": "2",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid URN")
}

func TestDataHubClientWriter_UpdateColumnDescriptionBatch_ReadError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("server error"))
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.UpdateColumnDescriptionBatch(context.Background(), testURN, map[string]string{
		"a": "1",
		"b": "2",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "read schema")
}

func TestDataHubClientWriter_UpdateColumnDescriptionBatch_WriteError(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if r.Method == http.MethodGet {
			// Successful read.
			resp := struct {
				Value json.RawMessage `json:"value"`
			}{
				Value: json.RawMessage(`{"editableSchemaFieldInfo":[]}`),
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		// POST fails.
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("write failed"))
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.UpdateColumnDescriptionBatch(context.Background(), testURN, map[string]string{
		"a": "1",
		"b": "2",
	})
	assert.Error(t, err)
}

func TestDataHubClientWriter_UpdateColumnDescriptionBatch_NoExistingAspect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.UpdateColumnDescriptionBatch(context.Background(), testURN, map[string]string{
		"col_a": "Description A",
		"col_b": "Description B",
	})
	require.NoError(t, err)
}

func TestDataHubClientWriter_UpdateColumnDescriptionBatch_NullAspectValue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			resp := struct {
				Value json.RawMessage `json:"value"`
			}{
				Value: json.RawMessage(`null`),
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.UpdateColumnDescriptionBatch(context.Background(), testURN, map[string]string{
		"col_a": "Description A",
		"col_b": "Description B",
	})
	require.NoError(t, err)
}

func TestParseEditableSchema(t *testing.T) {
	body := `{"value":{"editableSchemaFieldInfo":[{"fieldPath":"email","description":"Email"}]}}`
	schema, err := parseEditableSchema([]byte(body))
	require.NoError(t, err)
	require.Len(t, schema.EditableSchemaFieldInfo, 1)
	assert.Equal(t, "email", schema.EditableSchemaFieldInfo[0].FieldPath)
	assert.Equal(t, "Email", schema.EditableSchemaFieldInfo[0].Description)
}

func TestParseEditableSchema_NullValue(t *testing.T) {
	body := `{"value":null}`
	schema, err := parseEditableSchema([]byte(body))
	require.NoError(t, err)
	assert.Empty(t, schema.EditableSchemaFieldInfo)
}

func TestParseEditableSchema_EmptyValue(t *testing.T) {
	body := `{"value":""}`
	_, err := parseEditableSchema([]byte(body))
	// Empty string is not valid JSON for the schema, should error.
	assert.Error(t, err)
}

func TestParseEditableSchema_InvalidJSON(t *testing.T) {
	_, err := parseEditableSchema([]byte(`not json`))
	assert.Error(t, err)
}

func TestAspectGetURL_V1(t *testing.T) {
	cfg := dhclient.DefaultConfig()
	cfg.URL = "https://datahub.example.com/api/graphql"
	cfg.Token = "test"
	c, err := dhclient.New(cfg)
	require.NoError(t, err)
	defer func() { _ = c.Close() }()
	writer := NewDataHubClientWriter(c)

	got := writer.aspectGetURL("dataset", testURN, "editableSchemaMetadata")
	assert.Contains(t, got, "/aspects/")
	assert.Contains(t, got, "editableSchemaMetadata")
}

func TestAspectGetURL_V3(t *testing.T) {
	cfg := dhclient.DefaultConfig()
	cfg.URL = "https://datahub.example.com/api/graphql"
	cfg.Token = "test"
	cfg.APIVersion = dhclient.APIVersionV3
	c, err := dhclient.New(cfg)
	require.NoError(t, err)
	defer func() { _ = c.Close() }()
	writer := NewDataHubClientWriter(c)

	got := writer.aspectGetURL("dataset", testURN, "editableSchemaMetadata")
	assert.Contains(t, got, "/openapi/v3/entity/dataset/")
	assert.Contains(t, got, "editableSchemaMetadata")
}

func TestTruncateBody(t *testing.T) {
	short := []byte("short")
	assert.Equal(t, "short", truncateBody(short))

	long := []byte(strings.Repeat("x", 300))
	result := truncateBody(long)
	assert.Len(t, result, 203) // 200 + "..."
	assert.True(t, strings.HasSuffix(result, "..."))
}

func TestDataHubClientWriter_CreateCuratedQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := graphQLResponse{
			Data: json.RawMessage(`{
				"createQuery": {
					"urn": "urn:li:query:abc123",
					"properties": {
						"name": "Daily revenue",
						"description": "Revenue by day",
						"source": "MANUAL",
						"statement": {"value": "SELECT date, SUM(amount) FROM sales GROUP BY date", "language": "SQL"}
					},
					"subjects": [{"dataset": {"urn": "` + testURN + `"}}]
				}
			}`),
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	urn, err := writer.CreateCuratedQuery(
		context.Background(),
		testURN,
		"Daily revenue",
		"SELECT date, SUM(amount) FROM sales GROUP BY date",
		"Revenue by day",
	)

	require.NoError(t, err)
	assert.Equal(t, "urn:li:query:abc123", urn)
}

func TestDataHubClientWriter_CreateCuratedQuery_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := graphQLResponse{
			Errors: []any{map[string]any{"message": "permission denied"}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	_, err := writer.CreateCuratedQuery(
		context.Background(),
		testURN,
		"Query",
		"SELECT 1",
		"desc",
	)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "creating curated query")
}

func TestDataHubClientWriter_UpsertStructuredProperties(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := graphQLResponse{
			Data: json.RawMessage(`{"upsertStructuredProperties": {"properties": [{"structuredProperty": {"urn": "urn:li:structuredProperty:io.acryl.privacy.retentionTime"}}]}}`),
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.UpsertStructuredProperties(
		context.Background(),
		testURN,
		"urn:li:structuredProperty:io.acryl.privacy.retentionTime",
		[]any{float64(90)},
	)

	require.NoError(t, err)
}

func TestDataHubClientWriter_UpsertStructuredProperties_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := graphQLResponse{
			Errors: []any{map[string]any{"message": "property not found"}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.UpsertStructuredProperties(context.Background(), testURN, "urn:li:structuredProperty:x", []any{"v"})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "upserting structured property")
}

func TestDataHubClientWriter_RemoveStructuredProperty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := graphQLResponse{
			Data: json.RawMessage(`{"removeStructuredProperties": {"properties": []}}`),
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.RemoveStructuredProperty(
		context.Background(),
		testURN,
		"urn:li:structuredProperty:io.acryl.privacy.retentionTime",
	)

	require.NoError(t, err)
}

func TestDataHubClientWriter_RemoveStructuredProperty_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := graphQLResponse{
			Errors: []any{map[string]any{"message": "error"}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.RemoveStructuredProperty(context.Background(), testURN, "urn:li:structuredProperty:x")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "removing structured property")
}

func TestDataHubClientWriter_RaiseIncident(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := graphQLResponse{
			Data: json.RawMessage(`{"raiseIncident": "urn:li:incident:new123"}`),
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	urn, err := writer.RaiseIncident(
		context.Background(),
		testURN,
		"Pipeline failure",
		"The ETL pipeline crashed",
	)

	require.NoError(t, err)
	assert.Equal(t, "urn:li:incident:new123", urn)
}

func TestDataHubClientWriter_GetIncidents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(graphQLResponse{
			Data: json.RawMessage(`{"entity":{"incidents":{"total":1,"incidents":[` +
				`{"urn":"urn:li:incident:abc","type":"OPERATIONAL","title":"Data quality issue",` +
				`"description":"nulls","status":{"state":"ACTIVE"}}` +
				`]}}}`),
		})
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	incidents, err := writer.GetIncidents(context.Background(), testURN)
	require.NoError(t, err)
	require.Len(t, incidents, 1)
	assert.Equal(t, "urn:li:incident:abc", incidents[0].URN)
	assert.Equal(t, "Data quality issue", incidents[0].Title)
	assert.Equal(t, "ACTIVE", incidents[0].State)
}

func TestDataHubClientWriter_RaiseIncident_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := graphQLResponse{
			Errors: []any{map[string]any{"message": "unauthorized"}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	_, err := writer.RaiseIncident(context.Background(), testURN, "Title", "Desc")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "raising incident")
}

func TestDataHubClientWriter_ResolveIncident(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := graphQLResponse{
			Data: json.RawMessage(`{"updateIncidentStatus": true}`),
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.ResolveIncident(
		context.Background(),
		"urn:li:incident:abc123",
		"Fixed the issue",
	)

	require.NoError(t, err)
}

func TestDataHubClientWriter_ResolveIncident_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := graphQLResponse{
			Errors: []any{map[string]any{"message": "not found"}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.ResolveIncident(context.Background(), "urn:li:incident:abc", "msg")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "resolving incident")
}

func TestDataHubClientWriter_UpsertContextDocument(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		requestCount++

		// Read body to determine which GraphQL operation this is
		body, _ := io.ReadAll(r.Body)
		bodyStr := string(body)

		if strings.Contains(bodyStr, "createDocument") {
			// CreateDocument response
			resp := graphQLResponse{
				Data: json.RawMessage(`{"createDocument": "urn:li:document:new-doc-id"}`),
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		// GetDocument response (post-create fetch) — uses "document" not "entity"
		resp := graphQLResponse{
			Data: json.RawMessage(`{
				"document": {
					"urn": "urn:li:document:new-doc-id",
					"type": "DOCUMENT",
					"subType": "analysis",
					"info": {
						"title": "Test Doc",
						"contents": {"text": "content here"},
						"created": {"time": 1700000000000},
						"lastModified": {"time": 1700000000000}
					},
					"ownership": {
						"owners": [{
							"owner": {"urn": "urn:li:corpuser:admin", "type": "CORP_USER"},
							"type": "DATAOWNER"
						}]
					}
				}
			}`),
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	doc, err := writer.UpsertContextDocument(context.Background(), testURN, types.ContextDocumentInput{
		Title:   "Test Doc",
		Content: "content here",
	})

	require.NoError(t, err)
	require.NotNil(t, doc)
	assert.Equal(t, "new-doc-id", doc.ID)
	assert.Equal(t, "Test Doc", doc.Title)
}

func TestDataHubClientWriter_UpsertContextDocument_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := graphQLResponse{
			Errors: []any{map[string]any{"message": "creation failed"}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	_, err := writer.UpsertContextDocument(context.Background(), testURN, types.ContextDocumentInput{
		Title:   "Test Doc",
		Content: "content here",
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "upserting context document")
}

func TestDataHubClientWriter_DeleteContextDocument(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := graphQLResponse{
			Data: json.RawMessage(`{"deleteDocument": true}`),
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.DeleteContextDocument(context.Background(), "doc-123")

	assert.NoError(t, err)
}

func TestDataHubClientWriter_DeleteContextDocument_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := graphQLResponse{
			Errors: []any{map[string]any{"message": "not found"}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.DeleteContextDocument(context.Background(), "doc-123")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "deleting context document")
}

func TestDataHubClientWriter_InterfaceCompliance(t *testing.T) {
	// Compile-time check is in the source file; runtime verification here.
	var w DataHubWriter = &DataHubClientWriter{}
	assert.NotNil(t, w)
}
