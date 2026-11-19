// Package config reads and writes ~/.local-worker/config.json — the Go port
// of src/config.js.
package config

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/ssukru/local-worker/internal/util"
)

type Telegram struct {
	BotToken string `json:"botToken"`
	ChatID   string `json:"chatId"`
}

type Watchdog struct {
	Installed       bool     `json:"installed"`
	IntervalSeconds int      `json:"intervalSeconds"`
	HealthchecksURL string   `json:"healthchecksUrl"`
	Telegram        Telegram `json:"telegram"`
}

// VM holds isolation settings shared by the Tart and Docker backends.
type VM struct {
	Image   string `json:"image"`
	Refresh string `json:"refresh"`
}

// Scale controls how many runners serve a repo.
//
//	auto  — an autoscaler daemon watches the repo's job queue and runs one
//	        ephemeral runner per queued job, between Min and Max.
//	fixed — Count runners stay registered permanently, one service each.
//
// Configs written before autoscaling have no scale block at all; they are
// read as fixed mode so existing installs keep behaving exactly as they did.
type Scale struct {
	Mode        string `json:"mode"` // "auto" | "fixed"
	Min         int    `json:"min,omitempty"`
	Max         int    `json:"max,omitempty"`
	IdleSeconds int    `json:"idleSeconds,omitempty"`
}

const (
	ModeAuto  = "auto"
	ModeFixed = "fixed"

	// DefaultIdleSeconds is how long a runner that never picked up a job is
	// kept warm before the pool retires it.
	DefaultIdleSeconds = 120
)

type Runner struct {
	Repo string `json:"repo"`
	// Name is the GitHub-visible name of slot 1; later slots suffix it.
	Name string `json:"name"`
	// Count is how many runner instances serve this repo concurrently — one
	// job each. Absent/0 in configs written before multi-runner support.
	Count     int      `json:"count,omitempty"`
	Scale     *Scale   `json:"scale,omitempty"`
	Labels    []string `json:"labels"`
	Isolation string   `json:"isolation,omitempty"` // "", "host", "vm", "container"
	VM        *VM      `json:"vm,omitempty"`
	AddedAt   string   `json:"addedAt"`
	AddedBy   string   `json:"addedBy"`
}

func (r *Runner) Isolated() bool {
	return r.Isolation != "" && r.Isolation != "host"
}

// Auto reports whether this repo's pool scales itself. Absent scale block =
// fixed, so pre-autoscaling configs are untouched.
func (r *Runner) Auto() bool { return r.Scale != nil && r.Scale.Mode == ModeAuto }

// Slots is the fixed-mode instance count, normalised (never below 1).
func (r *Runner) Slots() int { return util.ClampSlots(r.Count) }

// MinRunners is the warm floor: runners kept registered even with an empty
// queue, so a single job starts without waiting for one to boot. Unlike a
// slot count this may legitimately be 0 — a fully cold pool that spawns a
// runner only when a job actually queues.
func (r *Runner) MinRunners() int {
	if !r.Auto() {
		return r.Slots()
	}
	switch {
	case r.Scale.Min < 0:
		return 0
	case r.Scale.Min > util.MaxSlots:
		return util.MaxSlots
	default:
		return r.Scale.Min
	}
}

// MaxRunners is the ceiling on concurrent runners.
func (r *Runner) MaxRunners() int {
	if !r.Auto() {
		return r.Slots()
	}
	max := util.ClampSlots(r.Scale.Max)
	if min := r.MinRunners(); max < min {
		return min
	}
	return max
}

// TopSlot is the highest slot number this repo may ever occupy — the bound
// for teardown and cleanup scans.
func (r *Runner) TopSlot() int {
	if r.Auto() {
		return r.MaxRunners()
	}
	return r.Slots()
}

func (r *Runner) IdleSeconds() int {
	if r.Scale == nil || r.Scale.IdleSeconds <= 0 {
		return DefaultIdleSeconds
	}
	return r.Scale.IdleSeconds
}

// SlotName returns slot n's GitHub-visible runner name.
func (r *Runner) SlotName(slot int) string { return util.SlotName(r.Name, slot) }

type Config struct {
	Runners  []Runner `json:"runners"`
	Watchdog Watchdog `json:"watchdog"`
}

func defaults() Config {
	return Config{
		Runners:  []Runner{},
		Watchdog: Watchdog{IntervalSeconds: 300},
	}
}

func Load() Config {
	cfg := defaults()
	raw, err := os.ReadFile(util.ConfigPath())
	if err != nil {
		return cfg
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return defaults()
	}
	if cfg.Runners == nil {
		cfg.Runners = []Runner{}
	}
	if cfg.Watchdog.IntervalSeconds == 0 {
		cfg.Watchdog.IntervalSeconds = 300
	}
	return cfg
}

func Save(cfg Config) error {
	if err := util.EnsureDirs(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(util.ConfigPath(), append(data, '\n'), 0o600)
}

// FindRunner returns the config entry for repo (case-insensitive), or nil.
func FindRunner(cfg *Config, repo string) *Runner {
	for i := range cfg.Runners {
		if strings.EqualFold(cfg.Runners[i].Repo, repo) {
			return &cfg.Runners[i]
		}
	}
	return nil
}

// RemoveRunner drops repo's entry (case-insensitive) from the list.
func RemoveRunner(cfg *Config, repo string) {
	kept := cfg.Runners[:0]
	for _, r := range cfg.Runners {
		if !strings.EqualFold(r.Repo, repo) {
			kept = append(kept, r)
		}
	}
	cfg.Runners = kept
}
