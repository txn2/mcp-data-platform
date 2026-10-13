package resource

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/indexpolicy"
)

// indexUpload builds an upload of a CSV into folder with the given index
// field, or none when it is empty.
func indexUpload(t *testing.T, folder, index string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fields := map[string]string{
		"scope": "global", "path": folder, "display_name": "Daily archive", "description": "One day of it.",
	}
	if index != "" {
		fields["index"] = index
	}
	for k, v := range fields {
		require.NoError(t, w.WriteField(k, v))
	}
	part, err := w.CreateFormFile("file", "2019-01-01.csv")
	require.NoError(t, err)
	_, err = part.Write([]byte("station,reach\nKXYZ,10\n"))
	require.NoError(t, err)
	require.NoError(t, w.Close())
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/resources", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req
}

func indexedHandler(t *testing.T, rules indexpolicy.Config) (*Handler, *mockStore) {
	t.Helper()
	resolver, err := indexpolicy.NewResolver(rules)
	require.NoError(t, err)
	store := newMockStore()
	h := NewHandler(Deps{
		Store: store, S3Client: newMockS3(), S3Bucket: "test-bucket", URIScheme: "mcp", IndexPolicy: resolver,
	}, okExtractor, nil)
	return h, store
}

// TestUploadDecidesTheIndexPolicy is #2111 at the upload route: the uploader's
// choice, the operator's ceiling over it, and the rules' default when the
// uploader says nothing.
func TestUploadDecidesTheIndexPolicy(t *testing.T) {
	rules := indexpolicy.Config{Rules: []indexpolicy.Rule{{Path: "pipelines/**", Index: "none", Max: "metadata"}}}
	cases := []struct {
		name, folder, index string
		want                string
		explicit            bool
	}{
		{"the uploader takes it out of search", "reports", "none", "none", true},
		{"a table of values defaults to its metadata", "reports", "", "metadata", false},
		{"the uploader asks for its content", "reports", "full", "full", true},
		{"a folder rule decides for an uploader who says nothing", "pipelines/nltv", "", "none", false},
		{"the ceiling lowers what the uploader asked for", "pipelines/nltv", "full", "metadata", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, store := indexedHandler(t, rules)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, indexUpload(t, c.folder, c.index))
			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
			require.Len(t, store.resources, 1)
			for _, r := range store.resources {
				assert.Equal(t, c.want, r.IndexPolicy)
				assert.Equal(t, c.explicit, r.IndexPolicyExplicit)
			}
			var body map[string]any
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			assert.Equal(t, c.want, body["index_policy"], "the response reports the policy")
		})
	}
}

func TestUploadRefusesAnIndexItCannotRead(t *testing.T) {
	h, store := indexedHandler(t, indexpolicy.Config{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, indexUpload(t, "reports", "some"))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var body map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Contains(t, body["error"], `index must be one of "full", "metadata", "none"`)
	assert.Empty(t, store.resources)
}

// TestPatchChangesTheIndexPolicy: the policy is editable afterwards, held to
// the ceiling of the folder the file is in, and recorded as chosen.
func TestPatchChangesTheIndexPolicy(t *testing.T) {
	rules := indexpolicy.Config{Rules: []indexpolicy.Rule{{Path: "pipelines/**", Max: "metadata"}}}
	cases := []struct {
		name, folder, index string
		status              int
		want                string
	}{
		{"out of search", "reports", "none", http.StatusOK, "none"},
		{"capped by the folder", "pipelines/nltv", "full", http.StatusOK, "metadata"},
		{"a value it cannot read", "reports", "everything", http.StatusBadRequest, "full"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, store := indexedHandler(t, rules)
			seedResource(store, newMockS3(), "res-1", ScopeGlobal, "", "user-123")
			store.resources["res-1"].Path = c.folder
			store.resources["res-1"].IndexPolicy = "full"

			body, _ := json.Marshal(map[string]string{"index": c.index})
			req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch, "/api/v1/resources/res-1", bytes.NewReader(body))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			require.Equal(t, c.status, rec.Code, rec.Body.String())
			assert.Equal(t, c.want, store.resources["res-1"].IndexPolicy)
			assert.Equal(t, c.status == http.StatusOK, store.resources["res-1"].IndexPolicyExplicit)
		})
	}
}

func TestUpdateFieldsWritesNothingWithoutFields(t *testing.T) {
	store := newMockStore()
	err := UpdateFields(context.Background(), Deps{Store: store}, &Claims{Sub: "u"}, &Resource{ID: "gone"}, Update{})
	assert.NoError(t, err, "an empty update must not reach the store")
	bad := "lots"
	err = UpdateFields(context.Background(), Deps{Store: store}, &Claims{Sub: "u"}, &Resource{ID: "gone"}, Update{Index: &bad})
	assert.True(t, isInvalid(err), "err = %v", err)
}

func TestOriginOf(t *testing.T) {
	assert.Equal(t, indexpolicy.OriginScript, originOf(&Claims{Sub: "script:nltv-archive-import"}, indexpolicy.OriginAgent))
	assert.Equal(t, indexpolicy.OriginWebhook, originOf(&Claims{Sub: "system:webhooks"}, ""))
	assert.Equal(t, indexpolicy.OriginCatalog, originOf(&Claims{Sub: "system:catalog"}, ""))
	assert.Equal(t, indexpolicy.OriginExport, originOf(&Claims{Sub: "u"}, indexpolicy.OriginExport))
	assert.Equal(t, indexpolicy.OriginUpload, originOf(nil, ""))
}

// TestAWritersOriginMeetsItsCeiling: a ceiling an operator scopes to agents
// holds an agent's edit and replacement, and leaves a person's upload alone.
func TestAWritersOriginMeetsItsCeiling(t *testing.T) {
	resolver, err := indexpolicy.NewResolver(indexpolicy.Config{Rules: []indexpolicy.Rule{
		{Origin: []string{string(indexpolicy.OriginAgent)}, Max: "metadata"},
	}})
	require.NoError(t, err)
	store := newMockStore()
	s3 := newMockS3()
	deps := Deps{Store: store, S3Client: s3, S3Bucket: "test-bucket", URIScheme: "mcp", IndexPolicy: resolver}
	seedResource(store, s3, "res-1", ScopeGlobal, "", "user-123")
	res := store.resources["res-1"]
	full := "full"

	require.NoError(t, UpdateFields(context.Background(), deps, &Claims{Sub: "u"}, res,
		Update{Index: &full, Origin: indexpolicy.OriginAgent}))
	assert.Equal(t, "metadata", store.resources["res-1"].IndexPolicy, "an agent is held to the ceiling")

	require.NoError(t, UpdateFields(context.Background(), deps, &Claims{Sub: "u"}, res, Update{Index: &full}))
	assert.Equal(t, "full", store.resources["res-1"].IndexPolicy, "a person's edit is not an agent's")

	versions := newFakeVersions(store)
	deps.Versions = versions
	_, _, err = ReviseContent(context.Background(), deps, res, &Claims{Sub: "u"}, RevisionUpload{
		Content: bytes.NewReader([]byte("a,b\n")), MIMEType: "text/csv", Index: "full", Origin: indexpolicy.OriginAgent,
	})
	require.NoError(t, err)
	assert.Equal(t, "metadata", store.resources["res-1"].IndexPolicy, "an agent's replacement is held to it too")
}
