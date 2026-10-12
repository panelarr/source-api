// SPDX-License-Identifier: Apache-2.0

package conformance

import (
	"context"
	"fmt"
	"slices"

	jobsv1 "github.com/panelarr/source-api/gen/panelarr/jobs/v1"
	sourcev1 "github.com/panelarr/source-api/gen/panelarr/source/v1"
	"github.com/panelarr/source-api/sdk/client"
	"github.com/panelarr/source-api/sdk/jobs"
	"github.com/panelarr/source-api/sdk/plugin"
)

var paramTypes = []string{jobs.TypeBool, jobs.TypeInt, jobs.TypeString, jobs.TypeStringList}

// checkJobs reads the jobs of a plugin with the JOBS capability. It never
// runs one: runs do real work.
func checkJobs(ctx context.Context, rep *Report, c client.Client, info *sourcev1.PluginInfo) {
	if !slices.Contains(info.GetCapabilities(), sourcev1.PluginCapability_PLUGIN_CAPABILITY_JOBS) {
		if _, err := c.ListJobs(ctx, &sourcev1.ListJobsRequest{}); plugin.ErrorCode(err) != plugin.CodeUnimplemented {
			rep.warn("ListJobs: a plugin without the JOBS capability should answer UNIMPLEMENTED, got %v", err)
		}
		return
	}
	res, err := c.ListJobs(ctx, &sourcev1.ListJobsRequest{})
	if err != nil {
		rep.problem("ListJobs: %v", err)
		return
	}
	rep.Jobs = len(res.GetJobs())
	seen := map[string]bool{}
	for i, j := range res.GetJobs() {
		at := fmt.Sprintf("ListJobs: job %d (%s)", i, j.GetId())
		checkJob(rep, at, j)
		if seen[j.GetId()] {
			rep.problem("%s: id appears twice", at)
		}
		seen[j.GetId()] = true
		got, err := c.GetJob(ctx, &sourcev1.GetJobRequest{JobId: j.GetId()})
		if err != nil || got.GetJob().GetId() != j.GetId() {
			rep.problem("GetJob(%s): %v", j.GetId(), err)
		}
		runs, err := c.ListJobRuns(ctx, &sourcev1.ListJobRunsRequest{JobId: j.GetId(), PageSize: 5})
		if err != nil {
			rep.problem("ListJobRuns(%s): %v", j.GetId(), err)
			continue
		}
		for _, r := range runs.GetRuns() {
			if r.GetJobId() != j.GetId() || r.GetId() == "" || r.GetStatus() == jobsv1.JobRunStatus_JOB_RUN_STATUS_UNSPECIFIED {
				rep.problem("ListJobRuns(%s): run %q has no ID, the wrong job or no status", j.GetId(), r.GetId())
				continue
			}
			if run, err := c.GetJobRun(ctx, &sourcev1.GetJobRunRequest{RunId: r.GetId()}); err != nil || run.GetRun().GetSummary().GetId() != r.GetId() {
				rep.problem("GetJobRun(%s): %v", r.GetId(), err)
			}
		}
	}
	if _, err := c.GetJob(ctx, &sourcev1.GetJobRequest{JobId: "conformance.no-such-job"}); plugin.ErrorCode(err) != plugin.CodeNotFound {
		rep.problem("GetJob: an unknown job must be NOT_FOUND, got %v", err)
	}
	if _, err := c.RunJob(ctx, &sourcev1.RunJobRequest{JobId: "conformance.no-such-job"}); plugin.ErrorCode(err) != plugin.CodeNotFound {
		rep.problem("RunJob: an unknown job must be NOT_FOUND, got %v", err)
	}
	if _, err := c.GetJobRun(ctx, &sourcev1.GetJobRunRequest{RunId: "conformance-no-such-run"}); plugin.ErrorCode(err) != plugin.CodeNotFound {
		rep.problem("GetJobRun: an unknown run must be NOT_FOUND, got %v", err)
	}
}

func checkJob(rep *Report, at string, j *jobsv1.Job) {
	if !jobs.IDPattern.MatchString(j.GetId()) {
		rep.problem("%s: id must match %s", at, jobs.IDPattern)
	}
	if j.GetName() == "" {
		rep.problem("%s: no name", at)
	}
	if j.GetRunning() != (j.GetCurrentRunId() != "") {
		rep.problem("%s: running and current_run_id disagree", at)
	}
	for _, p := range j.GetParams() {
		if p.GetName() == "" || !slices.Contains(paramTypes, p.GetType()) {
			rep.problem("%s: param %q needs a name and a type among %v", at, p.GetName(), paramTypes)
		}
	}
}

// checkHealthChecks: SetSourceHealthCheck exists with the HEALTH_CHECKS
// capability. It changes nothing: turning checks off is the admin's call.
func checkHealthChecks(ctx context.Context, rep *Report, c client.Client, info *sourcev1.PluginInfo) {
	_, err := c.SetSourceHealthCheck(ctx, &sourcev1.SetSourceHealthCheckRequest{SourceId: "conformance-no-such-source", Enabled: true})
	if !slices.Contains(info.GetCapabilities(), sourcev1.PluginCapability_PLUGIN_CAPABILITY_HEALTH_CHECKS) {
		if plugin.ErrorCode(err) != plugin.CodeUnimplemented {
			rep.warn("SetSourceHealthCheck: a plugin without the HEALTH_CHECKS capability should answer UNIMPLEMENTED, got %v", err)
		}
		return
	}
	if plugin.ErrorCode(err) != plugin.CodeNotFound {
		rep.problem("SetSourceHealthCheck: an unknown source must be NOT_FOUND, got %v", err)
	}
}
