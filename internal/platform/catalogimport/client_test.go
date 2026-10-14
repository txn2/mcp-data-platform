package catalogimport

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gms answers DataHub's GraphQL route with what answer returns for the
// request's query, recording the requests.
func gms(t *testing.T, status int, answer func(query string) string) (*httptest.Server, *[]*http.Request) {
	t.Helper()
	var seen []*http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req graphQLRequest
		_ = json.Unmarshal(body, &req)
		seen = append(seen, r)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, answer(req.Query))
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func TestClientExecute(t *testing.T) {
	srv, seen := gms(t, http.StatusOK, func(string) string { return `{"data":{"n":7}}` })
	c, err := NewClient(srv.URL+"/", "tok")
	require.NoError(t, err)
	var out struct {
		N int `json:"n"`
	}
	require.NoError(t, c.Execute(context.Background(), "query q { n }", nil, &out))
	assert.Equal(t, 7, out.N)
	require.Len(t, *seen, 1)
	assert.Equal(t, graphQLPath, (*seen)[0].URL.Path)
	assert.Equal(t, "Bearer tok", (*seen)[0].Header.Get("Authorization"))
	require.NoError(t, c.Execute(context.Background(), "query q { n }", nil, nil))

	c, err = NewClient(srv.URL+graphQLPath, "")
	require.NoError(t, err)
	require.NoError(t, c.Execute(context.Background(), "query q { n }", nil, &out))
	assert.Equal(t, graphQLPath, (*seen)[2].URL.Path, "an address already naming the route is used as it is")
	assert.Empty(t, (*seen)[2].Header.Get("Authorization"))
}

func TestClientRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
		want   string
	}{
		"unauthorized":  {http.StatusUnauthorized, ``, "personal access token"},
		"server error":  {http.StatusBadGateway, `upstream gone`, "502 Bad Gateway: upstream gone"},
		"not graphql":   {http.StatusOK, `<html>`, "not GraphQL"},
		"graphql error": {http.StatusOK, `{"errors":[{"message":"a"},{"message":"b"}]}`, "refused: a; b"},
		"no data":       {http.StatusOK, `{"data":null}`, "no data"},
		"wrong shape":   {http.StatusOK, `{"data":{"n":"x"}}`, "decoding the answer"},
	} {
		t.Run(name, func(t *testing.T) {
			srv, _ := gms(t, tc.status, func(string) string { return tc.body })
			c, err := NewClient(srv.URL, "t")
			require.NoError(t, err)
			var out struct {
				N int `json:"n"`
			}
			err = c.Execute(context.Background(), "query q { n }", nil, &out)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
	for _, bad := range []string{"", "ftp://dh", "http://", "::"} {
		_, err := NewClient(bad, "t")
		assert.Error(t, err, bad)
		_, err = Open(bad, "t")
		assert.Error(t, err, bad)
	}
	c, err := NewClient("http://127.0.0.1:1", "t")
	require.NoError(t, err)
	assert.ErrorContains(t, c.Execute(context.Background(), "q", nil, nil), "request failed")
	assert.Equal(t, strings.Repeat("x", 200)+"...", preview([]byte(strings.Repeat("x", 300))))
}

func TestClientContextDocuments(t *testing.T) {
	srv, _ := gms(t, http.StatusOK, func(string) string {
		return `{"data":{"entity":{"relatedDocuments":{"documents":[
			{"urn":"urn:li:document:d1","subType":"runbook","info":{"title":"Loading","contents":{"text":"nightly"}}}]}}}}`
	})
	c, err := NewClient(srv.URL, "t")
	require.NoError(t, err)
	docs, err := c.GetContextDocuments(context.Background(), "urn:li:dataset:x")
	require.NoError(t, err)
	assert.Equal(t, []ContextDocument{{ID: "d1", Title: "Loading", Content: "nightly", Category: "runbook"}}, docs)

	src, err := Open(srv.URL, "t")
	require.NoError(t, err)
	assert.NotNil(t, src)

	for name, body := range map[string]string{
		"none":                     `{"data":{"entity":{}}}`,
		"a DataHub before context": `{"errors":[{"message":"Validation error: Field 'relatedDocuments' in type 'Dataset' is undefined"}]}`,
	} {
		srv, _ := gms(t, http.StatusOK, func(string) string { return body })
		c, err := NewClient(srv.URL, "t")
		require.NoError(t, err)
		docs, err := c.GetContextDocuments(context.Background(), "urn:li:dataset:x")
		require.NoError(t, err, name)
		assert.Empty(t, docs, name)
	}
	srv, _ = gms(t, http.StatusInternalServerError, func(string) string { return "down" })
	c, err = NewClient(srv.URL, "t")
	require.NoError(t, err)
	_, err = c.GetContextDocuments(context.Background(), "urn:li:dataset:x")
	assert.Error(t, err)
}
