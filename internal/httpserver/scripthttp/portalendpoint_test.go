package scripthttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epcmd"
	"github.com/txn2/mcp-data-platform/internal/httpserver/scripthttp/scriptlist"
)

// listedBindings is a binding store holding a fixed set, answering a listing
// with the bindings of the scripts it names.
type listedBindings struct {
	epbinding.Store
	bindings []epbinding.Binding
	asked    epbinding.Filter
	err      error
}

func (l *listedBindings) List(_ context.Context, f epbinding.Filter) ([]epbinding.Binding, int, error) {
	l.asked = f
	if l.err != nil {
		return nil, 0, l.err
	}
	out := []epbinding.Binding{}
	for _, b := range l.bindings {
		for _, id := range f.ScriptIDs {
			if b.ScriptID == id {
				out = append(out, b)
			}
		}
	}
	return out, len(out), nil
}

func (*listedBindings) Stamp(context.Context, string) (epbinding.Stamp, error) {
	return epbinding.Stamp{Binding: time.Time{}}, nil
}

// #2259: each listed script bound to an endpoint carries it, a script bound to
// none carries none, and endpoint= narrows the listing in the store.
func TestPortalListScripts_CarriesEachScriptsEndpoint(t *testing.T) {
	store := portalStore()
	bindings := &listedBindings{bindings: []epbinding.Binding{{
		ScriptID: "script_2", Name: "carols-report", Path: "/{id}", AuthMode: epbinding.AuthBotCheck,
		BotCheck: "default", Enabled: false,
	}}}
	deps := portalDeps(store, nil, nil, carol)
	deps.Endpoints = &epcmd.Handler{Store: bindings}

	rec := servePortal(t, deps, "/api/v1/portal/scripts")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out struct {
		Data []struct {
			Script   struct{ ID string }  `json:"script"`
			Endpoint *scriptlist.Endpoint `json:"endpoint"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	byID := map[string]*scriptlist.Endpoint{}
	for _, row := range out.Data {
		byID[row.Script.ID] = row.Endpoint
	}
	require.Contains(t, byID, "script_1")
	assert.Nil(t, byID["script_1"], "a script bound to no endpoint carries none")
	require.NotNil(t, byID["script_2"])
	assert.Equal(t, scriptlist.Endpoint{
		Name: "carols-report", Path: "/endpoints/carols-report/{id}", AuthMode: epbinding.AuthBotCheck, Enabled: false,
	}, *byID["script_2"])
	assert.Len(t, bindings.asked.ScriptIDs, 2, "one read for the page's scripts")
	assert.Equal(t, 2, bindings.asked.Limit, "the limit is the page, so no bound row is cut off")

	rec = servePortal(t, deps, "/api/v1/portal/scripts?endpoint=bound")
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, store.lastFilter.Endpoint)
	assert.True(t, *store.lastFilter.Endpoint)
	rec = servePortal(t, deps, "/api/v1/portal/scripts?endpoint=unbound")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.False(t, *store.lastFilter.Endpoint)

	rec = servePortal(t, deps, "/api/v1/portal/scripts?endpoint=yes")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), `unknown endpoint \"yes\"`)

	bindings.err = errors.New("down")
	rec = servePortal(t, deps, "/api/v1/portal/scripts")
	require.Equal(t, http.StatusOK, rec.Code, "a listing that cannot read the bindings is still a listing")
	assert.NotContains(t, rec.Body.String(), `"endpoint"`)
}
