package knowledge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dhclient "github.com/txn2/mcp-datahub/pkg/client"

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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// GetEntity uses GraphQL — POST to /api/graphql
		w.Header().Set("Content-Type", "application/json")
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

func TestDataHubClientWriter_AddTag(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			// Return empty tags (not found)
			w.WriteHeader(http.StatusNotFound)
		case http.MethodPost:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.AddTag(context.Background(), testURN, "urn:li:tag:NewTag")

	require.NoError(t, err)
}

func TestDataHubClientWriter_AddTag_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`server error`))
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.AddTag(context.Background(), testURN, "urn:li:tag:NewTag")

	assert.Error(t, err)
}

func TestDataHubClientWriter_RemoveTag(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			resp := map[string]json.RawMessage{
				"value": json.RawMessage(`{"tags":[{"tag":"urn:li:tag:Keep"},{"tag":"urn:li:tag:Remove"}]}`),
			}
			_ = json.NewEncoder(w).Encode(resp)
		case http.MethodPost:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.RemoveTag(context.Background(), testURN, "urn:li:tag:Remove")

	require.NoError(t, err)
}

func TestDataHubClientWriter_AddGlossaryTerm(t *testing.T) {
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
	err := writer.AddGlossaryTerm(context.Background(), testURN, "urn:li:glossaryTerm:Revenue")

	require.NoError(t, err)
}

func TestDataHubClientWriter_AddGlossaryTerm_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`server error`))
	}))
	defer server.Close()

	writer := NewDataHubClientWriter(newTestClient(t, server.URL))
	err := writer.AddGlossaryTerm(context.Background(), testURN, "urn:li:glossaryTerm:Revenue")

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

func TestDataHubClientWriter_InterfaceCompliance(t *testing.T) {
	// Compile-time check is in the source file; runtime verification here.
	var w DataHubWriter = &DataHubClientWriter{}
	assert.NotNil(t, w)
}
