package commands

import (
	"errors"
	"fmt"

	"github.com/ssukru/local-worker/internal/config"
	"github.com/ssukru/local-worker/internal/pool"
)

// Pool is the entrypoint for the autoscaler daemon. It is internal — the
// service manager runs it, users go through `lw add` / `lw scale`.
func Pool(sub, repo string, flags *Flags) error {
	switch sub {
	case "daemon":
		if repo == "" {
			return errors.New("Usage: lw pool daemon owner/repo")
		}
		cfg := config.Load()
		entry := config.FindRunner(&cfg, repo)
		if entry == nil {
			return fmt.Errorf("%s is not attached", repo)
		}
		if !entry.Auto() {
			return fmt.Errorf("%s is not autoscaled (fixed pool of %d)", repo, entry.Slots())
		}
		return pool.Daemon(repo)
	default:
		fmt.Println("Usage: lw pool daemon owner/repo   (internal — run by the service manager)")
		return nil
	}
}
