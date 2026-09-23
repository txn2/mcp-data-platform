package portal

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

// citedScriptID is a script id in the form scripts.id takes.
const citedScriptID = "6f1c0a52-8d8e-4f7b-9a3e-2b8c1d0e4f55"

// scriptRefsOwnedBy is a ScriptRefs lookup holding one script, citedScriptID,
// shown as "Orders sync" and owned by owner.
func scriptRefsOwnedBy(owner string) func(context.Context, string) (string, string, bool) {
	return func(_ context.Context, id string) (label, own string, ok bool) {
		if id != citedScriptID {
			return "", "", false
		}
		return "Orders sync", owner, true
	}
}

func scriptRefHandler(user *User, lookup func(context.Context, string) (string, string, bool), refs []knowledgepage.EntityRef) *Handler {
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

// A cited script resolves for the readers who may open it, its owner and an
// administrator, and for no one else (#1855). A script the reader may not open
// and one that does not exist answer identically, so the resolve endpoint does
// not reveal which scripts exist.
func TestResolveKnowledgePageRefs_Script(t *testing.T) {
	urn := "mcp:script:" + citedScriptID
	cases := []struct {
		name       string
		user       *User
		lookup     func(context.Context, string) (string, string, bool)
		urn        string
		accessible bool
	}{
		{"the owner opens it", kpViewer, scriptRefsOwnedBy(kpViewer.Email), urn, true},
		{"an administrator opens it", kpAdmin, scriptRefsOwnedBy("someone@example.com"), urn, true},
		{"an administrator opens an ownerless script", kpAdmin, scriptRefsOwnedBy(""), urn, true},
		{"another reader does not", kpViewer, scriptRefsOwnedBy("someone@example.com"), urn, false},
		{"an ownerless script is nobody's", kpViewer, scriptRefsOwnedBy(""), urn, false},
		{
			"a missing script is unavailable", kpViewer, scriptRefsOwnedBy(kpViewer.Email),
			"mcp:script:0b7e2f0c-1111-4a4a-8b8b-000000000000", false,
		},
		{"no lookup wired is unavailable", kpAdmin, nil, urn, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveOne(t, scriptRefHandler(tc.user, tc.lookup, nil), tc.urn)
			assert.Equal(t, knowledgepage.RefTargetScript, got.Type)
			assert.Equal(t, tc.accessible, got.Accessible)
			if tc.accessible {
				assert.Equal(t, "Orders sync", got.Label)
			} else {
				assert.NotContains(t, got.Label, "Orders sync", "an unavailable script's name is not revealed")
			}
		})
	}
}

// A page citing a script stays readable by everyone: its reference list
// carries the script for the owner and withholds it from any other reader,
// while the page's other references are listed for both.
func TestListKnowledgePageRefs_ScriptWithheldFromOtherReaders(t *testing.T) {
	refs := []knowledgepage.EntityRef{
		{TargetType: knowledgepage.RefTargetScript, ScriptID: citedScriptID, Source: knowledgepage.RefSourcePromoted},
		{TargetType: knowledgepage.RefTargetDataHub, EntityURN: "urn:li:dataset:x", Source: knowledgepage.RefSourcePromoted},
	}
	listed := func(user *User) map[string]string {
		h := scriptRefHandler(user, scriptRefsOwnedBy("owner@example.com"), refs)
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
	assert.Equal(t, "Orders sync", listed(owner)["mcp:script:"+citedScriptID])
	assert.Contains(t, listed(owner), "urn:li:dataset:x")

	other := listed(kpViewer)
	assert.NotContains(t, other, "mcp:script:"+citedScriptID)
	assert.Contains(t, other, "urn:li:dataset:x", "the rest of the page's references still list")
}
