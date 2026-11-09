package sitestore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/site"
)

var jobCols = []string{
	"id", "site_id", "kind", "status", "phase", "environment", "source", "done", "total", "pages", "bytes", "skipped_count", "redirects", "skipped",
	"release_number", "error", "created_by", "created_at", "updated_at",
}

func jobRow(id, status, skipped string) []driver.Value {
	return []driver.Value{id, "site_1", site.JobCrawl, status, "Copying pages", "default", "https://old.example.com/", 2, 9, 2, int64(0), 0, 1, []byte(skipped), 0, "", "a", rowTime, rowTime}
}

func jobRows(rows ...[]driver.Value) *sqlmock.Rows {
	out := sqlmock.NewRows(jobCols)
	for _, r := range rows {
		out.AddRow(r...)
	}
	return out
}

func TestNewJobID(t *testing.T) {
	assert.True(t, strings.HasPrefix(NewJobID(), "sjob_"))
	assert.NotEqual(t, NewJobID(), NewJobID())
}

func TestCreateJob(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)
	j := func() *site.Job {
		return &site.Job{ID: "sjob_1", SiteID: "site_1", Kind: site.JobPublish, Phase: "Reading files", Environment: "default", Source: "upload", CreatedBy: "a"}
	}

	mock.ExpectQuery(q("INSERT INTO site_jobs")).WithArgs("sjob_1", "site_1", site.JobPublish, "Reading files", "default", "upload", "a").
		WillReturnRows(sqlmock.NewRows([]string{"status", "created_at", "updated_at"}).AddRow(site.JobRunning, rowTime, rowTime))
	got := j()
	require.NoError(t, s.CreateJob(ctx, got))
	assert.Equal(t, site.JobRunning, got.Status, "the database says how it started")
	assert.Equal(t, rowTime, got.UpdatedAt)
	assert.Equal(t, []site.Skipped{}, got.Skipped, "none is [], never null")

	kept := j()
	kept.Skipped = []site.Skipped{{Address: "/x"}}
	mock.ExpectQuery(q("INSERT INTO site_jobs")).WillReturnRows(sqlmock.NewRows([]string{"status", "created_at", "updated_at"}).AddRow(site.JobRunning, rowTime, rowTime))
	require.NoError(t, s.CreateJob(ctx, kept))
	assert.Len(t, kept.Skipped, 1)

	mock.ExpectQuery(q("INSERT INTO site_jobs")).WillReturnError(errDown)
	require.ErrorContains(t, s.CreateJob(ctx, j()), "create job")
}

func TestJob(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)

	mock.ExpectQuery(q("FROM site_jobs WHERE id = $1")).WithArgs("sjob_1").
		WillReturnRows(jobRows(jobRow("sjob_1", site.JobRunning, `[{"address":"/gone","reason":"answered 404"}]`)))
	got, err := s.Job(ctx, "sjob_1")
	require.NoError(t, err)
	assert.Equal(t, site.JobRunning, got.Status)
	assert.Equal(t, 2, got.Done)
	assert.Equal(t, 9, got.Total)
	assert.Equal(t, []site.Skipped{{Address: "/gone", Reason: "answered 404"}}, got.Skipped)

	mock.ExpectQuery(q("FROM site_jobs")).WillReturnRows(jobRows(jobRow("sjob_1", site.JobDone, `[]`)))
	got, err = s.Job(ctx, "sjob_1")
	require.NoError(t, err)
	assert.Equal(t, []site.Skipped{}, got.Skipped, "none is [], never null")

	mock.ExpectQuery(q("FROM site_jobs")).WillReturnError(sql.ErrNoRows)
	_, err = s.Job(ctx, "none")
	require.ErrorIs(t, err, site.ErrNoJob)

	mock.ExpectQuery(q("FROM site_jobs")).WillReturnError(errDown)
	_, err = s.Job(ctx, "sjob_1")
	require.ErrorContains(t, err, "read job")

	mock.ExpectQuery(q("FROM site_jobs")).WillReturnRows(jobRows(jobRow("sjob_1", site.JobDone, `not json`)))
	_, err = s.Job(ctx, "sjob_1")
	require.ErrorContains(t, err, "decode skipped addresses")
}

func TestActiveJobs(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)

	mock.ExpectQuery(q("WHERE site_id = $1 AND status = $2 ORDER BY created_at DESC")).WithArgs("site_1", site.JobRunning).
		WillReturnRows(jobRows(jobRow("sjob_2", site.JobRunning, `[]`), jobRow("sjob_1", site.JobRunning, `[]`)))
	got, err := s.ActiveJobs(ctx, "site_1")
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "sjob_2", got[0].ID)

	mock.ExpectQuery(q("FROM site_jobs")).WillReturnRows(jobRows())
	got, err = s.ActiveJobs(ctx, "site_1")
	require.NoError(t, err)
	assert.Equal(t, []site.Job{}, got, "none is [], never null")

	mock.ExpectQuery(q("FROM site_jobs")).WillReturnError(errDown)
	_, err = s.ActiveJobs(ctx, "site_1")
	require.ErrorContains(t, err, "list jobs")

	mock.ExpectQuery(q("FROM site_jobs")).WillReturnRows(jobRows(jobRow("sjob_1", site.JobRunning, `not json`)))
	_, err = s.ActiveJobs(ctx, "site_1")
	require.ErrorContains(t, err, "decode skipped addresses")

	mock.ExpectQuery(q("FROM site_jobs")).WillReturnRows(jobRows(jobRow("sjob_1", site.JobRunning, `[]`)).RowError(0, errDown))
	_, err = s.ActiveJobs(ctx, "site_1")
	require.ErrorContains(t, err, "list jobs")
}

func TestReportJob(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)
	p := site.JobProgress{Phase: "Writing files", Done: 1, Total: 3, Pages: 4, Redirects: 5}

	mock.ExpectQuery(q("UPDATE site_jobs SET phase = $2")).WithArgs("sjob_1", p.Phase, p.Done, p.Total, p.Pages, p.Redirects, p.Bytes, p.Skipped).
		WillReturnRows(sqlmock.NewRows([]string{"stop_requested"}).AddRow(true))
	stop, err := s.ReportJob(ctx, "sjob_1", p)
	require.NoError(t, err)
	assert.True(t, stop, "a stop asked for on another replica is read in the same statement")

	mock.ExpectQuery(q("UPDATE site_jobs")).WillReturnError(sql.ErrNoRows)
	_, err = s.ReportJob(ctx, "none", p)
	require.ErrorIs(t, err, site.ErrNoJob)

	mock.ExpectQuery(q("UPDATE site_jobs")).WillReturnError(errDown)
	_, err = s.ReportJob(ctx, "sjob_1", p)
	require.ErrorContains(t, err, "report job")
}

func TestFinishJob(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)
	r := site.JobResult{Status: site.JobDone, Release: 3, Pages: 4, Redirects: 5, Skipped: []site.Skipped{{Address: "/a", Reason: "b"}}, Error: ""}

	mock.ExpectExec(q("UPDATE site_jobs SET status = $2")).
		WithArgs("sjob_1", site.JobDone, 3, 4, 5, []byte(`[{"address":"/a","reason":"b"}]`), "").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.FinishJob(ctx, "sjob_1", r))

	mock.ExpectExec(q("UPDATE site_jobs")).WithArgs("sjob_1", site.JobFailed, 0, 0, 0, []byte(`[]`), "why").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.FinishJob(ctx, "sjob_1", site.JobResult{Status: site.JobFailed, Error: "why"}), "no skipped address is stored as []")

	mock.ExpectExec(q("UPDATE site_jobs")).WillReturnResult(sqlmock.NewResult(0, 0))
	require.ErrorIs(t, s.FinishJob(ctx, "none", r), site.ErrNoJob)

	mock.ExpectExec(q("UPDATE site_jobs")).WillReturnError(errDown)
	require.ErrorContains(t, s.FinishJob(ctx, "sjob_1", r), "finish job")
}

func TestStopJob(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)

	mock.ExpectExec(q("UPDATE site_jobs SET stop_requested = TRUE WHERE id = $1 AND status = $2")).WithArgs("sjob_1", site.JobRunning).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.StopJob(ctx, "sjob_1"))

	mock.ExpectExec(q("UPDATE site_jobs")).WillReturnResult(sqlmock.NewResult(0, 0))
	require.ErrorIs(t, s.StopJob(ctx, "sjob_done"), site.ErrNoJob, "a job already finished cannot be stopped")

	mock.ExpectExec(q("UPDATE site_jobs")).WillReturnError(errDown)
	require.ErrorContains(t, s.StopJob(ctx, "sjob_1"), "stop job")
}
