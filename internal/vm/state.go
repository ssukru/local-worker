package vm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/ssukru/local-worker/internal/util"
)

// StateDir holds per-environment state files, shared by the VM and container
// backends.
func StateDir() string { return filepath.Join(util.Root(), "vm") }

// State tracks an isolated environment's lifecycle on disk.
type State struct {
	Image             string `json:"image"`
	CreatedAt         string `json:"createdAt"`
	JobsRun           int    `json:"jobsRun"`
	RecreateRequested bool   `json:"recreateRequested,omitempty"`
}

func statePath(name string) string {
	return filepath.Join(StateDir(), name+".json")
}

// ReadState returns nil when there is no (readable) state file.
func ReadState(name string) *State {
	raw, err := os.ReadFile(statePath(name))
	if err != nil {
		return nil
	}
	var s State
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil
	}
	return &s
}

func WriteState(name string, s *State) error {
	if err := os.MkdirAll(StateDir(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(statePath(name), append(data, '\n'), 0o644)
}

func ClearState(name string) {
	os.Remove(statePath(name))
}

var refreshRe = regexp.MustCompile(`^(\d+(?:\.\d+)?)h$`)

// ParseRefreshHours returns the hour count for interval policies like "24h",
// or -1 when the policy is not an interval.
func ParseRefreshHours(policy string) float64 {
	m := refreshRe.FindStringSubmatch(policy)
	if m == nil {
		return -1
	}
	h, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return -1
	}
	return h
}

func ValidRefresh(policy string) bool {
	return policy == "never" || policy == "per-job" || ParseRefreshHours(policy) >= 0
}

// refreshDue reports whether a scheduled rebuild is due under an interval
// policy ("never" and "per-job" never trigger here).
func refreshDue(policy string, state *State) bool {
	if state == nil {
		return false
	}
	hours := ParseRefreshHours(policy)
	if hours < 0 {
		return false
	}
	created, err := time.Parse(time.RFC3339, state.CreatedAt)
	if err != nil {
		return false
	}
	return time.Since(created) > time.Duration(hours*float64(time.Hour))
}

func NewState(image string) *State {
	return &State{Image: image, CreatedAt: util.Timestamp(), JobsRun: 0}
}
