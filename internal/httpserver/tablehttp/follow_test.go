package tablehttp

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The follow choice travels through the REST surface (#1536): the register
// body carries it, and every view carries the stored state back, including
// why the last follow did not move the table.

func TestRegisterRoute_CarriesTheFollowChoice(t *testing.T) {
	h := newHarness(t)

	// A body that says nothing gets a table that follows the file, which is
	// what a person replacing the file expects of it.
	w := h.do(http.MethodPost, "/api/v1/portal/assets/asset_1/tables",
		`{"connection":"scratch","table_name":"live"}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	var got registrationView
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.True(t, got.Follow)
	stored, err := h.store.Get(context.Background(), got.ID)
	require.NoError(t, err)
	assert.True(t, stored.Follow)

	// Pinning is said explicitly.
	w = h.do(http.MethodPost, "/api/v1/portal/assets/asset_1/tables",
		`{"connection":"scratch","table_name":"snapshot","follow":false}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.False(t, got.Follow)
}

func TestListRoute_ReportsWhyAFollowDidNotMoveTheTable(t *testing.T) {
	h := newHarness(t)
	w := h.do(http.MethodPost, "/api/v1/portal/assets/asset_1/tables", `{"connection":"scratch"}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var reg registrationView
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &reg))
	require.NoError(t, h.store.RecordFollowFailure(context.Background(), reg.ID, "coordinator down"))

	w = h.do(http.MethodGet, "/api/v1/portal/assets/asset_1/tables", "")
	require.Equal(t, http.StatusOK, w.Code)
	var list struct {
		Registrations []registrationView `json:"registrations"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &list))
	require.Len(t, list.Registrations, 1)
	assert.True(t, list.Registrations[0].Follow)
	assert.Equal(t, "coordinator down", list.Registrations[0].FollowError)

	w = h.do(http.MethodGet, "/api/v1/tables/"+reg.ID, "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var one scratchTableView
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &one))
	assert.True(t, one.Follow)
	assert.Equal(t, "coordinator down", one.FollowError)
}

// TestRegisterRoute_CarriesTheRepairChoice. Correcting the file is a standing
// choice, not one submission's (#1577): the register body sets it, the record
// keeps it, and every view carries it back so the panel can say which tables
// rewrite their file.
func TestRegisterRoute_CarriesTheRepairChoice(t *testing.T) {
	h := newHarness(t)

	w := h.do(http.MethodPost, "/api/v1/portal/assets/asset_1/tables",
		`{"connection":"scratch","table_name":"live","repair":true}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var got registrationView
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.True(t, got.Repair)

	stored, err := h.store.Get(context.Background(), got.ID)
	require.NoError(t, err)
	assert.True(t, stored.Repair)

	// A body that does not ask for it gets a registration that corrects
	// nothing, which is what registering somebody's file must default to.
	w = h.do(http.MethodPost, "/api/v1/portal/assets/asset_1/tables",
		`{"connection":"scratch","table_name":"snapshot"}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.False(t, got.Repair)

	w = h.do(http.MethodGet, "/api/v1/portal/assets/asset_1/tables", "")
	require.Equal(t, http.StatusOK, w.Code)
	var list struct {
		Registrations []registrationView `json:"registrations"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &list))
	require.Len(t, list.Registrations, 2)
	byTable := map[string]bool{}
	for _, reg := range list.Registrations {
		byTable[reg.QueryTable] = reg.Repair
	}
	assert.True(t, byTable["scratch.uploads.analyst_live"])
	assert.False(t, byTable["scratch.uploads.analyst_snapshot"])
}
