// Package pool implements demand-driven autoscaling: instead of a fixed set
// of runners, a per-repo daemon watches the repo's job queue and keeps one
// ephemeral runner per queued job (between min and max). Runners deregister
// themselves after a single job, so the pool shrinks on its own — the same
// model GitHub-hosted runners present to the user.
package pool

import (
	"fmt"
	"strings"

	"github.com/ssukru/local-worker/internal/util"
)

// Demand is what the repo currently wants from this machine.
type Demand struct {
	Queued  int // jobs waiting for a runner whose labels we satisfy
	Running int // jobs already executing on our runners
}

// EffectiveLabels is the label set GitHub sees for our runners: the three it
// assigns automatically plus whatever the user configured. Lowercased,
// because runs-on matching is case-insensitive.
func EffectiveLabels(configured []string) map[string]bool {
	arch := "x64"
	if util.MachineArch() == "arm64" {
		arch = "arm64"
	}
	set := map[string]bool{
		"self-hosted":                   true,
		strings.ToLower(util.OsLabel()): true,
		arch:                            true,
	}
	for _, l := range configured {
		if l = strings.TrimSpace(l); l != "" {
			set[strings.ToLower(l)] = true
		}
	}
	return set
}

// JobMatches applies GitHub's runs-on rule: a job can land on a runner only
// if the runner carries *every* label the job asked for. Extra runner labels
// are fine; a missing one disqualifies it.
//
// This is what keeps us from scaling up for jobs that were never ours —
// `runs-on: ubuntu-latest` asks for a label we don't have, so it doesn't
// count as demand.
func JobMatches(jobLabels []string, ours map[string]bool) bool {
	if len(jobLabels) == 0 {
		return false
	}
	for _, l := range jobLabels {
		if !ours[strings.ToLower(strings.TrimSpace(l))] {
			return false
		}
	}
	return true
}

type apiJob struct {
	Status     string   `json:"status"`
	Labels     []string `json:"labels"`
	RunnerName string   `json:"runner_name"`
}

// CountJobs reduces a run's jobs to demand. Split out from the API call so
// the counting rules are testable without network.
func CountJobs(jobs []apiJob, ours map[string]bool, mine func(runnerName string) bool) Demand {
	var d Demand
	for _, j := range jobs {
		switch j.Status {
		case "queued":
			// A queued job needs a runner we don't have yet.
			if JobMatches(j.Labels, ours) {
				d.Queued++
			}
		case "in_progress":
			// Already running on one of ours: that runner is occupied, so it
			// must stay counted or we'd scale down out from under a live job.
			if mine(j.RunnerName) {
				d.Running++
			}
		}
	}
	return d
}

// QueryDemand asks GitHub what this repo needs right now.
//
// There is no repo-wide "list queued jobs" endpoint, so this lists recent
// runs and drills into the ones still active. Completed runs are skipped
// without a second call, which keeps a poll down to ~1-3 API requests.
func QueryDemand(repo string, ours map[string]bool, mine func(string) bool) (Demand, error) {
	var runs struct {
		WorkflowRuns []struct {
			ID     int64  `json:"id"`
			Status string `json:"status"`
		} `json:"workflow_runs"`
	}
	if err := util.GhJSON(&runs, "repos/"+repo+"/actions/runs?per_page=50"); err != nil {
		return Demand{}, err
	}

	var total Demand
	for _, run := range runs.WorkflowRuns {
		// "waiting" runs are blocked on a deployment approval, not on a
		// runner — spawning capacity for them would just burn idle runners.
		if run.Status != "queued" && run.Status != "in_progress" {
			continue
		}
		var jobs struct {
			Jobs []apiJob `json:"jobs"`
		}
		if err := util.GhJSON(&jobs, fmt.Sprintf("repos/%s/actions/runs/%d/jobs?per_page=100", repo, run.ID)); err != nil {
			// One unreadable run shouldn't zero out the whole demand signal.
			continue
		}
		d := CountJobs(jobs.Jobs, ours, mine)
		total.Queued += d.Queued
		total.Running += d.Running
	}
	return total, nil
}
