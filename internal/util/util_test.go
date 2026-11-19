package util

import "testing"

// Slot 1 must keep the pre-multi-runner identity, or upgrading orphans every
// existing runner directory, service and GitHub registration.
func TestSlotOneKeepsLegacyIdentity(t *testing.T) {
	const repo = "ssukru/local-worker"
	legacyID := RepoID(repo)

	if got := InstanceID(repo, 1); got != legacyID {
		t.Errorf("InstanceID slot 1 = %q, want legacy id %q", got, legacyID)
	}
	if got := InstanceID(repo, 0); got != legacyID {
		t.Errorf("InstanceID slot 0 = %q, want legacy id %q", got, legacyID)
	}
	if got, want := RunnerLabel(repo, 1), "com.local-worker.runner."+legacyID; got != want {
		t.Errorf("RunnerLabel slot 1 = %q, want %q", got, want)
	}
	if got := SlotName("mac-mini-ssukru__local-worker", 1); got != "mac-mini-ssukru__local-worker" {
		t.Errorf("SlotName slot 1 = %q, want the base name unchanged", got)
	}
}

func TestSlotsAreDistinct(t *testing.T) {
	const repo = "ssukru/local-worker"
	seen := map[string]int{}
	for slot := 1; slot <= 5; slot++ {
		id := InstanceID(repo, slot)
		if prev, dup := seen[id]; dup {
			t.Fatalf("slot %d and slot %d share instance id %q", prev, slot, id)
		}
		seen[id] = slot
	}
	if got, want := InstanceID(repo, 3), RepoID(repo)+"-3"; got != want {
		t.Errorf("InstanceID slot 3 = %q, want %q", got, want)
	}
	if got, want := SlotName("host-repo", 3), "host-repo-3"; got != want {
		t.Errorf("SlotName slot 3 = %q, want %q", got, want)
	}
}

func TestClampSlots(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, 1}, // config written before multi-runner support
		{-4, 1},
		{1, 1},
		{7, 7},
		{MaxSlots, MaxSlots},
		{MaxSlots + 1, MaxSlots},
	}
	for _, c := range cases {
		if got := ClampSlots(c.in); got != c.want {
			t.Errorf("ClampSlots(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}
