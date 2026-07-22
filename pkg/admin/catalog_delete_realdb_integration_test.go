//go:build integration

package admin

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	apicatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalogindex"
)

// specBody is a one-operation OpenAPI document the admin spec write
// path accepts, so each PUT enqueues exactly one index job through the
// real producer hook.
const specBody = `openapi: 3.0.0
info: {title: t, version: "1"}
paths:
  /a:
    get:
      operationId: a
      responses:
        "200":
          description: ok`

// requireNoResidue asserts the acceptance criteria of #998 for one
// unit: zero pending/running jobs for the key, zero open failures, and
// an api_catalog kind that reports no unresolved failures (the signal
// the portal's Degraded verdict keys on).
func requireNoResidue(t *testing.T, jobs *indexjobs.PostgresStore, catalogID, specName string) {
	t.Helper()
	ctx := context.Background()
	sourceID := catalogindex.EncodeSourceID(catalogID, specName)

	rows, err := jobs.List(ctx, indexjobs.ListFilter{SourceKind: catalogindex.SourceKind, SourceID: sourceID})
	require.NoError(t, err)
	for _, j := range rows {
		require.NotEqual(t, indexjobs.StatusPending, j.Status, "pending job survived the delete: %+v", j)
		require.NotEqual(t, indexjobs.StatusRunning, j.Status, "running job survived the delete: %+v", j)
	}

	failures, err := jobs.ActiveFailures(ctx, catalogindex.SourceKind, 50)
	require.NoError(t, err)
	for _, f := range failures {
		require.NotEqual(t, sourceID, f.SourceID, "open failure survived the delete: %+v", f)
	}

	counts, err := jobs.Counts(ctx, catalogindex.SourceKind)
	require.NoError(t, err)
	require.Zero(t, counts.UnresolvedFailures, "api_catalog kind must report healthy after the delete")
}

// TestCatalogSpecDelete_ClearsIndexResidue_RealDB drives the real
// admin delete handlers against the real Postgres-backed indexjobs
// store (#998): deleting a spec (or its whole catalog) must leave zero
// pending jobs and zero open failures for the affected keys, with no
// operator dismiss.
func TestCatalogSpecDelete_ClearsIndexResidue_RealDB(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	catalogStore := apicatalog.NewPostgresStore(db)
	jobs := indexjobs.NewPostgresStore(db)
	h := NewHandler(Deps{
		APICatalogStore:   catalogStore,
		EmbedJobs:         catalogindex.NewAdminStore(jobs, db),
		ConfigStore:       &mockConfigStore{mode: "database"},
		DatabaseAvailable: true,
	}, nil)

	require.NoError(t, catalogStore.CreateCatalog(ctx, apicatalog.Catalog{
		ID: "cat1", Name: "cat1", DisplayName: "Catalog One",
	}))

	putSpec := func(catalogID, specName string) {
		t.Helper()
		res := doJSON(t, h, http.MethodPut, "/api/v1/admin/api-catalogs/"+catalogID+"/specs/"+specName, map[string]any{
			"source_kind": "inline",
			"content":     specBody,
		})
		require.Equal(t, http.StatusOK, res.Code, "upsert %s/%s: %s", catalogID, specName, res.Body.String())
	}

	t.Run("pending job is canceled by spec delete", func(t *testing.T) {
		putSpec("cat1", "alpha")

		pending, err := jobs.List(ctx, indexjobs.ListFilter{
			SourceKind: catalogindex.SourceKind,
			SourceID:   catalogindex.EncodeSourceID("cat1", "alpha"),
			Status:     indexjobs.StatusPending,
		})
		require.NoError(t, err)
		require.Len(t, pending, 1, "spec write must enqueue a pending job")

		res := doJSON(t, h, http.MethodDelete, "/api/v1/admin/api-catalogs/cat1/specs/alpha", nil)
		require.Equal(t, http.StatusOK, res.Code, res.Body.String())

		requireNoResidue(t, jobs, "cat1", "alpha")
	})

	t.Run("open failure is resolved by spec delete", func(t *testing.T) {
		putSpec("cat1", "beta")

		job, err := jobs.Claim(ctx, "it-worker")
		require.NoError(t, err)
		require.Equal(t, catalogindex.EncodeSourceID("cat1", "beta"), job.SourceID)
		require.NoError(t, jobs.Fail(ctx, job.ID, "it-worker", "catalogSource: get spec: boom"))

		failures, err := jobs.ActiveFailures(ctx, catalogindex.SourceKind, 50)
		require.NoError(t, err)
		require.Len(t, failures, 1, "the failed job must surface as an open failure before the delete")

		res := doJSON(t, h, http.MethodDelete, "/api/v1/admin/api-catalogs/cat1/specs/beta", nil)
		require.Equal(t, http.StatusOK, res.Code, res.Body.String())

		requireNoResidue(t, jobs, "cat1", "beta")
	})

	t.Run("catalog delete clears residue for every spec", func(t *testing.T) {
		require.NoError(t, catalogStore.CreateCatalog(ctx, apicatalog.Catalog{
			ID: "cat2", Name: "cat2", DisplayName: "Catalog Two",
		}))
		putSpec("cat2", "gamma")
		putSpec("cat2", "delta")

		// Leave gamma's job pending and fail delta's, covering both
		// residue shapes in one catalog delete.
		var deltaJob *indexjobs.Job
		for {
			job, err := jobs.Claim(ctx, "it-worker")
			if err != nil {
				break
			}
			if job.SourceID == catalogindex.EncodeSourceID("cat2", "delta") {
				deltaJob = job
			} else {
				require.NoError(t, jobs.Retry(ctx, job.ID, "it-worker", "put back"))
			}
		}
		require.NotNil(t, deltaJob, "delta job must be claimable")
		require.NoError(t, jobs.Fail(ctx, deltaJob.ID, "it-worker", "catalogSource: get spec: boom"))

		res := doJSON(t, h, http.MethodDelete, "/api/v1/admin/api-catalogs/cat2", nil)
		require.Equal(t, http.StatusOK, res.Code, res.Body.String())

		requireNoResidue(t, jobs, "cat2", "gamma")
		requireNoResidue(t, jobs, "cat2", "delta")
	})
}
