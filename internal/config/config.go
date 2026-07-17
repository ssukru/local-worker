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

type Runner struct {
	Repo      string   `json:"repo"`
	Name      string   `json:"name"`
	Labels    []string `json:"labels"`
	Isolation string   `json:"isolation,omitempty"` // "", "host", "vm", "container"
	VM        *VM      `json:"vm,omitempty"`
	AddedAt   string   `json:"addedAt"`
	AddedBy   string   `json:"addedBy"`
}

func (r *Runner) Isolated() bool {
	return r.Isolation != "" && r.Isolation != "host"
}

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
