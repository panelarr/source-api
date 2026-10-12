// SPDX-License-Identifier: Apache-2.0

// Package jobs runs a plugin's background jobs and answers the job calls of
// the JOBS capability (ListJobs, GetJob, ListJobRuns, GetJobRun, RunJob,
// CancelJobRun) with the shared panelarr.jobs.v1 model.
//
// Add jobs to a [Runner], call [Runner.Start] to begin their schedules, and
// forward the plugin's job calls to the runner's methods of the same names.
// History is kept in memory: a plugin that restarts starts with none.
package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	jobsv1 "github.com/panelarr/source-api/gen/panelarr/jobs/v1"
	sourcev1 "github.com/panelarr/source-api/gen/panelarr/source/v1"
	"github.com/panelarr/source-api/sdk/plugin"
)

// IDPattern is what job IDs look like: no ':', which HTTP paths read as a
// custom verb (as in ":run").
var IDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// Param types of [jobsv1.JobParam].
const (
	TypeBool       = "bool"
	TypeInt        = "int"
	TypeString     = "string"
	TypeStringList = "string_list"
)

// Note levels.
const (
	LevelInfo  = "info"
	LevelWarn  = "warn"
	LevelError = "error"
)

const (
	defaultKeep     = 100
	maxNotes        = 200
	defaultPageSize = 20
	maxPageSize     = 100
)

// Job is a piece of background work.
type Job struct {
	// ID matches [IDPattern].
	ID          string
	Name        string
	Description string
	// Group is a label to group jobs by, such as "Stores".
	Group string
	// Every is how often the job runs on its own; zero means only on request.
	// [Runner.SetEvery] changes it.
	Every time.Duration
	// ScheduleSource says where the schedule is configured, e.g. a config key.
	ScheduleSource string
	// AtStart runs the job once when the runner starts.
	AtStart bool
	// Params are what a run accepts. Scheduled runs use their defaults.
	Params []*jobsv1.JobParam
	// Cancellable jobs stop when their run's context is cancelled.
	Cancellable bool
	// DisabledReason, when set, says why the job can't run now; "" means it
	// can. It's asked every time the job is shown or due, so it follows
	// changes. While it gives a reason, starting the job fails with
	// JOB_DISABLED and the schedule doesn't fire. It must not call the
	// runner.
	DisabledReason func() string
	// Run does the work. Returning an error fails the run with its message;
	// see [Run] for counts, notes, progress and skipping.
	Run func(ctx context.Context, run *Run) error
}

// Options tune a [Runner].
type Options struct {
	// Keep is how many runs are kept per job, 100 by default.
	Keep int
	// RunIDPrefix starts every run ID, so a plugin that also passes on another
	// service's runs can tell them apart.
	RunIDPrefix string
	// Now is the clock, time.Now by default.
	Now func() time.Time
}

// Runner keeps jobs, runs them on request and on schedule, and remembers
// their runs. It is safe for concurrent use.
type Runner struct {
	opts   Options
	ctx    context.Context //nolint:containedctx // the runs' parent, cancelled by Stop
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	order   []string
	jobs    map[string]*entry
	runs    map[string]*Run
	started bool
}

// disabled is why e can't run now, or "".
func (e *entry) disabled() string {
	if e.job.DisabledReason == nil {
		return ""
	}
	return e.job.DisabledReason()
}

type entry struct {
	job     Job
	every   time.Duration
	next    time.Time
	current *Run
	history []*Run // oldest first
	wake    chan struct{}
}

// New returns a runner without jobs.
func New(opts Options) *Runner {
	if opts.Keep <= 0 {
		opts.Keep = defaultKeep
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Runner{opts: opts, ctx: ctx, cancel: cancel, jobs: map[string]*entry{}, runs: map[string]*Run{}}
}

// Add adds a job. Add jobs before Start.
func (r *Runner) Add(j Job) error {
	if !IDPattern.MatchString(j.ID) {
		return fmt.Errorf("jobs: id %q must match %s", j.ID, IDPattern)
	}
	if j.Run == nil {
		return fmt.Errorf("jobs: %s has no Run", j.ID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.jobs[j.ID] != nil {
		return fmt.Errorf("jobs: %s added twice", j.ID)
	}
	r.jobs[j.ID] = &entry{job: j, every: j.Every, wake: make(chan struct{}, 1)}
	r.order = append(r.order, j.ID)
	return nil
}

// Start begins the schedules and the AtStart runs.
func (r *Runner) Start() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started {
		return
	}
	r.started = true
	for _, id := range r.order {
		e := r.jobs[id]
		if e.job.AtStart && e.disabled() == "" {
			r.startLocked(e, jobsv1.JobTrigger_JOB_TRIGGER_STARTUP, r.defaults(e), "")
		}
		r.wg.Add(1)
		go r.schedule(e)
	}
}

// Stop cancels the runs going on and the schedules, and waits for them. A
// run cut short this way fails with "interrupted".
func (r *Runner) Stop() {
	r.cancel()
	r.wg.Wait()
}

// SetEvery changes how often a job runs on its own; zero stops its schedule.
func (r *Runner) SetEvery(id string, every time.Duration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.jobs[id]
	if e == nil {
		return plugin.ErrNotFound("no job %q", id)
	}
	if e.every == every {
		return nil
	}
	e.every = every
	select {
	case e.wake <- struct{}{}:
	default:
	}
	return nil
}

// Trigger starts a run the plugin decided on itself (JOB_TRIGGER_SYSTEM).
// Missing params get their defaults.
func (r *Runner) Trigger(id string, params map[string]string) (*jobsv1.JobRunSummary, error) {
	return r.start(id, jobsv1.JobTrigger_JOB_TRIGGER_SYSTEM, params, "")
}

// Wait waits for a run to finish and returns it.
func (r *Runner) Wait(ctx context.Context, runID string) (*jobsv1.JobRun, error) {
	r.mu.Lock()
	run := r.runs[runID]
	r.mu.Unlock()
	if run == nil {
		return nil, plugin.ErrNotFound("no job run %q", runID)
	}
	select {
	case <-run.done:
		return run.view(), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (r *Runner) schedule(e *entry) {
	defer r.wg.Done()
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	for {
		r.mu.Lock()
		every := e.every
		if every > 0 {
			e.next = r.opts.Now().Add(every)
		} else {
			e.next = time.Time{}
		}
		r.mu.Unlock()
		var tick <-chan time.Time
		if every > 0 {
			timer.Reset(every)
			tick = timer.C
		}
		select {
		case <-r.ctx.Done():
			return
		case <-e.wake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-tick:
			r.mu.Lock()
			if e.disabled() == "" {
				r.startLocked(e, jobsv1.JobTrigger_JOB_TRIGGER_SCHEDULE, r.defaults(e), "")
			}
			r.mu.Unlock()
		}
	}
}

func (r *Runner) start(id string, trigger jobsv1.JobTrigger, params map[string]string, by string) (*jobsv1.JobRunSummary, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.jobs[id]
	if e == nil {
		return nil, plugin.ErrNotFound("no job %q", id)
	}
	if reason := e.disabled(); reason != "" {
		return nil, plugin.ErrJobDisabled(id, reason)
	}
	checked, err := checkParams(e.job.Params, params)
	if err != nil {
		return nil, err
	}
	if e.current != nil {
		return nil, plugin.ErrAlreadyRunning(e.current.id, "job %s is already running", id)
	}
	return r.startLocked(e, trigger, checked, by).summaryView(), nil
}

// defaults are the params a scheduled run uses.
func (r *Runner) defaults(e *entry) map[string]string {
	out := map[string]string{}
	for _, p := range e.job.Params {
		if p.GetDefault() != "" {
			out[p.GetName()] = p.GetDefault()
		}
	}
	return out
}

// startLocked starts a run of e, or records a skipped one when a scheduled
// run finds e busy. r.mu is held.
func (r *Runner) startLocked(e *entry, trigger jobsv1.JobTrigger, params map[string]string, by string) *Run {
	run := &Run{
		id:     r.opts.RunIDPrefix + newRunID(r.opts.Now()),
		params: params,
		now:    r.opts.Now,
		done:   make(chan struct{}),
		summary: &jobsv1.JobRunSummary{
			JobId:       e.job.ID,
			Trigger:     trigger,
			StartedAt:   timestamppb.New(r.opts.Now()),
			TriggeredBy: by,
			Status:      jobsv1.JobRunStatus_JOB_RUN_STATUS_RUNNING,
		},
	}
	run.summary.Id = run.id
	r.remember(e, run)
	if e.current != nil {
		run.summary.Status = jobsv1.JobRunStatus_JOB_RUN_STATUS_SKIPPED
		run.summary.Message = "Still running from before"
		run.summary.FinishedAt = run.summary.GetStartedAt()
		close(run.done)
		return run
	}
	e.current = run
	ctx, cancel := context.WithCancel(r.ctx)
	run.cancel = cancel
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer cancel()
		err := call(ctx, e.job.Run, run)
		r.mu.Lock()
		run.finish(err, r.ctx.Err() != nil)
		e.current = nil
		r.mu.Unlock()
		close(run.done)
	}()
	return run
}

func call(ctx context.Context, f func(context.Context, *Run) error, run *Run) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v", p)
		}
	}()
	return f(ctx, run)
}

func (r *Runner) remember(e *entry, run *Run) {
	e.history = append(e.history, run)
	r.runs[run.id] = run
	for len(e.history) > r.opts.Keep {
		// The oldest run goes, unless it is still running.
		i := 0
		if e.history[0] == e.current {
			i = 1
		}
		delete(r.runs, e.history[i].id)
		e.history = slices.Delete(e.history, i, i+1)
	}
}

func newRunID(now time.Time) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return strconv.FormatInt(now.UnixMilli(), 16) + hex.EncodeToString(b)
}

// checkParams validates a run's params against the job's and fills in the
// defaults.
func checkParams(specs []*jobsv1.JobParam, given map[string]string) (map[string]string, error) {
	byName := map[string]*jobsv1.JobParam{}
	for _, p := range specs {
		byName[p.GetName()] = p
	}
	out := map[string]string{}
	for name, value := range given {
		p := byName[name]
		if p == nil {
			return nil, plugin.ErrInvalidArgument("unknown param %q", name)
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		v, err := checkValue(p, value)
		if err != nil {
			return nil, err
		}
		out[name] = v
	}
	for _, p := range specs {
		if _, ok := out[p.GetName()]; !ok && p.GetDefault() != "" {
			out[p.GetName()] = p.GetDefault()
		}
		if _, ok := out[p.GetName()]; !ok && p.GetRequired() {
			return nil, plugin.ErrInvalidArgument("param %q is required", p.GetName())
		}
	}
	return out, nil
}

func checkValue(p *jobsv1.JobParam, value string) (string, error) {
	switch p.GetType() {
	case TypeBool:
		b, err := strconv.ParseBool(value)
		if err != nil {
			return "", plugin.ErrInvalidArgument("param %q must be true or false", p.GetName())
		}
		return strconv.FormatBool(b), nil
	case TypeInt:
		if _, err := strconv.ParseInt(value, 10, 64); err != nil {
			return "", plugin.ErrInvalidArgument("param %q must be a whole number", p.GetName())
		}
	case TypeStringList:
		var items []string
		for _, item := range strings.Split(value, ",") {
			if item = strings.TrimSpace(item); item != "" {
				if len(p.GetChoices()) > 0 && !slices.Contains(p.GetChoices(), item) {
					return "", plugin.ErrInvalidArgument("param %q: %q isn't one of %s", p.GetName(), item, strings.Join(p.GetChoices(), ", "))
				}
				items = append(items, item)
			}
		}
		return strings.Join(items, ","), nil
	}
	if len(p.GetChoices()) > 0 && !slices.Contains(p.GetChoices(), value) {
		return "", plugin.ErrInvalidArgument("param %q: %q isn't one of %s", p.GetName(), value, strings.Join(p.GetChoices(), ", "))
	}
	return value, nil
}

// ListJobs answers the ListJobs call.
func (r *Runner) ListJobs(context.Context, *sourcev1.ListJobsRequest) (*sourcev1.ListJobsResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	res := &sourcev1.ListJobsResponse{}
	for _, id := range r.order {
		res.Jobs = append(res.Jobs, r.jobView(r.jobs[id]))
	}
	return res, nil
}

// GetJob answers the GetJob call.
func (r *Runner) GetJob(_ context.Context, req *sourcev1.GetJobRequest) (*sourcev1.GetJobResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.jobs[req.GetJobId()]
	if e == nil {
		return nil, plugin.ErrNotFound("no job %q", req.GetJobId())
	}
	return &sourcev1.GetJobResponse{Job: r.jobView(e)}, nil
}

func (r *Runner) jobView(e *entry) *jobsv1.Job {
	j := &jobsv1.Job{
		Id:          e.job.ID,
		Name:        e.job.Name,
		Description: e.job.Description,
		Group:       e.job.Group,
		Cancellable: e.job.Cancellable,
		Schedule:    &jobsv1.Schedule{Source: e.job.ScheduleSource},
	}
	for _, p := range e.job.Params {
		j.Params = append(j.Params, proto.CloneOf(p))
	}
	j.DisabledReason = e.disabled()
	if e.every > 0 {
		j.Schedule.Kind = &jobsv1.Schedule_Interval{Interval: durationpb.New(e.every)}
		j.Schedule.Enabled = true
		if !e.next.IsZero() && j.GetDisabledReason() == "" {
			j.NextRunAt = timestamppb.New(e.next)
		}
	}
	if e.current != nil {
		j.Running = true
		j.CurrentRunId = e.current.id
	}
	if n := len(e.history); n > 0 {
		j.LastRun = e.history[n-1].summaryView()
	}
	return j
}

// ListJobRuns answers the ListJobRuns call: newest first, a page at a time.
func (r *Runner) ListJobRuns(_ context.Context, req *sourcev1.ListJobRunsRequest) (*sourcev1.ListJobRunsResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.jobs[req.GetJobId()]
	if e == nil {
		return nil, plugin.ErrNotFound("no job %q", req.GetJobId())
	}
	size := int(req.GetPageSize())
	if size <= 0 {
		size = defaultPageSize
	}
	size = min(size, maxPageSize)
	offset := 0
	if t := req.GetPageToken(); t != "" {
		n, err := strconv.Atoi(t)
		if err != nil || n < 0 {
			return nil, plugin.ErrInvalidArgument("bad page_token")
		}
		offset = n
	}
	var matching []*Run
	for i := len(e.history) - 1; i >= 0; i-- {
		run := e.history[i]
		if req.GetStatus() == jobsv1.JobRunStatus_JOB_RUN_STATUS_UNSPECIFIED || run.status() == req.GetStatus() {
			matching = append(matching, run)
		}
	}
	res := &sourcev1.ListJobRunsResponse{}
	for i := offset; i < len(matching) && i < offset+size; i++ {
		res.Runs = append(res.Runs, matching[i].summaryView())
	}
	if offset+size < len(matching) {
		res.NextPageToken = strconv.Itoa(offset + size)
	}
	return res, nil
}

// GetJobRun answers the GetJobRun call.
func (r *Runner) GetJobRun(_ context.Context, req *sourcev1.GetJobRunRequest) (*sourcev1.GetJobRunResponse, error) {
	r.mu.Lock()
	run := r.runs[req.GetRunId()]
	r.mu.Unlock()
	if run == nil {
		return nil, plugin.ErrNotFound("no job run %q", req.GetRunId())
	}
	return &sourcev1.GetJobRunResponse{Run: run.view()}, nil
}

// RunJob answers the RunJob call: it starts a run and returns at once.
func (r *Runner) RunJob(_ context.Context, req *sourcev1.RunJobRequest) (*sourcev1.RunJobResponse, error) {
	by := req.GetOnBehalfOf()
	if by == "" {
		by = "host"
	}
	run, err := r.start(req.GetJobId(), jobsv1.JobTrigger_JOB_TRIGGER_MANUAL, req.GetParams(), by)
	if err != nil {
		return nil, err
	}
	return &sourcev1.RunJobResponse{Run: run}, nil
}

// CancelJobRun answers the CancelJobRun call. The run stops when its job
// notices; the answer shows it as it is at that moment.
func (r *Runner) CancelJobRun(_ context.Context, req *sourcev1.CancelJobRunRequest) (*sourcev1.CancelJobRunResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	run := r.runs[req.GetRunId()]
	if run == nil {
		return nil, plugin.ErrNotFound("no job run %q", req.GetRunId())
	}
	e := r.jobs[run.summary.GetJobId()]
	if e.current != run {
		return nil, plugin.Errorf(plugin.CodeFailedPrecondition, "run %s isn't running", run.id)
	}
	if !e.job.Cancellable {
		return nil, plugin.Errorf(plugin.CodeFailedPrecondition, "job %s can't be cancelled", e.job.ID)
	}
	run.markCancelled()
	return &sourcev1.CancelJobRunResponse{Run: run.summaryView()}, nil
}

// Run is one run of a job, handed to [Job.Run] to report what it does.
type Run struct {
	id     string
	params map[string]string
	now    func() time.Time
	cancel context.CancelFunc
	done   chan struct{}

	mu        sync.Mutex
	summary   *jobsv1.JobRunSummary
	notes     []*jobsv1.JobRunNote
	progress  *jobsv1.JobProgress
	cancelled bool
	skipped   bool
}

// ID is the run's ID.
func (r *Run) ID() string { return r.id }

// Trigger is what started the run.
func (r *Run) Trigger() jobsv1.JobTrigger { return r.summary.GetTrigger() }

// Param returns a param's value, or its default, or "".
func (r *Run) Param(name string) string { return r.params[name] }

// List returns a string_list param's items.
func (r *Run) List(name string) []string {
	v := r.params[name]
	if v == "" {
		return nil
	}
	return strings.Split(v, ",")
}

// Note records a warning, error or notable event (at most 200 a run). ref
// says what it is about, such as a source ID; it may be empty.
func (r *Run) Note(level, message, ref string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.notes) < maxNotes {
		r.notes = append(r.notes, &jobsv1.JobRunNote{At: timestamppb.New(r.now()), Level: level, Message: message, Ref: ref})
	}
}

// Count sets a count of what the run did, such as "updates".
func (r *Run) Count(name string, n int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.summary.Counts == nil {
		r.summary.Counts = map[string]int64{}
	}
	r.summary.Counts[name] = n
}

// Add adds delta to a count.
func (r *Run) Add(name string, delta int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.summary.Counts == nil {
		r.summary.Counts = map[string]int64{}
	}
	r.summary.Counts[name] += delta
}

// Progress reports how far the run is, e.g. ("Checking", 3, 10, "sources").
func (r *Run) Progress(phase string, done, total int64, unit string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.progress = &jobsv1.JobProgress{Phase: phase, Done: done, Total: total, Unit: unit}
}

// SetMessage sets the run's one-line outcome.
func (r *Run) SetMessage(message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.summary.Message = message
}

// Skip marks the run as having nothing to do; return nil after it.
func (r *Run) Skip(message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.skipped = true
	r.summary.Message = message
}

func (r *Run) markCancelled() {
	r.mu.Lock()
	r.cancelled = true
	r.mu.Unlock()
	r.cancel()
}

func (r *Run) finish(err error, stopping bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.summary
	s.FinishedAt = timestamppb.New(r.now())
	r.progress = nil
	switch {
	case r.cancelled:
		s.Status = jobsv1.JobRunStatus_JOB_RUN_STATUS_CANCELLED
		if s.GetMessage() == "" {
			s.Message = "Cancelled"
		}
	case err != nil && stopping:
		s.Status = jobsv1.JobRunStatus_JOB_RUN_STATUS_FAILED
		s.Message = "interrupted"
	case err != nil:
		s.Status = jobsv1.JobRunStatus_JOB_RUN_STATUS_FAILED
		s.Message = err.Error()
	case r.skipped:
		s.Status = jobsv1.JobRunStatus_JOB_RUN_STATUS_SKIPPED
	default:
		s.Status = jobsv1.JobRunStatus_JOB_RUN_STATUS_SUCCEEDED
	}
}

func (r *Run) status() jobsv1.JobRunStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.summary.GetStatus()
}

func (r *Run) summaryView() *jobsv1.JobRunSummary {
	r.mu.Lock()
	defer r.mu.Unlock()
	return proto.CloneOf(r.summary)
}

func (r *Run) view() *jobsv1.JobRun {
	r.mu.Lock()
	defer r.mu.Unlock()
	v := &jobsv1.JobRun{Summary: proto.CloneOf(r.summary), Params: map[string]string{}}
	for k, val := range r.params {
		v.Params[k] = val
	}
	for _, n := range r.notes {
		v.Notes = append(v.Notes, proto.CloneOf(n))
	}
	if r.progress != nil {
		v.Progress = proto.CloneOf(r.progress)
	}
	return v
}
