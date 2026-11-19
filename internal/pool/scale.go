package pool

// Desired is the number of runners the pool should have live, given current
// demand and the configured bounds.
//
// Queued + Running is the key: a running job occupies a runner that must not
// be reclaimed, and every queued job wants one more. Summing them means the
// target holds steady as jobs move from queued to running, instead of
// oscillating.
func Desired(d Demand, min, max int) int {
	if min < 0 {
		min = 0
	}
	if max < min {
		max = min
	}
	want := d.Queued + d.Running
	if want < min {
		want = min
	}
	if want > max {
		want = max
	}
	return want
}
