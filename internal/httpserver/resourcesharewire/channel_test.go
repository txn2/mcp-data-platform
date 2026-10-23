package resourcesharewire

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/portal/resourceshare"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

type fakeStore struct {
	resourceshare.Shares
	inserted []portaldomain.Share
	revoked  []string
}

func (f *fakeStore) Insert(_ context.Context, s *portaldomain.Share) error {
	f.inserted = append(f.inserted, *s)
	return nil
}

func (f *fakeStore) Revoke(_ context.Context, _, id string) error {
	f.revoked = append(f.revoked, id)
	return nil
}

func (*fakeStore) Permission(context.Context, string, resourceshare.Recipient) (string, error) {
	return "", nil
}

type fakeFiles map[string]*resource.Resource

func (f fakeFiles) Get(_ context.Context, id string) (*resource.Resource, error) {
	if r, ok := f[id]; ok {
		return r, nil
	}
	return nil, errors.New("not found")
}

type fakePoster struct {
	sent bool
	args []map[string]any
}

func (p *fakePoster) CallAs(w http.ResponseWriter, _ *http.Request, _ *access.User, args map[string]any) bool {
	p.args = append(p.args, args)
	if !p.sent {
		w.WriteHeader(http.StatusBadRequest)
		return false
	}
	w.WriteHeader(http.StatusOK)
	return true
}

func channelFixture(sharing bool) (*fakeStore, *fakePoster, *http.ServeMux) {
	store := &fakeStore{}
	post := &fakePoster{sent: true}
	d := &resourceshare.Deps{
		Shares: store,
		Resources: fakeFiles{"r1": {
			ID: "r1", DisplayName: "extract.csv", Scope: resource.ScopeUser, ScopeID: "owner", UploaderSub: "owner",
		}},
		Caller: func([]string) resourceshare.Caller { return resourceshare.Caller{Persona: "staff", Sharing: sharing} },
		Claims: func(r *http.Request) (*resource.Claims, error) {
			if r.Header.Get("X-As") == "" {
				return nil, errors.New("signed out")
			}
			return &resource.Claims{Sub: r.Header.Get("X-As"), Email: r.Header.Get("X-As") + "@example.com"}, nil
		},
	}
	s := &Shares{svc: resourceshare.New(*d), caller: d.Caller, deps: d, channels: post, portalURL: "https://platform.example.com/"}
	mux := http.NewServeMux()
	s.Register(mux)
	return store, post, mux
}

func postFile(mux *http.ServeMux, as, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/resources/r1/channel-shares", strings.NewReader(body))
	if as != "" {
		req.Header.Set("X-As", as)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

// A file posted to a channel carries a link to its page and is shared with
// every signed-in internal person, recorded as posted there; the share is
// withdrawn when the post does not go out (#2258).
func TestPostAFileToAChannel(t *testing.T) {
	store, post, mux := channelFixture(true)
	w := postFile(mux, "owner", `{"channel":"slack","target":"C1","message":"This week"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Len(t, store.inserted, 1)
	sh := store.inserted[0]
	assert.Equal(t, "r1", sh.ResourceID)
	assert.Equal(t, portaldomain.OriginChannelPost, sh.Origin)
	assert.Equal(t, "C1 on slack", sh.PostedTo)
	assert.Equal(t, portaldomain.PermissionViewer, sh.Permission)
	assert.Empty(t, sh.SharedWithEmail)
	assert.Equal(t, map[string]any{
		"action": "send", "channel": "slack", "target": "C1", "title": "extract.csv",
		"body": "This week", "link": "https://platform.example.com/portal/resources/r1",
	}, post.args[0])

	w = postFile(mux, "owner", `{"channel":"slack","access_mode":"existing"}`)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Len(t, store.inserted, 1, "existing posts the page and shares nothing")

	post.sent = false
	postFile(mux, "owner", `{"channel":"slack"}`)
	require.Len(t, store.inserted, 2)
	assert.Equal(t, []string{store.inserted[1].ID}, store.revoked, "a post that did not go out takes its share back")
}

func TestPostAFileRefusals(t *testing.T) {
	store, _, mux := channelFixture(true)
	assert.Equal(t, http.StatusUnauthorized, postFile(mux, "", `{"channel":"slack"}`).Code)
	assert.Equal(t, http.StatusNotFound, postFile(mux, "stranger", `{"channel":"slack"}`).Code)
	assert.Equal(t, http.StatusBadRequest, postFile(mux, "owner", `{}`).Code)
	assert.Equal(t, http.StatusBadRequest, postFile(mux, "owner", `{"channel":"slack","access_mode":"public"}`).Code)
	assert.Empty(t, store.inserted)

	store, _, mux = channelFixture(false)
	assert.Equal(t, http.StatusForbidden, postFile(mux, "owner", `{"channel":"slack"}`).Code, "a persona that withholds sharing")
	assert.Empty(t, store.inserted)
}
