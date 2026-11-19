package pool

import "testing"

func TestJobMatchesFollowsRunsOnRules(t *testing.T) {
	ours := map[string]bool{
		"self-hosted": true, "macos": true, "arm64": true, "local-worker": true,
	}
	cases := []struct {
		name string
		job  []string
		want bool
	}{
		{"bare self-hosted", []string{"self-hosted"}, true},
		{"subset of our labels", []string{"self-hosted", "macOS"}, true},
		{"case-insensitive", []string{"SELF-HOSTED", "ARM64"}, true},
		{"custom label we carry", []string{"self-hosted", "local-worker"}, true},
		// The important negative: a hosted-runner job must never register as
		// demand, or the pool spins up runners for jobs that will never come.
		{"hosted runner", []string{"ubuntu-latest"}, false},
		{"hosted macos", []string{"macos-14"}, false},
		{"one label we lack", []string{"self-hosted", "gpu"}, false},
		{"right arch, wrong os", []string{"self-hosted", "linux"}, false},
		{"no labels at all", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := JobMatches(c.job, ours); got != c.want {
				t.Errorf("JobMatches(%v) = %v, want %v", c.job, got, c.want)
			}
		})
	}
}

func TestCountJobsSeparatesQueuedFromOurRunning(t *testing.T) {
	ours := map[string]bool{"self-hosted": true, "macos": true, "arm64": true}
	mine := func(name string) bool { return name == "mac-1" || name == "mac-2" }

	jobs := []apiJob{
		{Status: "queued", Labels: []string{"self-hosted"}},
		{Status: "queued", Labels: []string{"self-hosted"}},
		{Status: "queued", Labels: []string{"ubuntu-latest"}}, // not ours
		{Status: "in_progress", Labels: []string{"self-hosted"}, RunnerName: "mac-1"},
		{Status: "in_progress", Labels: []string{"self-hosted"}, RunnerName: "someone-else"},
		{Status: "completed", Labels: []string{"self-hosted"}, RunnerName: "mac-2"},
	}
	got := CountJobs(jobs, ours, mine)
	if got.Queued != 2 {
		t.Errorf("Queued = %d, want 2", got.Queued)
	}
	if got.Running != 1 {
		t.Errorf("Running = %d, want 1 (only jobs on our runners)", got.Running)
	}
}

func TestDesired(t *testing.T) {
	cases := []struct {
		name     string
		d        Demand
		min, max int
		want     int
	}{
		{"idle pool holds the warm floor", Demand{}, 1, 5, 1},
		{"cold pool stays at zero", Demand{}, 0, 5, 0},
		{"six queued, ceiling of five", Demand{Queued: 6}, 1, 5, 5},
		{"six queued, ceiling of eight", Demand{Queued: 6}, 1, 8, 6},
		// The steady-state property: as jobs move queued -> running the
		// target must not dip, or the pool would retire runners mid-burst.
		{"burst starting", Demand{Queued: 6, Running: 0}, 1, 8, 6},
		{"burst half started", Demand{Queued: 3, Running: 3}, 1, 8, 6},
		{"burst fully started", Demand{Queued: 0, Running: 6}, 1, 8, 6},
		{"burst draining", Demand{Queued: 0, Running: 2}, 1, 8, 2},
		{"burst done", Demand{}, 1, 8, 1},
		{"running alone still counts", Demand{Running: 4}, 0, 8, 4},
		{"max below min is corrected", Demand{Queued: 9}, 3, 1, 3},
		{"negative min is clamped", Demand{}, -2, 4, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Desired(c.d, c.min, c.max); got != c.want {
				t.Errorf("Desired(%+v, min=%d, max=%d) = %d, want %d",
					c.d, c.min, c.max, got, c.want)
			}
		})
	}
}

// Busy tracking is what makes scale-down safe — a misread here would let the
// pool kill a runner mid-job.
func TestParseJobEvent(t *testing.T) {
	cases := []struct {
		line              string
		started, finished bool
	}{
		{"2026-07-21 10:00:00Z: Running job: build", true, false},
		{"2026-07-21 10:05:00Z: Job build completed with result: Succeeded", false, true},
		{"2026-07-21 10:05:00Z: Job test completed with result: Failed", false, true},
		{"√ Connected to GitHub", false, false},
		{"Listening for Jobs", false, false},
		{"", false, false},
	}
	for _, c := range cases {
		started, finished := parseJobEvent(c.line)
		if started != c.started || finished != c.finished {
			t.Errorf("parseJobEvent(%q) = (%v, %v), want (%v, %v)",
				c.line, started, finished, c.started, c.finished)
		}
	}
}

func TestEffectiveLabelsIncludesAutoAssigned(t *testing.T) {
	set := EffectiveLabels([]string{"local-worker", "  spaced  "})
	for _, want := range []string{"self-hosted", "local-worker", "spaced"} {
		if !set[want] {
			t.Errorf("expected label %q in %v", want, set)
		}
	}
	// GitHub assigns an OS and an arch label to every runner; demand matching
	// depends on us mirroring them.
	if len(set) < 5 {
		t.Errorf("expected os/arch labels to be included, got %v", set)
	}
}
