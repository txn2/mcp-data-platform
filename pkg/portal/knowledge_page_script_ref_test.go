package portal

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

// citedScriptID is a script id in the form scripts.id takes.
const citedScriptID = "6f1c0a52-8d8e-4f7b-9a3e-2b8c1d0e4f55"

// citedScripts is a ScriptRefs lookup holding one script, citedScriptID,
// shown as "Orders sync".
func citedScripts(_ context.Context, id string) (label string, ok bool, err error) {
	if id != citedScriptID {
		return "", false, nil
	}
	return "Orders sync", true, nil
}

func scriptRefHandler(user *User, lookup func(context.Context, string) (string, bool, error), refs []knowledgepage.EntityRef) *Handler {
	deps := Deps{
		KnowledgePageStore: &mockKnowledgePageStore{page: livePage(), refs: refs},
		AdminRoles:         []string{"admin"},
		RateLimit:          RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
		ScriptRefs:         lookup,
	}
	return NewHandler(deps, testAuthMiddleware(user))
}

func resolveOne(t *testing.T, h *Handler, urn string) resolvedRef {
	t.Helper()
	w := doKP(h, "POST", "/api/v1/portal/knowledge-pages/refs/resolve", `{"urns":["`+urn+`"]}`)
	require.Equal(t, http.StatusOK, w.Code)
	var resp resolveResp
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Refs, 1)
	return resp.Refs[0]
}

// A cited script resolves for every reader of the page (#2027): its
// definition is everyone signed in's to read. A script that no longer exists
// is a broken reference, the way a deleted page is.
func TestResolveKnowledgePageRefs_ScriptResolvesForEveryReader(t *testing.T) {
	urn := "mcp:script:" + citedScriptID
	for _, user := range []*User{kpViewer, kpAdmin} {
		got := resolveOne(t, scriptRefHandler(user, citedScripts, nil), urn)
		assert.Equal(t, knowledgepage.RefTargetScript, got.Type)
		assert.True(t, got.Accessible)
		assert.True(t, got.Exists)
		assert.Equal(t, "Orders sync", got.Label)
	}

	missing := resolveOne(t, scriptRefHandler(kpViewer, citedScripts, nil), "mcp:script:0b7e2f0c-1111-4a4a-8b8b-000000000000")
	assert.False(t, missing.Exists, "a deleted script is a broken reference")

	unwired := resolveOne(t, scriptRefHandler(kpViewer, nil, nil), urn)
	assert.False(t, unwired.Accessible, "no lookup wired is unavailable")

	failing := resolveOne(t, scriptRefHandler(kpViewer, func(context.Context, string) (string, bool, error) {
		return "", false, errors.New("db down")
	}, nil), urn)
	assert.False(t, failing.Accessible, "a failed lookup withholds the citation")
	assert.True(t, failing.Exists, "and does not tell the reader the script was deleted")
}

// A page citing a script lists it for every reader, beside its other
// references.
func TestListKnowledgePageRefs_ScriptListedForEveryReader(t *testing.T) {
	refs := []knowledgepage.EntityRef{
		{TargetType: knowledgepage.RefTargetScript, ScriptID: citedScriptID, Source: knowledgepage.RefSourcePromoted},
		{TargetType: knowledgepage.RefTargetDataHub, EntityURN: "urn:li:dataset:x", Source: knowledgepage.RefSourcePromoted},
	}
	listed := func(user *User) map[string]string {
		h := scriptRefHandler(user, citedScripts, refs)
		w := doKP(h, "GET", "/api/v1/portal/knowledge-pages/kp1/refs", "")
		require.Equal(t, http.StatusOK, w.Code)
		var resp refsResp
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		out := map[string]string{}
		for _, r := range resp.Refs {
			out[r.URN] = r.Label
		}
		return out
	}

	owner := &User{UserID: "owner-1", Email: "owner@example.com", Roles: []string{"analyst"}}
	for _, u := range []*User{owner, kpViewer} {
		got := listed(u)
		assert.Equal(t, "Orders sync", got["mcp:script:"+citedScriptID])
		assert.Contains(t, got, "urn:li:dataset:x")
	}
}
