package runner

import "testing"

// Slot 1's directory is a string prefix of every other slot's, so matching
// must be path-component aware — otherwise slot 1 looks alive whenever any
// slot of the repo is, and the watchdog never heals it.
func TestListenerMatchesIsNotPrefixConfused(t *testing.T) {
	slot1 := "/Users/me/.local-worker/runners/owner__repo"
	slot2 := slot1 + "-2"

	cases := []struct {
		name   string
		psLine string
		dir    string
		want   bool
	}{
		{"slot 2 process is not slot 1", "4821 /bin/bash " + slot2 + "/run.sh", slot1, false},
		{"slot 2 process is slot 2", "4821 /bin/bash " + slot2 + "/run.sh", slot2, true},
		{"slot 1 process is slot 1", "4820 /bin/bash " + slot1 + "/run.sh", slot1, true},
		{"slot 1 process is not slot 2", "4820 /bin/bash " + slot1 + "/run.sh", slot2, false},
		{"bare dir as argument", "4820 " + slot1, slot1, true},
		{"unrelated repo", "4822 /bin/bash /Users/me/.local-worker/runners/other__repo/run.sh", slot1, false},
		{"empty line", "", slot1, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := listenerMatches(c.psLine, c.dir); got != c.want {
				t.Errorf("listenerMatches(%q, %q) = %v, want %v", c.psLine, c.dir, got, c.want)
			}
		})
	}
}
