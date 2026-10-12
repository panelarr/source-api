// SPDX-License-Identifier: Apache-2.0

package jobs_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	jobsv1 "github.com/panelarr/source-api/gen/panelarr/jobs/v1"
	sourcev1 "github.com/panelarr/source-api/gen/panelarr/source/v1"
	"github.com/panelarr/source-api/sdk/jobs"
	"github.com/panelarr/source-api/sdk/plugin"
)

func newRunner(t *testing.T, js ...jobs.Job) *jobs.Runner {
	t.Helper()
	r := jobs.New(jobs.Options{RunIDPrefix: "p-"})
	for _, j := range js {
		if err := r.Add(j); err != nil {
			t.Fatal(err)
		}
	}
	r.Start()
	t.Cleanup(r.Stop)
	return r
}

func wait(t *testing.T, r *jobs.Runner, id string) *jobsv1.JobRun {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	run, err := r.Wait(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func TestRunRecordsOutcome(t *testing.T) {
	r := newRunner(t, jobs.Job{
		ID: "refresh", Name: "Refresh",
		Params: []*jobsv1.JobParam{
			{Name: "force", Type: jobs.TypeBool, Default: "false"},
			{Name: "langs", Type: jobs.TypeStringList, Choices: []string{"en", "fr"}},
		},
		Run: func(_ context.Context, run *jobs.Run) error {
			run.Count("updates", 2)
			run.Add("updates", 1)
			run.Note(jobs.LevelWarn, "one store is slow", "store-a")
			run.SetMessage("3 updates " + run.Param("force") + " " + run.Param("langs"))
			return nil
		},
	})
	ctx := context.Background()
	res, err := r.RunJob(ctx, &sourcev1.RunJobRequest{JobId: "refresh", Params: map[string]string{"langs": "en, fr"}, OnBehalfOf: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if res.GetRun().GetTrigger() != jobsv1.JobTrigger_JOB_TRIGGER_MANUAL || res.GetRun().GetTriggeredBy() != "alice" || res.GetRun().GetId()[:2] != "p-" {
		t.Fatalf("run: %v", res.GetRun())
	}
	run := wait(t, r, res.GetRun().GetId())
	s := run.GetSummary()
	if s.GetStatus() != jobsv1.JobRunStatus_JOB_RUN_STATUS_SUCCEEDED || s.GetCounts()["updates"] != 3 || s.GetMessage() != "3 updates false en,fr" {
		t.Fatalf("summary: %v", s)
	}
	if len(run.GetNotes()) != 1 || run.GetNotes()[0].GetRef() != "store-a" || run.GetParams()["force"] != "false" {
		t.Fatalf("run: %v", run)
	}
	job, err := r.GetJob(ctx, &sourcev1.GetJobRequest{JobId: "refresh"})
	if err != nil || job.GetJob().GetLastRun().GetId() != s.GetId() || job.GetJob().GetRunning() {
		t.Fatalf("job: %v, %v", job, err)
	}
}

func TestParamsAreChecked(t *testing.T) {
	r := newRunner(t, jobs.Job{
		ID: "check",
		Params: []*jobsv1.JobParam{
			{Name: "sample", Type: jobs.TypeInt},
			{Name: "steps", Type: jobs.TypeStringList, Choices: []string{"popular", "image"}},
			{Name: "store", Type: jobs.TypeString, Required: true},
		},
		Run: func(context.Context, *jobs.Run) error { return nil },
	})
	for _, params := range []map[string]string{
		{"store": "a", "sample": "ten"},
		{"store": "a", "steps": "popular,video"},
		{"store": "a", "nope": "1"},
		{"sample": "1"},
	} {
		_, err := r.RunJob(context.Background(), &sourcev1.RunJobRequest{JobId: "check", Params: params})
		if plugin.ErrorCode(err) != plugin.CodeInvalidArgument {
			t.Errorf("%v: got %v", params, err)
		}
	}
}

func TestOneRunAtATime(t *testing.T) {
	release := make(chan struct{})
	r := newRunner(t, jobs.Job{
		ID: "slow", Cancellable: true,
		Run: func(ctx context.Context, _ *jobs.Run) error {
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})
	ctx := context.Background()
	first, err := r.RunJob(ctx, &sourcev1.RunJobRequest{JobId: "slow"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.RunJob(ctx, &sourcev1.RunJobRequest{JobId: "slow"})
	var pe *plugin.Error
	if !errors.As(err, &pe) || pe.Code != plugin.CodeAlreadyExists || pe.Reason != plugin.ReasonAlreadyRunning || pe.CurrentRunID != first.GetRun().GetId() {
		t.Fatalf("second run: %v", err)
	}
	if _, err := r.CancelJobRun(ctx, &sourcev1.CancelJobRunRequest{RunId: first.GetRun().GetId()}); err != nil {
		t.Fatal(err)
	}
	if got := wait(t, r, first.GetRun().GetId()).GetSummary().GetStatus(); got != jobsv1.JobRunStatus_JOB_RUN_STATUS_CANCELLED {
		t.Fatalf("status %v", got)
	}
	if _, err := r.CancelJobRun(ctx, &sourcev1.CancelJobRunRequest{RunId: first.GetRun().GetId()}); plugin.ErrorCode(err) != plugin.CodeFailedPrecondition {
		t.Fatalf("cancel a finished run: %v", err)
	}
	close(release)
}

func TestNotCancellable(t *testing.T) {
	release := make(chan struct{})
	r := newRunner(t, jobs.Job{ID: "steady", Run: func(context.Context, *jobs.Run) error { <-release; return nil }})
	res, err := r.RunJob(context.Background(), &sourcev1.RunJobRequest{JobId: "steady"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.CancelJobRun(context.Background(), &sourcev1.CancelJobRunRequest{RunId: res.GetRun().GetId()}); plugin.ErrorCode(err) != plugin.CodeFailedPrecondition {
		t.Fatalf("cancel: %v", err)
	}
	close(release)
	wait(t, r, res.GetRun().GetId())
}

func TestFailuresAndSkips(t *testing.T) {
	r := newRunner(t,
		jobs.Job{ID: "broken", Run: func(context.Context, *jobs.Run) error { return errors.New("store unreachable") }},
		jobs.Job{ID: "panics", Run: func(context.Context, *jobs.Run) error { panic("boom") }},
		jobs.Job{ID: "idle", Run: func(_ context.Context, run *jobs.Run) error { run.Skip("Nothing to do"); return nil }},
	)
	for id, want := range map[string]jobsv1.JobRunStatus{
		"broken": jobsv1.JobRunStatus_JOB_RUN_STATUS_FAILED,
		"panics": jobsv1.JobRunStatus_JOB_RUN_STATUS_FAILED,
		"idle":   jobsv1.JobRunStatus_JOB_RUN_STATUS_SKIPPED,
	} {
		res, err := r.RunJob(context.Background(), &sourcev1.RunJobRequest{JobId: id})
		if err != nil {
			t.Fatal(err)
		}
		if s := wait(t, r, res.GetRun().GetId()).GetSummary(); s.GetStatus() != want || s.GetMessage() == "" {
			t.Errorf("%s: %v", id, s)
		}
	}
}

func TestScheduleAndSkipWhenBusy(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 10)
	r := newRunner(t, jobs.Job{
		ID: "tick", Every: 20 * time.Millisecond, ScheduleSource: "tick_every",
		Run: func(context.Context, *jobs.Run) error { started <- struct{}{}; <-release; return nil },
	})
	<-started
	deadline := time.Now().Add(5 * time.Second)
	for {
		res, err := r.ListJobRuns(context.Background(), &sourcev1.ListJobRunsRequest{JobId: "tick", Status: jobsv1.JobRunStatus_JOB_RUN_STATUS_SKIPPED})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.GetRuns()) > 0 {
			if res.GetRuns()[0].GetTrigger() != jobsv1.JobTrigger_JOB_TRIGGER_SCHEDULE {
				t.Fatalf("skipped run: %v", res.GetRuns()[0])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no skipped run")
		}
		time.Sleep(10 * time.Millisecond)
	}
	job, _ := r.GetJob(context.Background(), &sourcev1.GetJobRequest{JobId: "tick"})
	s := job.GetJob().GetSchedule()
	if !s.GetEnabled() || s.GetInterval().AsDuration() != 20*time.Millisecond || s.GetSource() != "tick_every" || !job.GetJob().GetRunning() {
		t.Fatalf("job: %v", job.GetJob())
	}
	close(release)
	if err := r.SetEvery("tick", 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	job, _ = r.GetJob(context.Background(), &sourcev1.GetJobRequest{JobId: "tick"})
	if job.GetJob().GetSchedule().GetEnabled() || job.GetJob().GetNextRunAt() != nil {
		t.Fatalf("schedule after SetEvery(0): %v", job.GetJob())
	}
}

func TestStopInterruptsRuns(t *testing.T) {
	r := jobs.New(jobs.Options{})
	if err := r.Add(jobs.Job{ID: "long", Run: func(ctx context.Context, _ *jobs.Run) error { <-ctx.Done(); return ctx.Err() }}); err != nil {
		t.Fatal(err)
	}
	r.Start()
	res, err := r.RunJob(context.Background(), &sourcev1.RunJobRequest{JobId: "long"})
	if err != nil {
		t.Fatal(err)
	}
	r.Stop()
	run, err := r.GetJobRun(context.Background(), &sourcev1.GetJobRunRequest{RunId: res.GetRun().GetId()})
	if err != nil || run.GetRun().GetSummary().GetStatus() != jobsv1.JobRunStatus_JOB_RUN_STATUS_FAILED || run.GetRun().GetSummary().GetMessage() != "interrupted" {
		t.Fatalf("run: %v, %v", run, err)
	}
}

func TestHistoryPagesAndLimits(t *testing.T) {
	r := jobs.New(jobs.Options{Keep: 3})
	if err := r.Add(jobs.Job{ID: "quick", Run: func(context.Context, *jobs.Run) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	r.Start()
	defer r.Stop()
	var ids []string
	for range 5 {
		res, err := r.RunJob(context.Background(), &sourcev1.RunJobRequest{JobId: "quick"})
		if err != nil {
			t.Fatal(err)
		}
		wait(t, r, res.GetRun().GetId())
		ids = append(ids, res.GetRun().GetId())
	}
	page, err := r.ListJobRuns(context.Background(), &sourcev1.ListJobRunsRequest{JobId: "quick", PageSize: 2})
	if err != nil || len(page.GetRuns()) != 2 || page.GetRuns()[0].GetId() != ids[4] || page.GetNextPageToken() == "" {
		t.Fatalf("first page: %v, %v", page, err)
	}
	next, err := r.ListJobRuns(context.Background(), &sourcev1.ListJobRunsRequest{JobId: "quick", PageSize: 2, PageToken: page.GetNextPageToken()})
	if err != nil || len(next.GetRuns()) != 1 || next.GetRuns()[0].GetId() != ids[2] || next.GetNextPageToken() != "" {
		t.Fatalf("second page: %v, %v", next, err)
	}
	if _, err := r.GetJobRun(context.Background(), &sourcev1.GetJobRunRequest{RunId: ids[0]}); plugin.ErrorCode(err) != plugin.CodeNotFound {
		t.Fatalf("a dropped run: %v", err)
	}
}

func TestDisabledJobs(t *testing.T) {
	var reason atomic.Value
	reason.Store("the search engine is off")
	ran := make(chan struct{}, 10)
	r := newRunner(t, jobs.Job{
		ID: "rebuild", Every: 20 * time.Millisecond,
		DisabledReason: func() string { return reason.Load().(string) },
		Run:            func(context.Context, *jobs.Run) error { ran <- struct{}{}; return nil },
	})
	ctx := context.Background()
	_, err := r.RunJob(ctx, &sourcev1.RunJobRequest{JobId: "rebuild"})
	var pe *plugin.Error
	if !errors.As(err, &pe) || pe.Code != plugin.CodeFailedPrecondition || pe.Reason != plugin.ReasonJobDisabled || pe.Metadata["job_id"] != "rebuild" {
		t.Fatalf("run a disabled job: %#v", err)
	}
	if _, err := r.Trigger("rebuild", nil); plugin.ErrorReason(err) != plugin.ReasonJobDisabled {
		t.Fatalf("trigger a disabled job: %v", err)
	}
	job, _ := r.GetJob(ctx, &sourcev1.GetJobRequest{JobId: "rebuild"})
	if job.GetJob().GetDisabledReason() != "the search engine is off" || job.GetJob().GetNextRunAt() != nil || !job.GetJob().GetSchedule().GetEnabled() {
		t.Fatalf("disabled job: %v", job.GetJob())
	}
	time.Sleep(80 * time.Millisecond)
	if len(ran) != 0 {
		t.Fatal("the schedule fired while disabled")
	}
	runs, _ := r.ListJobRuns(ctx, &sourcev1.ListJobRunsRequest{JobId: "rebuild"})
	if len(runs.GetRuns()) != 0 {
		t.Fatalf("runs while disabled: %v", runs)
	}

	reason.Store("")
	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		t.Fatal("the schedule didn't fire once enabled")
	}
	if job, _ := r.GetJob(ctx, &sourcev1.GetJobRequest{JobId: "rebuild"}); job.GetJob().GetDisabledReason() != "" {
		t.Fatalf("enabled job: %v", job.GetJob())
	}
}

func TestUnknownAndInvalid(t *testing.T) {
	r := newRunner(t)
	ctx := context.Background()
	if _, err := r.GetJob(ctx, &sourcev1.GetJobRequest{JobId: "nope"}); plugin.ErrorCode(err) != plugin.CodeNotFound {
		t.Errorf("GetJob: %v", err)
	}
	if _, err := r.RunJob(ctx, &sourcev1.RunJobRequest{JobId: "nope"}); plugin.ErrorCode(err) != plugin.CodeNotFound {
		t.Errorf("RunJob: %v", err)
	}
	if _, err := r.ListJobRuns(ctx, &sourcev1.ListJobRunsRequest{JobId: "nope"}); plugin.ErrorCode(err) != plugin.CodeNotFound {
		t.Errorf("ListJobRuns: %v", err)
	}
	if _, err := r.CancelJobRun(ctx, &sourcev1.CancelJobRunRequest{RunId: "nope"}); plugin.ErrorCode(err) != plugin.CodeNotFound {
		t.Errorf("CancelJobRun: %v", err)
	}
	for _, id := range []string{"Upper", "a:b", "", "-x"} {
		if err := jobs.New(jobs.Options{}).Add(jobs.Job{ID: id, Run: func(context.Context, *jobs.Run) error { return nil }}); err == nil {
			t.Errorf("id %q accepted", id)
		}
	}
}
