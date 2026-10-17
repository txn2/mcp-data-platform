package storagecap

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// volumeServer answers /status as SeaweedFS 3.88's volume server does.
func volumeServer(t *testing.T, all, used, free uint64) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status" {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprintf(w, `{"DiskStatuses":[{"dir":"/data","all":%d,"used":%d,"free":%d,"percent_free":68.7,"percent_used":31.2}],"Version":"30GB 3.88","Volumes":null}`, all, used, free)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// master answers /dir/status listing nodes, as the master does.
func master(t *testing.T, nodes ...string) *httptest.Server {
	t.Helper()
	parts := make([]string, 0, len(nodes))
	for _, n := range nodes {
		parts = append(parts, fmt.Sprintf(`{"Url":%q,"PublicUrl":%q,"Volumes":0,"Max":50}`, n, n))
	}
	body := `{"Topology":{"Max":50,"Free":50,"DataCenters":[{"Id":"DefaultDataCenter","Racks":[{"Id":"DefaultRack","DataNodes":[` +
		strings.Join(parts, ",") + `]}]}],"Layouts":null},"Version":"30GB 3.88"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/dir/status" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestReadSeaweedFSThroughTheMaster(t *testing.T) {
	ctx := context.Background()
	a := volumeServer(t, 1000, 300, 700)
	b := volumeServer(t, 2000, 500, 1500)
	m := master(t, strings.TrimPrefix(a.URL, "http://"), strings.TrimPrefix(b.URL, "http://"), strings.TrimPrefix(a.URL, "http://"))
	got, err := ReadSeaweedFS(ctx, http.DefaultClient, SeaweedFS{MasterURL: m.URL})
	require.NoError(t, err)
	assert.Equal(t, ClusterCapacity{Total: 3000, Used: 800, Free: 2200, Servers: 2, Unreached: []string{}}, got)
}

func TestReadSeaweedFSNamedVolumes(t *testing.T) {
	ctx := context.Background()
	a := volumeServer(t, 1000, 300, 700)
	got, err := ReadSeaweedFS(ctx, http.DefaultClient, SeaweedFS{MasterURL: "http://unused", VolumeURLs: []string{a.URL, "http://127.0.0.1:1"}})
	require.NoError(t, err)
	assert.Equal(t, int64(1000), got.Total)
	assert.Equal(t, []string{"http://127.0.0.1:1"}, got.Unreached, "a partial reading names what it missed")

	_, err = ReadSeaweedFS(ctx, http.DefaultClient, SeaweedFS{MasterURL: "http://unused", VolumeURLs: []string{"http://127.0.0.1:1"}})
	require.ErrorIs(t, err, errNoServers)
}

func TestReadSeaweedFSMasterFailures(t *testing.T) {
	ctx := context.Background()
	_, err := ReadSeaweedFS(ctx, http.DefaultClient, SeaweedFS{MasterURL: "http://127.0.0.1:1"})
	require.Error(t, err)

	empty := master(t)
	_, err = ReadSeaweedFS(ctx, http.DefaultClient, SeaweedFS{MasterURL: empty.URL})
	require.ErrorContains(t, err, "lists no volume server")

	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("{not json")) }))
	defer broken.Close()
	_, err = ReadSeaweedFS(ctx, http.DefaultClient, SeaweedFS{MasterURL: broken.URL})
	require.ErrorContains(t, err, "decoding")

	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer down.Close()
	_, err = ReadSeaweedFS(ctx, http.DefaultClient, SeaweedFS{MasterURL: down.URL})
	require.ErrorContains(t, err, "status 503")

	_, err = ReadSeaweedFS(ctx, http.DefaultClient, SeaweedFS{MasterURL: "http://bad host"})
	require.Error(t, err)

	// A master listing a node by address only, with no scheme, is read over http.
	v := volumeServer(t, 10, 1, 9)
	plain := master(t, strings.TrimPrefix(v.URL, "http://"))
	got, err := ReadSeaweedFS(ctx, http.DefaultClient, SeaweedFS{MasterURL: plain.URL})
	require.NoError(t, err)
	assert.Equal(t, 1, got.Servers)
}

func TestClamp(t *testing.T) {
	assert.Equal(t, int64(5), clamp(5))
	assert.Equal(t, int64(1<<62), clamp(1<<63))
}
