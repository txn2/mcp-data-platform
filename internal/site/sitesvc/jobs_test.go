package sitesvc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/site"
)

// awaitJob waits for a job's goroutine to record how it ended. It never sleeps:
// the fake store sends the job on a channel when FinishJob runs.
func awaitJob(t *testing.T, store *memStore) site.Job {
	t.Helper()
	select {
	case j := <-store.finished:
		return j
	case <-time.After(30 * time.Second):
		require.FailNow(t, "the job never finished")
		return site.Job{}
	}
}

func readFiles(files ...InputFile) func(context.Context) ([]InputFile, error) {
	return func(context.Context) ([]InputFile, error) { return files, nil }
}

type ctxKey struct{}

func TestStartPublish_RunsApartFromTheRequest(t *testing.T) {
	svc, store, _, _ := fixture(t)
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), ctxKey{}, "jane"))
	proceed := make(chan struct{})
	var readErr error
	var who any
	job, err := svc.StartPublish(ctx, owner, PublishJob{
		Site: "marketing", Source: "upload", Note: "first",
		Read: func(c context.Context) ([]InputFile, error) {
			<-proceed
			readErr, who = c.Err(), c.Value(ctxKey{})
			return []InputFile{page("index.html")}, nil
		},
	})
	require.NoError(t, err)
	assert.Equal(t, site.JobPublish, job.Kind)
	assert.Equal(t, site.EnvDefault, job.Environment, "no environment named is the default")
	assert.Equal(t, phaseReading, job.Phase)
	assert.Equal(t, owner.Email, job.CreatedBy)

	cancel() // the request ends while the job is still reading
	close(proceed)

	done := awaitJob(t, store)
	require.NoError(t, readErr, "closing the request does not stop the job")
	assert.Equal(t, "jane", who, "the job keeps who asked")
	assert.Equal(t, site.JobDone, done.Status)
	assert.Equal(t, 1, done.Release)
	assert.Equal(t, 1, store.sites[siteID].Environment(site.EnvDefault).Release, "the environment moved")

	phases := make([]string, 0, len(store.reports))
	for _, p := range store.reports {
		phases = append(phases, p.Phase)
	}
	assert.Contains(t, phases, phaseWriting)
	assert.Contains(t, phases, "Switching default to the new release")
	last := store.reports[len(store.reports)-1]
	assert.Equal(t, 1, last.Done, "the last file's report is always written")
	assert.Equal(t, 1, last.Total)
}

func TestStartPublish_RefusesInTheRequest(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		actor Actor
		in    PublishJob
		want  error
	}{
		{"no such site", owner, PublishJob{Site: "nope"}, site.ErrNotFound},
		{"someone who does not manage it", stranger, PublishJob{Site: "marketing"}, site.ErrNotAllowed},
		{"no such environment", owner, PublishJob{Site: "marketing", Environment: "qa"}, site.ErrNoEnvironment},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, store, _, _ := fixture(t)
			tc.in.Read = func(context.Context) ([]InputFile, error) {
				t.Error("a refused job reads nothing")
				return nil, nil
			}
			_, err := svc.StartPublish(ctx, tc.actor, tc.in)
			require.ErrorIs(t, err, tc.want)
			assert.Empty(t, store.jobs, "no job is recorded")
		})
	}

	t.Run("an environment that requires approval", func(t *testing.T) {
		svc, store, _, _ := fixture(t)
		_, err := svc.StartPublish(ctx, admin, PublishJob{Site: "marketing", Environment: envProd, Read: readFiles()})
		var r *RefusedError
		require.ErrorAs(t, err, &r)
		assert.Contains(t, r.Msg, "requires approval")
		assert.Empty(t, store.jobs)
	})
	t.Run("the job cannot be recorded", func(t *testing.T) {
		svc, store, _, _ := fixture(t)
		boom := errors.New("down")
		store.failJob = boom
		_, err := svc.StartPublish(ctx, owner, PublishJob{Site: "marketing", Read: readFiles()})
		require.ErrorIs(t, err, boom)
	})
}

func TestStartPublish_EachWayItEnds(t *testing.T) {
	ctx := context.Background()
	const secret = "dial tcp 10.1.2.3:5432: password authentication failed for user admin"
	cases := []struct {
		name      string
		read      func(context.Context) ([]InputFile, error)
		wantError string
	}{
		{"a read that is refused says why", func(context.Context) ([]InputFile, error) {
			return nil, &RefusedError{Msg: "the archive is not a zip"}
		}, "the archive is not a zip"},
		{"a read that breaks is said in general terms", func(context.Context) ([]InputFile, error) {
			return nil, errors.New(secret)
		}, jobBroke},
		{"a publish that is refused says why", readFiles(), "holds no files"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, store, _, _ := fixture(t)
			_, err := svc.StartPublish(ctx, owner, PublishJob{Site: "marketing", Read: tc.read})
			require.NoError(t, err)
			done := awaitJob(t, store)
			assert.Equal(t, site.JobFailed, done.Status)
			assert.Contains(t, done.Error, tc.wantError)
			assert.NotContains(t, done.Error, "10.1.2.3", "what broke underneath is logged, never shown")
			assert.Zero(t, done.Release)
			assert.Zero(t, store.sites[siteID].Environment(site.EnvDefault).Release)
		})
	}

	t.Run("a job whose end cannot be recorded is logged", func(t *testing.T) {
		svc, store, _, _ := fixture(t)
		store.failFinish = errors.New("down")
		_, err := svc.StartPublish(ctx, owner, PublishJob{Site: "marketing", Read: readFiles(page("a.html"))})
		require.NoError(t, err)
		assert.Equal(t, site.JobDone, awaitJob(t, store).Status)
	})
}

func TestStartCopy_PublishesWhatItBroughtAcross(t *testing.T) {
	svc, store, _, _ := fixture(t)
	skipped := []site.Skipped{{Address: "/gone", Reason: "answered 404"}}
	job, err := svc.StartCopy(context.Background(), owner, CopyJob{
		Site: "marketing", Source: "https://old.example.org/", Note: "moved",
		Copy: func(_ context.Context, progress func(CopyStats) bool) (*CopyResult, error) {
			assert.False(t, progress(CopyStats{Pages: 1, Known: 4, Bytes: 9, Redirects: 2, Skipped: 1}), "nothing asked it to stop")
			return &CopyResult{Files: []InputFile{page("index.html")}, Pages: 3, Redirects: 2, Skipped: skipped}, nil
		},
	})
	require.NoError(t, err)
	assert.Equal(t, site.JobCrawl, job.Kind)
	assert.Equal(t, phaseCopying, job.Phase)

	done := awaitJob(t, store)
	assert.Equal(t, site.JobDone, done.Status)
	assert.Equal(t, 1, done.Release)
	assert.Equal(t, 3, done.Pages)
	assert.Equal(t, 2, done.Redirects)
	assert.Equal(t, skipped, done.Skipped)
	assert.Equal(t, "https://old.example.org/", store.releases[siteID][0].Source)
	assert.Equal(t, "moved", store.releases[siteID][0].Note)
	assert.Equal(t, site.JobProgress{Phase: phaseCopying, Done: 1, Total: 4, Pages: 1, Bytes: 9, Redirects: 2, Skipped: 1}, store.reports[0], "the first report carries the copy's count")
}

func TestStartCopy_StoppedOnRequest(t *testing.T) {
	ctx := context.Background()
	svc, store, _, _ := fixture(t)
	proceed := make(chan struct{})
	stopped := make(chan bool, 1)
	job, err := svc.StartCopy(ctx, owner, CopyJob{
		Site: "marketing",
		Copy: func(_ context.Context, progress func(CopyStats) bool) (*CopyResult, error) {
			<-proceed
			stop := progress(CopyStats{Pages: 1, Known: 2})
			stopped <- stop
			if stop {
				return nil, ErrStopped
			}
			return &CopyResult{}, nil
		},
	})
	require.NoError(t, err)

	require.NoError(t, svc.StopJob(ctx, owner, "marketing", job.ID))
	close(proceed)

	assert.True(t, <-stopped, "the copy's next report is told to stop")
	assert.Equal(t, site.JobStopped, awaitJob(t, store).Status)
	assert.Empty(t, store.releases[siteID], "a stopped copy publishes nothing")
}

func TestStartCopy_Failures(t *testing.T) {
	ctx := context.Background()
	t.Run("a copy that breaks", func(t *testing.T) {
		svc, store, _, _ := fixture(t)
		_, err := svc.StartCopy(ctx, owner, CopyJob{
			Site: "marketing",
			Copy: func(context.Context, func(CopyStats) bool) (*CopyResult, error) {
				return nil, errors.New("connect: 10.1.2.3 refused")
			},
		})
		require.NoError(t, err)
		done := awaitJob(t, store)
		assert.Equal(t, site.JobFailed, done.Status)
		assert.Equal(t, jobBroke, done.Error)
	})
	t.Run("a publish refused afterwards keeps the copy's counts", func(t *testing.T) {
		svc, store, _, _ := fixture(t)
		skipped := []site.Skipped{{Address: "/x", Reason: "answered 500"}}
		_, err := svc.StartCopy(ctx, owner, CopyJob{
			Site: "marketing",
			Copy: func(context.Context, func(CopyStats) bool) (*CopyResult, error) {
				return &CopyResult{Pages: 5, Redirects: 1, Skipped: skipped}, nil
			},
		})
		require.NoError(t, err)
		done := awaitJob(t, store)
		assert.Equal(t, site.JobFailed, done.Status)
		assert.Contains(t, done.Error, "holds no files")
		assert.Equal(t, 5, done.Pages)
		assert.Equal(t, 1, done.Redirects)
		assert.Equal(t, skipped, done.Skipped)
	})
	t.Run("refused in the request", func(t *testing.T) {
		svc, store, _, _ := fixture(t)
		_, err := svc.StartCopy(ctx, stranger, CopyJob{Site: "marketing"})
		require.ErrorIs(t, err, site.ErrNotAllowed)
		assert.Empty(t, store.jobs)
	})
	t.Run("the job cannot be recorded", func(t *testing.T) {
		svc, store, _, _ := fixture(t)
		boom := errors.New("down")
		store.failJob = boom
		_, err := svc.StartCopy(ctx, owner, CopyJob{Site: "marketing"})
		require.ErrorIs(t, err, boom)
	})
}

func TestReporter_PacesWritesAndKeepsTheLastStop(t *testing.T) {
	ctx := context.Background()
	store := newMem()
	store.jobs["sjob_1"] = &site.Job{ID: "sjob_1", Status: site.JobRunning}
	r := &reporter{s: &Service{Store: store}, id: "sjob_1"}
	set := func(n int) func(*site.JobProgress) { return func(p *site.JobProgress) { p.Done = n } }

	assert.False(t, r.update(ctx, set(1), false), "the first report is written")
	assert.Len(t, store.reports, 1)

	r.update(ctx, set(2), false)
	assert.Len(t, store.reports, 1, "a second within the pace is held")

	r.update(ctx, set(3), true)
	require.Len(t, store.reports, 2, "a forced report is written")
	assert.Equal(t, 3, store.reports[1].Done, "merging every change held so far")

	store.jobs["sjob_1"].Status = site.JobStopped
	assert.True(t, r.update(ctx, set(4), true), "the store said to stop")

	store.failReport = errors.New("down")
	assert.True(t, r.update(ctx, set(5), true), "a failed write keeps the last answer")
	assert.True(t, r.update(ctx, set(6), false), "a held report keeps the last answer")
}

func TestFailed(t *testing.T) {
	ctx := context.Background()
	assert.Equal(t, site.JobResult{Status: site.JobFailed, Error: "why"}, failed(ctx, &RefusedError{Msg: "why"}))
	wrapped := errors.Join(errors.New("context"), &RefusedError{Msg: "inner"})
	assert.Equal(t, "inner", failed(ctx, wrapped).Error, "a wrapped refusal is still a refusal")
	assert.Equal(t, site.JobResult{Status: site.JobFailed, Error: jobBroke}, failed(ctx, errors.New("password=hunter2")))
}

func staleJob(status string) *site.Job {
	return &site.Job{ID: "sjob_old", SiteID: siteID, Status: status, UpdatedAt: time.Now().Add(-jobStaleAfter - time.Minute)}
}

func TestJobOf(t *testing.T) {
	ctx := context.Background()

	t.Run("a job of the site", func(t *testing.T) {
		svc, store, _, _ := fixture(t)
		store.jobs["sjob_1"] = &site.Job{ID: "sjob_1", SiteID: siteID, Status: site.JobRunning, UpdatedAt: time.Now()}
		got, err := svc.JobOf(ctx, "marketing", "sjob_1")
		require.NoError(t, err)
		assert.Equal(t, site.JobRunning, got.Status)
		assert.Empty(t, got.Error)
	})
	t.Run("nobody has reported on it for a while", func(t *testing.T) {
		svc, store, _, _ := fixture(t)
		store.jobs["sjob_old"] = staleJob(site.JobRunning)
		got, err := svc.JobOf(ctx, "marketing", "sjob_old")
		require.NoError(t, err)
		assert.Equal(t, site.JobFailed, got.Status)
		assert.Equal(t, jobLost, got.Error)
	})
	t.Run("a job that finished long ago stays finished", func(t *testing.T) {
		svc, store, _, _ := fixture(t)
		store.jobs["sjob_old"] = staleJob(site.JobDone)
		got, err := svc.JobOf(ctx, "marketing", "sjob_old")
		require.NoError(t, err)
		assert.Equal(t, site.JobDone, got.Status)
	})
	t.Run("another site's job is not found", func(t *testing.T) {
		svc, store, _, _ := fixture(t)
		store.jobs["sjob_x"] = &site.Job{ID: "sjob_x", SiteID: "site_other", Status: site.JobRunning, UpdatedAt: time.Now()}
		_, err := svc.JobOf(ctx, "marketing", "sjob_x")
		require.ErrorIs(t, err, site.ErrNoJob)
	})
	t.Run("no such site", func(t *testing.T) {
		svc, _, _, _ := fixture(t)
		_, err := svc.JobOf(ctx, "nope", "sjob_1")
		require.ErrorIs(t, err, site.ErrNotFound)
	})
	t.Run("no such job", func(t *testing.T) {
		svc, _, _, _ := fixture(t)
		_, err := svc.JobOf(ctx, "marketing", "sjob_none")
		require.ErrorIs(t, err, site.ErrNoJob)
	})
}

func TestActiveJobs(t *testing.T) {
	ctx := context.Background()
	svc, store, _, _ := fixture(t)

	none, err := svc.ActiveJobs(ctx, "marketing")
	require.NoError(t, err)
	assert.NotNil(t, none, "none is [], never null")
	assert.Empty(t, none)

	store.jobs["sjob_live"] = &site.Job{ID: "sjob_live", SiteID: siteID, Status: site.JobRunning, UpdatedAt: time.Now()}
	store.jobs["sjob_old"] = staleJob(site.JobRunning)
	store.jobs["sjob_done"] = &site.Job{ID: "sjob_done", SiteID: siteID, Status: site.JobDone, UpdatedAt: time.Now()}
	store.jobs["sjob_other"] = &site.Job{ID: "sjob_other", SiteID: "site_other", Status: site.JobRunning, UpdatedAt: time.Now()}

	got, err := svc.ActiveJobs(ctx, siteID)
	require.NoError(t, err)
	require.Len(t, got, 1, "a job nobody has reported on is not running")
	assert.Equal(t, "sjob_live", got[0].ID)

	_, err = svc.ActiveJobs(ctx, "nope")
	require.ErrorIs(t, err, site.ErrNotFound)

	boom := errors.New("down")
	store.failJob = boom
	_, err = svc.ActiveJobs(ctx, "marketing")
	require.ErrorIs(t, err, boom)
}

func TestStopJob(t *testing.T) {
	ctx := context.Background()
	live := func() *site.Job {
		return &site.Job{ID: "sjob_1", SiteID: siteID, Status: site.JobRunning, UpdatedAt: time.Now()}
	}

	t.Run("someone who manages the site", func(t *testing.T) {
		svc, store, _, _ := fixture(t)
		grant(store, manager.Email, site.LevelManage)
		store.jobs["sjob_1"] = live()
		require.NoError(t, svc.StopJob(ctx, manager, "marketing", "sjob_1"))
		assert.Equal(t, site.JobStopped, store.jobs["sjob_1"].Status)
	})
	for name, actor := range map[string]Actor{"a stranger": stranger, "a viewer": viewer, "an unverified owner": unverified(owner)} {
		t.Run(name, func(t *testing.T) {
			svc, store, _, _ := fixture(t)
			grant(store, viewer.Email, site.LevelView)
			store.jobs["sjob_1"] = live()
			require.ErrorIs(t, svc.StopJob(ctx, actor, "marketing", "sjob_1"), site.ErrNotAllowed)
			assert.Equal(t, site.JobRunning, store.jobs["sjob_1"].Status)
		})
	}
	t.Run("another site's job", func(t *testing.T) {
		svc, store, _, _ := fixture(t)
		store.jobs["sjob_x"] = &site.Job{ID: "sjob_x", SiteID: "site_other", Status: site.JobRunning, UpdatedAt: time.Now()}
		require.ErrorIs(t, svc.StopJob(ctx, owner, "marketing", "sjob_x"), site.ErrNoJob)
		assert.Equal(t, site.JobRunning, store.jobs["sjob_x"].Status)
	})
	t.Run("no such site", func(t *testing.T) {
		svc, _, _, _ := fixture(t)
		require.ErrorIs(t, svc.StopJob(ctx, owner, "nope", "sjob_1"), site.ErrNotFound)
	})
	t.Run("no such job", func(t *testing.T) {
		svc, _, _, _ := fixture(t)
		require.ErrorIs(t, svc.StopJob(ctx, owner, "marketing", "sjob_none"), site.ErrNoJob)
	})
	t.Run("the store refuses", func(t *testing.T) {
		svc, store, _, _ := fixture(t)
		boom := errors.New("down")
		store.jobs["sjob_1"] = live()
		store.failStop = boom
		require.ErrorIs(t, svc.StopJob(ctx, owner, "marketing", "sjob_1"), boom)
	})
}
