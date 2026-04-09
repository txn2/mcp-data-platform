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
