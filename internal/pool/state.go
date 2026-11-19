package pool

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/ssukru/local-worker/internal/util"
)

// StateDir holds one file per autoscaled repo. The daemon owns the pool's
// real state in memory; this file is how `lw status` sees it without needing
// to talk to the daemon.
func StateDir() string { return filepath.Join(util.Root(), "pool") }

// State is the daemon's last observed snapshot.
type State struct {
	Min       int    `json:"min"`
	Max       int    `json:"max"`
	Desired   int    `json:"desired"`
	Live      int    `json:"live"`
	Busy      int    `json:"busy"`
	Queued    int    `json:"queued"`
	UpdatedAt string `json:"updatedAt"`
	LastError string `json:"lastError,omitempty"`
}

func statePath(repo string) string {
	return filepath.Join(StateDir(), util.RepoID(repo)+".json")
}

// ReadState returns nil when the daemon has never reported (or the file is
// unreadable) — callers should treat that as "unknown", not "empty pool".
func ReadState(repo string) *State {
	raw, err := os.ReadFile(statePath(repo))
	if err != nil {
		return nil
	}
	var s State
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil
	}
	return &s
}

func WriteState(repo string, s *State) error {
	if err := os.MkdirAll(StateDir(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(statePath(repo), append(data, '\n'), 0o644)
}

func ClearState(repo string) { os.Remove(statePath(repo)) }
