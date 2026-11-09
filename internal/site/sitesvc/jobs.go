package sitesvc

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitestore"
)

const (
	// jobTimeout bounds one publish or copy.
	jobTimeout = time.Hour
	// jobStaleAfter is how long a job may go unreported before it reads as
	// failed: the replica running it stopped.
	jobStaleAfter = 15 * time.Minute
	// reportEvery paces the progress a job writes.
	reportEvery = time.Second

	phaseReading = "Reading files"
	phaseWriting = "Writing files"
	phaseCopying = "Copying pages"
	jobLost      = "the replica running this stopped before it finished"
	jobBroke     = "this could not be finished; try again"
)

// ErrStopped is what a copy returns when it was asked to stop.
var ErrStopped = errors.New("stopped")

// CopyStats is how far a copy has got.
type CopyStats struct {
	Pages     int
	Known     int
	Bytes     int64
	Redirects int
	Skipped   int
}

// MayPublish refuses a caller who could not publish to the environment, so
// that a copy fetches nothing on their behalf before being refused.
func (s *Service) MayPublish(ctx context.Context, actor Actor, ref, environment string) error {
	_, _, err := s.jobTarget(ctx, actor, ref, environment)
	return err
}

// CopyResult is what a copy of a site brought across.
type CopyResult struct {
	Files     []InputFile
	Pages     int
	Redirects int
	Skipped   []site.Skipped
}

// PublishJob is a publish to run in the background. Read supplies the files
// and runs as the caller: ctx carries who is asking.
type PublishJob struct {
	Site        string
	Environment string
	Source      string
	Note        string
	Read        func(ctx context.Context) ([]InputFile, error)
}

// CopyJob copies an existing site in the background. Copy reports progress and
// is told to stop when its Progress returns true.
type CopyJob struct {
	Site        string
	Environment string
	Source      string
	Note        string
	Copy        func(ctx context.Context, progress func(CopyStats) bool) (*CopyResult, error)
}

// reporter paces one job's writes.
type reporter struct {
	s    *Service
	id   string
	mu   sync.Mutex
	last time.Time
	p    site.JobProgress
	stop bool
}

// update merges a change into the job's state and writes it, at most once
// per reportEvery unless forced, and reports whether the job should stop.
func (r *reporter) update(ctx context.Context, change func(*site.JobProgress), force bool) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	change(&r.p)
	if !force && time.Since(r.last) < reportEvery {
		return r.stop
	}
	r.last = time.Now()
	stop, err := r.s.Store.ReportJob(ctx, r.id, r.p)
	if err != nil {
		slog.WarnContext(ctx, "sites: reporting a job failed", "error", logsan.SanitizeForLog(err.Error()))
		return r.stop
	}
	r.stop = stop
	return stop
}

// StartPublish checks the caller may publish, records the job, and runs it
// apart from the request: closing the dialog does not stop it.
func (s *Service) StartPublish(ctx context.Context, actor Actor, in PublishJob) (*site.Job, error) {
	st, env, err := s.jobTarget(ctx, actor, in.Site, in.Environment)
	if err != nil {
		return nil, err
	}
	job, err := s.newJob(ctx, &site.Job{SiteID: st.ID, Kind: site.JobPublish, Environment: env.Name, Source: in.Source, Phase: phaseReading, CreatedBy: actor.Email})
	if err != nil {
		return nil, err
	}
	s.run(ctx, job, func(ctx context.Context, r *reporter) site.JobResult {
		files, err := in.Read(ctx)
		if err != nil {
			return failed(ctx, err)
		}
		return s.publishFiles(ctx, actor, r, in.publishRequest(files), site.JobResult{})
	})
	return job, nil
}

func (in PublishJob) publishRequest(files []InputFile) PublishRequest {
	return PublishRequest{Site: in.Site, Environment: in.Environment, Source: in.Source, Note: in.Note, Files: files}
}

// StartCopy checks the caller may publish, records the job, and copies the
// site apart from the request.
func (s *Service) StartCopy(ctx context.Context, actor Actor, in CopyJob) (*site.Job, error) {
	st, env, err := s.jobTarget(ctx, actor, in.Site, in.Environment)
	if err != nil {
		return nil, err
	}
	job, err := s.newJob(ctx, &site.Job{SiteID: st.ID, Kind: site.JobCrawl, Environment: env.Name, Source: in.Source, Phase: phaseCopying, CreatedBy: actor.Email})
	if err != nil {
		return nil, err
	}
	s.run(ctx, job, func(ctx context.Context, r *reporter) site.JobResult {
		got, err := in.Copy(ctx, func(c CopyStats) bool {
			return r.update(ctx, func(p *site.JobProgress) {
				p.Pages, p.Done, p.Total, p.Bytes, p.Redirects, p.Skipped = c.Pages, c.Pages, c.Known, c.Bytes, c.Redirects, c.Skipped
			}, false)
		})
		if errors.Is(err, ErrStopped) {
			return site.JobResult{Status: site.JobStopped}
		}
		if err != nil {
			return failed(ctx, err)
		}
		base := site.JobResult{Pages: got.Pages, Redirects: got.Redirects, Skipped: got.Skipped}
		return s.publishFiles(ctx, actor, r, PublishRequest{
			Site: in.Site, Environment: in.Environment, Source: in.Source, Note: in.Note, Files: got.Files,
		}, base)
	})
	return job, nil
}

// jobTarget is the site and environment a job would land in, or why not, so a
// refusal is answered to the request rather than found later.
func (s *Service) jobTarget(ctx context.Context, actor Actor, ref, environment string) (*site.Site, *site.Environment, error) {
	st, err := s.Find(ctx, ref)
	if err != nil {
		return nil, nil, err
	}
	if err := s.mayEdit(ctx, actor, st); err != nil {
		return nil, nil, err
	}
	env, err := s.publishTarget(st, PublishRequest{Environment: environment})
	if err != nil {
		return nil, nil, err
	}
	return st, env, nil
}

func (s *Service) newJob(ctx context.Context, job *site.Job) (*site.Job, error) {
	job.ID = sitestore.NewJobID()
	if err := s.Store.CreateJob(ctx, job); err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	return job, nil
}

// run does the work on a context that outlives the request and keeps who
// asked, and records how it ended.
func (s *Service) run(ctx context.Context, job *site.Job, work func(context.Context, *reporter) site.JobResult) {
	r := &reporter{s: s, id: job.ID, p: site.JobProgress{Phase: job.Phase}}
	detached := context.WithoutCancel(ctx)
	go func() {
		bounded, cancel := context.WithTimeout(detached, jobTimeout)
		defer cancel()
		result := work(bounded, r)
		if err := s.Store.FinishJob(detached, job.ID, result); err != nil {
			slog.ErrorContext(detached, "sites: recording a job's end failed", "error", logsan.SanitizeForLog(err.Error()))
		}
	}()
}

func (s *Service) publishFiles(ctx context.Context, actor Actor, r *reporter, req PublishRequest, out site.JobResult) site.JobResult {
	r.update(ctx, func(p *site.JobProgress) { p.Phase = phaseWriting }, true)
	req.Progress = func(done, total int) {
		r.update(ctx, func(p *site.JobProgress) { p.Done, p.Total = done, total }, done == total)
	}
	req.Phase = func(phase string) { r.update(ctx, func(p *site.JobProgress) { p.Phase = phase }, true) }
	pub, err := s.Publish(ctx, actor, req)
	if err != nil {
		res := failed(ctx, err)
		res.Pages, res.Redirects, res.Skipped = out.Pages, out.Redirects, out.Skipped
		return res
	}
	out.Status, out.Release = site.JobDone, pub.Release.Number
	return out
}

// failed is the end of a job that did not finish. A refusal names the file and
// the limit and is shown; anything else is logged and said in general terms.
func failed(ctx context.Context, err error) site.JobResult {
	if r, ok := errors.AsType[*RefusedError](err); ok {
		return site.JobResult{Status: site.JobFailed, Error: r.Msg}
	}
	slog.ErrorContext(ctx, "sites: a job failed", "error", logsan.SanitizeForLog(err.Error()))
	return site.JobResult{Status: site.JobFailed, Error: jobBroke}
}

// JobOf reads a job of the site, reading one nobody has reported on for a
// while as failed.
func (s *Service) JobOf(ctx context.Context, ref, id string) (*site.Job, error) {
	st, err := s.Find(ctx, ref)
	if err != nil {
		return nil, err
	}
	job, err := s.Store.Job(ctx, id)
	if err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	if job.SiteID != st.ID {
		return nil, site.ErrNoJob
	}
	markLost(job)
	return job, nil
}

func markLost(job *site.Job) {
	if job.Status == site.JobRunning && time.Since(job.UpdatedAt) > jobStaleAfter {
		job.Status, job.Error = site.JobFailed, jobLost
	}
}

// ActiveJobs lists the site's jobs still running.
func (s *Service) ActiveJobs(ctx context.Context, ref string) ([]site.Job, error) {
	st, err := s.Find(ctx, ref)
	if err != nil {
		return nil, err
	}
	jobs, err := s.Store.ActiveJobs(ctx, st.ID)
	if err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	running := make([]site.Job, 0, len(jobs))
	for i := range jobs {
		markLost(&jobs[i])
		if jobs[i].Status == site.JobRunning {
			running = append(running, jobs[i])
		}
	}
	return running, nil
}

// StopJob asks a copy to stop.
func (s *Service) StopJob(ctx context.Context, actor Actor, ref, id string) error {
	st, err := s.Find(ctx, ref)
	if err != nil {
		return err
	}
	if err := s.mayEdit(ctx, actor, st); err != nil {
		return err
	}
	job, err := s.Store.Job(ctx, id)
	if err != nil {
		return err //nolint:wrapcheck // read by identity
	}
	if job.SiteID != st.ID {
		return site.ErrNoJob
	}
	return s.Store.StopJob(ctx, id) //nolint:wrapcheck // read by identity
}
