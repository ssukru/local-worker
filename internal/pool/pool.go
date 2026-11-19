package pool

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ssukru/local-worker/internal/config"
	"github.com/ssukru/local-worker/internal/runner"
	"github.com/ssukru/local-worker/internal/util"
)

const (
	// Poll fast while anything is happening, slowly when the repo is quiet.
	// A warm runner (min >= 1) picks up the first job with no poll at all, so
	// the idle interval only bounds how fast we react to a *burst*.
	pollActive = 5 * time.Second
	pollIdle   = 15 * time.Second

	// How long to let a job finish when the daemon is asked to stop. launchd
	// SIGKILLs at 20s, so staying under that keeps the shutdown graceful.
	shutdownGrace = 15 * time.Second

	// Floor on how fast a worker may retry. A working runner blocks for as
	// long as it waits for a job, so anything quicker is a failure loop.
	minCycle = 10 * time.Second
)

var errNoLongerAuto = errors.New("repo is no longer autoscaled")

// Pool supervises this repo's runner processes. Each worker owns one slot's
// instance directory and runs an ephemeral runner: register, take exactly one
// job, exit. The worker then re-registers, so a busy pool keeps cycling while
// an unwanted one simply isn't restarted.
type Pool struct {
	repo string
	log  func(string)

	mu      sync.Mutex
	workers map[int]*worker

	distMu  sync.Mutex
	distDir string
}

type worker struct {
	slot int
	pool *Pool

	quit atomic.Bool
	busy atomic.Bool
	// idleSince is unix seconds; only meaningful while !busy.
	idleSince atomic.Int64

	mu   sync.Mutex
	cmd  *exec.Cmd
	done chan struct{}
}

// Daemon runs the autoscaler for a repo until the config stops asking for it.
func Daemon(repo string) error {
	p := &Pool{
		repo:    repo,
		workers: map[int]*worker{},
		log:     func(msg string) { fmt.Printf("[%s] %s\n", util.Timestamp(), msg) },
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-stop
		p.log("stop requested — draining runners")
		p.shutdown()
		os.Exit(0)
	}()

	p.log("autoscaler started for " + repo)
	for {
		wait, err := p.tick()
		if err == errNoLongerAuto {
			p.shutdown()
			ClearState(repo)
			return nil
		}
		time.Sleep(wait)
	}
}

// tick runs one observe-decide-act cycle and returns how long to wait.
func (p *Pool) tick() (time.Duration, error) {
	cfg := config.Load()
	entry := config.FindRunner(&cfg, p.repo)
	if entry == nil {
		p.log(p.repo + " is no longer attached — exiting.")
		return 0, errNoLongerAuto
	}
	if !entry.Auto() {
		p.log(p.repo + " switched to a fixed runner count — exiting.")
		return 0, errNoLongerAuto
	}

	min, max := entry.MinRunners(), entry.MaxRunners()
	ours := EffectiveLabels(entry.Labels)
	names := map[string]bool{}
	for slot := 1; slot <= max; slot++ {
		names[entry.SlotName(slot)] = true
	}

	state := &State{Min: min, Max: max, UpdatedAt: util.Timestamp()}
	demand, err := QueryDemand(p.repo, ours, func(name string) bool { return names[name] })
	if err != nil {
		// Network or auth trouble: hold the pool where it is rather than
		// tearing down runners that may be mid-job.
		p.log("could not read the job queue: " + err.Error())
		state.LastError = err.Error()
		p.snapshot(state, p.desiredFloor(min))
		return pollIdle, nil
	}

	desired := Desired(demand, min, max)
	p.reconcile(desired, entry, time.Duration(entry.IdleSeconds())*time.Second)

	state.Queued = demand.Queued
	p.snapshot(state, desired)

	if demand.Queued > 0 || demand.Running > 0 {
		return pollActive, nil
	}
	return pollIdle, nil
}

func (p *Pool) desiredFloor(min int) int {
	if live := p.liveCount(); live > min {
		return live
	}
	return min
}

func (p *Pool) snapshot(s *State, desired int) {
	s.Desired = desired
	s.Live, s.Busy = p.counts()
	if err := WriteState(p.repo, s); err != nil {
		p.log("could not write pool state: " + err.Error())
	}
}

// reconcile brings the live worker count to desired.
func (p *Pool) reconcile(desired int, entry *config.Runner, idleTimeout time.Duration) {
	live := p.liveCount()

	for live < desired {
		slot := p.spawn(entry)
		if slot == 0 {
			break // every slot is taken
		}
		p.log(fmt.Sprintf("scaling up: runner #%d starting (%d/%d)", slot, live+1, desired))
		live++
	}

	if live <= desired {
		return
	}
	// Scale down. Busy workers are never touched — they exit on their own
	// after the job they are running, and an ephemeral runner that finishes
	// simply isn't replaced. Only runners that never got work are retired,
	// and only after the idle grace period so a burst doesn't thrash.
	surplus := live - desired
	for _, w := range p.idleWorkers() {
		if surplus == 0 {
			break
		}
		if w.idleFor() < idleTimeout {
			continue
		}
		p.log(fmt.Sprintf("scaling down: runner #%d idle for %s — retiring", w.slot, w.idleFor().Round(time.Second)))
		w.stop()
		surplus--
	}
}

// spawn claims the lowest free slot and starts a worker on it, returning the
// slot (or 0 when all are taken). Claiming and inserting happen under one
// lock: a worker exiting concurrently mutates the same map, and two
// scale-ups must never land on the same instance directory.
func (p *Pool) spawn(entry *config.Runner) int {
	max := entry.MaxRunners()

	p.mu.Lock()
	slot := 0
	for candidate := 1; candidate <= max; candidate++ {
		if _, taken := p.workers[candidate]; !taken {
			slot = candidate
			break
		}
	}
	if slot == 0 {
		p.mu.Unlock()
		return 0
	}
	w := &worker{slot: slot, pool: p, done: make(chan struct{})}
	w.markIdle()
	p.workers[slot] = w
	p.mu.Unlock()

	go w.run(entry.SlotName(slot), append([]string{}, entry.Labels...))
	return slot
}

func (p *Pool) forget(slot int) {
	p.mu.Lock()
	delete(p.workers, slot)
	p.mu.Unlock()
}

func (p *Pool) liveCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.workers)
}

func (p *Pool) counts() (live, busy int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, w := range p.workers {
		live++
		if w.busy.Load() {
			busy++
		}
	}
	return live, busy
}

// idleWorkers returns idle workers, highest slot first, so scale-down retires
// the most recently added runners and keeps the low slots warm.
func (p *Pool) idleWorkers() []*worker {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []*worker
	for slot := util.MaxSlots; slot >= 1; slot-- {
		if w := p.workers[slot]; w != nil && !w.busy.Load() {
			out = append(out, w)
		}
	}
	return out
}

// shutdown stops every worker, giving jobs in flight a chance to finish.
func (p *Pool) shutdown() {
	p.mu.Lock()
	workers := make([]*worker, 0, len(p.workers))
	for _, w := range p.workers {
		workers = append(workers, w)
	}
	p.mu.Unlock()

	for _, w := range workers {
		w.quit.Store(true)
		if !w.busy.Load() {
			w.terminate()
		}
	}

	deadline := time.Now().Add(shutdownGrace)
	for _, w := range workers {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		select {
		case <-w.done:
		case <-time.After(remaining):
		}
	}
	for _, w := range workers {
		w.kill()
	}
}

// dist resolves (and downloads once) the runner distribution shared by all
// slots.
func (p *Pool) dist() (string, error) {
	p.distMu.Lock()
	defer p.distMu.Unlock()
	if p.distDir != "" {
		return p.distDir, nil
	}
	asset, err := runner.LatestAsset("")
	if err != nil {
		return "", err
	}
	dir, err := runner.DownloadDist(asset)
	if err != nil {
		return "", err
	}
	p.distDir = dir
	return dir, nil
}

// ---------------------------------------------------------------- worker

func (w *worker) run(name string, labels []string) {
	defer close(w.done)
	defer w.pool.forget(w.slot)

	for !w.quit.Load() {
		started := time.Now()
		err := w.cycle(name, labels)
		if w.quit.Load() {
			return
		}
		if err != nil {
			w.pool.log(fmt.Sprintf("#%d %v", w.slot, err))
		}
		// A healthy cycle blocks until a job arrives, so a fast one means the
		// runner failed to stay up (bad token, revoked registration, no
		// network). Back off rather than hammering the API in a hot loop.
		if elapsed := time.Since(started); elapsed < minCycle {
			time.Sleep(minCycle - elapsed)
		}
	}
}

// cycle registers the runner and blocks until it has run one job. Ephemeral
// registration is what makes scale-down free: the runner deregisters itself
// on the way out, so a pool that shrinks leaves nothing behind on GitHub.
func (w *worker) cycle(name string, labels []string) error {
	distDir, err := w.pool.dist()
	if err != nil {
		return fmt.Errorf("runner download failed: %w", err)
	}
	// The previous ephemeral run deregistered server-side but left its local
	// credentials; config.sh refuses to reconfigure over those.
	runner.ClearRegistration(w.pool.repo, w.slot)
	dir, err := runner.Configure(runner.ConfigureOpts{
		Repo:      w.pool.repo,
		Slot:      w.slot,
		Name:      name,
		Labels:    labels,
		DistDir:   distDir,
		Ephemeral: true,
	})
	if err != nil {
		return fmt.Errorf("registration failed: %w", err)
	}
	w.markIdle()
	return w.exec(dir)
}

func (w *worker) exec(dir string) error {
	cmd := exec.Command(filepath.Join(dir, "run.sh"))
	cmd.Dir = dir
	isolateProcess(cmd)

	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw

	if err := cmd.Start(); err != nil {
		pw.Close()
		return fmt.Errorf("could not start runner: %w", err)
	}
	w.mu.Lock()
	w.cmd = cmd
	w.mu.Unlock()

	scanned := make(chan struct{})
	go func() {
		defer close(scanned)
		w.scan(pr)
	}()

	err := cmd.Wait()
	pw.Close()
	<-scanned

	w.mu.Lock()
	w.cmd = nil
	w.mu.Unlock()
	w.busy.Store(false)
	w.markIdle()

	if err != nil && !w.quit.Load() {
		return fmt.Errorf("runner exited: %w", err)
	}
	return nil
}

// scan mirrors the runner's output into the daemon log and tracks whether
// this worker is mid-job — which is what makes scale-down safe.
func (w *worker) scan(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if started, finished := parseJobEvent(line); started {
			w.busy.Store(true)
		} else if finished {
			w.busy.Store(false)
			w.markIdle()
		}
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			w.pool.log(fmt.Sprintf("#%d %s", w.slot, trimmed))
		}
	}
}

// parseJobEvent reads the runner's own progress lines. These markers have
// been stable across actions/runner releases for years, and reading them
// costs nothing — the alternative, polling the API for each runner's busy
// flag, is both slower and staler than the process telling us directly.
func parseJobEvent(line string) (started, finished bool) {
	switch {
	case strings.Contains(line, ": Running job: "):
		return true, false
	case strings.Contains(line, " completed with result: "):
		return false, true
	default:
		return false, false
	}
}

func (w *worker) markIdle() { w.idleSince.Store(time.Now().Unix()) }

func (w *worker) idleFor() time.Duration {
	since := w.idleSince.Load()
	if since == 0 {
		return 0
	}
	return time.Since(time.Unix(since, 0))
}

// stop asks the worker to finish and terminates its runner process.
func (w *worker) stop() {
	w.quit.Store(true)
	w.terminate()
}

func (w *worker) terminate() {
	w.mu.Lock()
	cmd := w.cmd
	w.mu.Unlock()
	terminateGroup(cmd)
}

func (w *worker) kill() {
	w.mu.Lock()
	cmd := w.cmd
	w.mu.Unlock()
	killGroup(cmd)
}
