// SPDX-License-Identifier: Apache-2.0

package plugintest

import (
	"context"
	"time"

	jobsv1 "github.com/panelarr/source-api/gen/panelarr/jobs/v1"
	sourcev1 "github.com/panelarr/source-api/gen/panelarr/source/v1"
	"github.com/panelarr/source-api/sdk/jobs"
)

// newJobs returns the fake's jobs: "refresh", which counts the sources and
// finishes at once, and "hold", which runs until it's cancelled or
// [Fake.Release] is called, so hosts can test a busy job.
func (f *Fake) newJobs() *jobs.Runner {
	r := jobs.New(jobs.Options{})
	must(r.Add(jobs.Job{
		ID:          "refresh",
		Name:        "Refresh the catalogue",
		Description: "Counts the sources. Made up.",
		Group:       "Catalogue",
		Params:      []*jobsv1.JobParam{{Name: "force", Type: jobs.TypeBool, Description: "Refresh even when nothing changed.", Default: "false"}},
		Run: func(_ context.Context, run *jobs.Run) error {
			run.Count("sources", int64(len(f.Sources)))
			run.SetMessage("Refreshed")
			return nil
		},
	}))
	must(r.Add(jobs.Job{
		ID:          "hold",
		Name:        "Hold",
		Description: "Runs until cancelled or released.",
		Group:       "Catalogue",
		Cancellable: true,
		Run: func(ctx context.Context, run *jobs.Run) error {
			run.Progress("Holding", 0, 1, "steps")
			select {
			case <-f.release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Minute):
				return nil
			}
		},
	}))
	return r
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

// Release ends the running "hold" job.
func (f *Fake) Release() {
	select {
	case f.release <- struct{}{}:
	default:
	}
}

// ListJobs lists the fake's jobs, when it has them.
func (f *Fake) ListJobs(ctx context.Context, r *sourcev1.ListJobsRequest) (*sourcev1.ListJobsResponse, error) {
	if f.Jobs == nil {
		return f.Unimplemented.ListJobs(ctx, r)
	}
	return f.Jobs.ListJobs(ctx, r)
}

func (f *Fake) GetJob(ctx context.Context, r *sourcev1.GetJobRequest) (*sourcev1.GetJobResponse, error) {
	if f.Jobs == nil {
		return f.Unimplemented.GetJob(ctx, r)
	}
	return f.Jobs.GetJob(ctx, r)
}

func (f *Fake) ListJobRuns(ctx context.Context, r *sourcev1.ListJobRunsRequest) (*sourcev1.ListJobRunsResponse, error) {
	if f.Jobs == nil {
		return f.Unimplemented.ListJobRuns(ctx, r)
	}
	return f.Jobs.ListJobRuns(ctx, r)
}

func (f *Fake) GetJobRun(ctx context.Context, r *sourcev1.GetJobRunRequest) (*sourcev1.GetJobRunResponse, error) {
	if f.Jobs == nil {
		return f.Unimplemented.GetJobRun(ctx, r)
	}
	return f.Jobs.GetJobRun(ctx, r)
}

func (f *Fake) RunJob(ctx context.Context, r *sourcev1.RunJobRequest) (*sourcev1.RunJobResponse, error) {
	if f.Jobs == nil {
		return f.Unimplemented.RunJob(ctx, r)
	}
	return f.Jobs.RunJob(ctx, r)
}

func (f *Fake) CancelJobRun(ctx context.Context, r *sourcev1.CancelJobRunRequest) (*sourcev1.CancelJobRunResponse, error) {
	if f.Jobs == nil {
		return f.Unimplemented.CancelJobRun(ctx, r)
	}
	return f.Jobs.CancelJobRun(ctx, r)
}
