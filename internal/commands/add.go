package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ssukru/local-worker/internal/config"
	"github.com/ssukru/local-worker/internal/container"
	"github.com/ssukru/local-worker/internal/pool"
	"github.com/ssukru/local-worker/internal/runner"
	"github.com/ssukru/local-worker/internal/service"
	"github.com/ssukru/local-worker/internal/ui"
	"github.com/ssukru/local-worker/internal/util"
	"github.com/ssukru/local-worker/internal/vm"
)

var defaultLabels = []string{"local-worker"}

func machineName() string {
	// os.Hostname() can be a bare IP on home networks; the OS-level set name
	// is stable.
	var name string
	if runtime.GOOS == "darwin" {
		name, _ = util.TrySh("scutil", []string{"--get", "LocalHostName"})
	} else {
		name, _ = util.TrySh("hostnamectl", []string{"--static"})
	}
	if name == "" {
		name, _ = os.Hostname()
	}
	return strings.ToLower(strings.TrimSuffix(name, ".local"))
}

var repoRe = regexp.MustCompile(`^[\w.-]+/[\w.-]+$`)

// intFlag parses a bounded numeric flag, exiting with a clear message rather
// than silently falling back to a default. Returns 0 when the flag is absent.
func intFlag(flags *Flags, name string, min, max int) int {
	if !flags.Has(name) {
		return 0
	}
	n, err := strconv.Atoi(flags.Get(name))
	if err != nil || n < min || n > max {
		ui.Error(fmt.Sprintf("--%s must be a number between %d and %d.", name, min, max))
		os.Exit(1)
	}
	return n
}

// PickRepo lets the user choose a repository interactively; returns "" on
// cancel.
func PickRepo() (string, error) {
	var s ui.Spinner
	s.Start("Fetching your repositories")
	out, err := util.Sh("gh", []string{
		"repo", "list",
		"--limit", "100",
		"--no-archived",
		"--json", "nameWithOwner,isPrivate,pushedAt",
	})
	if err != nil {
		s.Stop("Could not list repositories")
		return "", err
	}
	var repos []struct {
		NameWithOwner string `json:"nameWithOwner"`
		IsPrivate     bool   `json:"isPrivate"`
		PushedAt      string `json:"pushedAt"`
	}
	if err := json.Unmarshal([]byte(out), &repos); err != nil {
		s.Stop("Could not list repositories")
		return "", err
	}
	sort.Slice(repos, func(i, j int) bool { return repos[i].PushedAt > repos[j].PushedAt })
	s.Stop(fmt.Sprintf("Found %d repositories", len(repos)))

	options := []ui.SelectOption{}
	for i, r := range repos {
		if i >= 30 {
			break
		}
		hint := "private"
		if !r.IsPrivate {
			hint = ui.Yellow("PUBLIC — read the security note")
		}
		options = append(options, ui.SelectOption{Value: r.NameWithOwner, Label: r.NameWithOwner, Hint: hint})
	}
	options = append(options, ui.SelectOption{Value: "__other", Label: "Type an owner/repo manually…"})

	choice, err := ui.Select("Which repository should send jobs to this machine?", options)
	if err != nil {
		return "", nil // cancelled
	}
	if choice != "__other" {
		return choice, nil
	}
	typed, err := ui.Text("Repository (owner/repo):", "owner/repo", "", func(v string) error {
		if !repoRe.MatchString(v) {
			return fmt.Errorf("Expected owner/repo")
		}
		return nil
	})
	if err != nil {
		return "", nil // cancelled
	}
	return typed, nil
}

// Add attaches this machine as a runner for a repo.
func Add(repoArg string, flags *Flags, quietIntro bool) error {
	if !quietIntro {
		ui.Intro(" local-worker · add runner ")
	}

	// Validate flags before touching GitHub, so a typo costs nothing.
	requestedCount := intFlag(flags, "count", 1, util.MaxSlots)
	requestedMin := intFlag(flags, "min", 0, util.MaxSlots)
	requestedMax := intFlag(flags, "max", 1, util.MaxSlots)
	if flags.Has("count") && (flags.Has("min") || flags.Has("max")) {
		ui.Error("--count sets a fixed pool; --min/--max size an autoscaling one. Use one or the other.")
		os.Exit(1)
	}
	if requestedMin > 0 && requestedMax > 0 && requestedMin > requestedMax {
		ui.Error(fmt.Sprintf("--min (%d) cannot exceed --max (%d).", requestedMin, requestedMax))
		os.Exit(1)
	}

	if !util.GhAuthed() {
		ui.Error("GitHub CLI is not authenticated. Run `gh auth login` first (or `lw setup`).")
		os.Exit(1)
	}

	repo := repoArg
	if repo == "" {
		if !util.IsTTY() {
			ui.Error("No repo given. Usage: lw add owner/repo")
			os.Exit(1)
		}
		var err error
		repo, err = PickRepo()
		if err != nil {
			return err
		}
		if repo == "" {
			ui.Cancel("Cancelled.")
			return nil
		}
	}

	cfg := config.Load()
	existing := config.FindRunner(&cfg, repo)
	if existing != nil && !flags.Has("count") {
		ui.Warn(repo + " is already attached. Re-registering it (safe, uses --replace).")
	}

	// How many runners serve this repo concurrently. Omitting --count on a
	// re-add keeps the current count rather than silently scaling to 1.
	count := 1
	if existing != nil {
		count = existing.Slots()
	}
	if requestedCount > 0 {
		count = requestedCount
	}

	// Autoscaling is the default for new repos. An existing fixed pool is
	// left fixed unless the user explicitly asks otherwise — a re-add should
	// never silently change how someone's runners are managed.
	mode := config.ModeAuto
	switch {
	case flags.Has("count"):
		mode = config.ModeFixed
	case flags.Has("min") || flags.Has("max"):
		mode = config.ModeAuto
	case existing != nil && !existing.Auto():
		mode = config.ModeFixed
	}

	// Verify access + visibility before doing anything.
	infoOut, err := util.Sh("gh", []string{"api", "repos/" + repo, "--jq", `{private: .private, admin: .permissions.admin}`})
	if err != nil {
		return err
	}
	var info struct {
		Private bool `json:"private"`
		Admin   bool `json:"admin"`
	}
	if err := json.Unmarshal([]byte(infoOut), &info); err != nil {
		return err
	}
	if !info.Admin {
		ui.Error("You need admin access on " + repo + " to register runners.")
		os.Exit(1)
	}
	if !info.Private {
		isolationHint := "container"
		if runtime.GOOS == "darwin" {
			isolationHint = "vm"
		}
		ui.Warn(repo + " is " + ui.Yellow("PUBLIC") + ". Self-hosted runners on public repos can be attacked via fork PRs.\n" +
			"If you continue, do BOTH of these:\n" +
			"  1. Settings → Actions: require approval for ALL outside collaborators,\n" +
			"     and never let fork pull_request workflows reach this runner.\n" +
			"  2. Use throwaway isolation so nothing survives between jobs:\n" +
			"       lw add " + repo + " --isolation " + isolationHint + " --refresh per-job\n" +
			"See the Security section of the README for the full threat model.")
		if util.IsTTY() && !flags.Bool("yes") {
			ok, err := ui.Confirm("Continue anyway?", false)
			if err != nil || !ok {
				ui.Cancel("Cancelled.")
				return nil
			}
		} else if !flags.Bool("yes") {
			ui.Error("Refusing to attach a public repo non-interactively without --yes.")
			os.Exit(1)
		}
	}

	extraLabels := defaultLabels
	if flags.Has("labels") {
		extraLabels = nil
		for _, l := range strings.Split(flags.Get("labels"), ",") {
			if l = strings.TrimSpace(l); l != "" {
				extraLabels = append(extraLabels, l)
			}
		}
	}
	name := flags.Get("name")
	if name == "" {
		id := util.RepoID(repo)
		if len(id) > 40 {
			id = id[:40]
		}
		name = machineName() + "-" + id
	}

	isolation := "host"
	if flags.Has("isolation") {
		isolation = flags.Get("isolation")
	}
	switch isolation {
	case "host", "vm", "container":
	default:
		ui.Error(`Unknown isolation mode "` + isolation + `" — use host, vm or container.`)
		os.Exit(1)
	}
	if isolation != "host" {
		if flags.Has("min") || flags.Has("max") {
			ui.Error("Autoscaling isn't supported for vm/container isolation yet — " +
				"booting a fresh VM per job is too slow to scale on demand.\n" +
				"Use --count N for a fixed pool of isolated runners.")
			os.Exit(1)
		}
		return addIsolated(repo, name, extraLabels, isolation, count, flags, &cfg, quietIntro)
	}

	if mode == config.ModeAuto {
		return addAuto(repo, name, extraLabels, flags, existing, &cfg, quietIntro)
	}

	var s ui.Spinner
	s.Start("Downloading the GitHub Actions runner")
	asset, err := runner.LatestAsset("")
	if err != nil {
		return err
	}
	distDir, err := runner.DownloadDist(asset)
	if err != nil {
		return err
	}
	s.Stop(fmt.Sprintf("Runner %s ready (%s)", asset.Version, util.MachineArch()))

	retireAutoscaler(repo, existing)

	// Each slot is a separate registration with its own service, so GitHub can
	// hand out `count` jobs at once instead of queueing them behind one
	// listener.
	for slot := 1; slot <= count; slot++ {
		slotName := util.SlotName(name, slot)
		s.Start(fmt.Sprintf("Registering %q with %s (%d/%d)", slotName, repo, slot, count))
		if _, err := runner.Configure(runner.ConfigureOpts{
			Repo: repo, Slot: slot, Name: slotName, Labels: extraLabels, DistDir: distDir,
		}); err != nil {
			return err
		}
		label, err := runner.InstallService(repo, slot)
		if err != nil {
			return err
		}
		s.Stop(fmt.Sprintf("%s registered · service loaded: %s", slotName, label))
	}
	archLabel := "X64"
	if util.MachineArch() == "arm64" {
		archLabel = "ARM64"
	}
	ui.Info(fmt.Sprintf("%s registered with labels: self-hosted, %s, %s, %s",
		pluralRunners(count), util.OsLabel(), archLabel, strings.Join(extraLabels, ", ")))

	// Scaling down: drop the instances above the new count.
	if removed := teardownSlots(repo, count, existing, &s); removed > 0 {
		ui.Info(fmt.Sprintf("Removed %d runner(s) above the new count.", removed))
	}

	entry := config.Runner{
		Repo:    repo,
		Name:    name,
		Count:   count,
		Labels:  extraLabels,
		AddedAt: util.Timestamp(),
		AddedBy: util.GhLogin(),
	}
	config.RemoveRunner(&cfg, repo)
	cfg.Runners = append(cfg.Runners, entry)
	if err := config.Save(cfg); err != nil {
		return err
	}

	// Confirm GitHub sees them online.
	s.Start("Waiting for GitHub to see the runners online")
	online := 0
	for i := 0; i < 10 && online < count; i++ {
		time.Sleep(2 * time.Second)
		remote := runner.Remote(repo)
		online = 0
		for slot := 1; slot <= count; slot++ {
			if mine := runner.FindRemote(remote, util.SlotName(name, slot)); mine != nil && mine.Status == "online" {
				online++
			}
		}
	}
	switch {
	case online == count && count == 1:
		s.Stop(ui.Green("Runner is ONLINE on GitHub ✔"))
	case online == count:
		s.Stop(ui.Green(fmt.Sprintf("All %d runners are ONLINE on GitHub ✔", count)))
	default:
		s.Stop(ui.Yellow(fmt.Sprintf("%d/%d online so far — check `lw status` in a minute", online, count)))
	}

	next := "Route jobs here by changing workflows in " + repo + ":\n" +
		"  runs-on: [self-hosted]\n\n" +
		"Run " + ui.Cyan("lw integrate "+repo) + " for a copy-paste patch with a\n" +
		"hosted-runner fallback input."
	if count > 1 {
		next += "\n\nUp to " + strconv.Itoa(count) + " jobs now run at the same time. Jobs share this\n" +
			"machine's CPU, RAM, ports and Docker daemon — see the Concurrency\n" +
			"section of the README before raising it further."
	}
	ui.Note(next, "Next step")
	if !quietIntro {
		ui.Outro("Done.")
	}
	return nil
}

// addAuto attaches a repo whose pool sizes itself. Instead of N permanent
// runner services, one daemon watches the repo's job queue and runs an
// ephemeral runner per queued job.
func addAuto(repo, name string, labels []string, flags *Flags, existing *config.Runner, cfg *config.Config, quietIntro bool) error {
	// Start from the current setup when re-adding, otherwise from defaults
	// sized to this machine.
	min, max := 1, util.DefaultMaxRunners()
	if existing != nil && existing.Auto() {
		min, max = existing.MinRunners(), existing.MaxRunners()
	}
	// --min 0 is a meaningful choice (a fully cold pool), so presence of the
	// flag decides, not its value.
	if flags.Has("min") {
		min = intFlag(flags, "min", 0, util.MaxSlots)
	}
	if flags.Has("max") {
		max = intFlag(flags, "max", 1, util.MaxSlots)
	}
	if min > max {
		max = min
	}

	var s ui.Spinner

	// Switching away from a fixed pool: retire its per-runner services first,
	// or they'd keep taking jobs alongside the autoscaler.
	if existing != nil && !existing.Auto() {
		s.Start("Replacing the fixed runner pool with an autoscaling one")
		for slot := 1; slot <= existing.TopSlot(); slot++ {
			runner.UninstallService(repo, slot)
		}
		s.Stop("Fixed runner services removed")
	}

	s.Start("Downloading the GitHub Actions runner")
	asset, err := runner.LatestAsset("")
	if err != nil {
		return err
	}
	if _, err := runner.DownloadDist(asset); err != nil {
		return err
	}
	s.Stop(fmt.Sprintf("Runner %s ready (%s)", asset.Version, util.MachineArch()))

	entry := config.Runner{
		Repo:    repo,
		Name:    name,
		Labels:  labels,
		Scale:   &config.Scale{Mode: config.ModeAuto, Min: min, Max: max},
		AddedAt: util.Timestamp(),
		AddedBy: util.GhLogin(),
	}
	config.RemoveRunner(cfg, repo)
	cfg.Runners = append(cfg.Runners, entry)
	if err := config.Save(*cfg); err != nil {
		return err
	}

	s.Start("Installing the autoscaler")
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// Service managers can't reach gh's keyring, and the daemon mints a
	// registration token for every ephemeral runner it starts.
	token, err := util.Sh("gh", []string{"auth", "token"})
	if err != nil {
		return err
	}
	label := util.PoolLabel(repo)
	if err := service.Get().Install(service.Spec{
		Label:       label,
		ProgramArgs: []string{exe, "pool", "daemon", repo},
		LogPath:     filepath.Join(util.LogsDir(), label+".log"),
		KeepAlive:   true,
		Env:         map[string]string{"GH_TOKEN": token},
	}); err != nil {
		return err
	}
	s.Stop("Autoscaler running: " + label)

	archLabel := "X64"
	if util.MachineArch() == "arm64" {
		archLabel = "ARM64"
	}
	ui.Info(fmt.Sprintf("Labels: self-hosted, %s, %s, %s", util.OsLabel(), archLabel, strings.Join(labels, ", ")))

	warm := fmt.Sprintf("%d idle runner(s) kept warm", min)
	if min == 0 {
		warm = "no idle runners (first job waits for one to start)"
	}
	ui.Note(
		fmt.Sprintf("Jobs now scale on demand: %s, up to %s at once.\n", warm, pluralRunners(max))+
			"Queue 6 jobs and "+strconv.Itoa(minInt(6, max))+" run in parallel; the pool shrinks\n"+
			"again when the queue empties. Nothing to tune per workflow.\n\n"+
			"  Route jobs here:  runs-on: [self-hosted]\n"+
			"  Resize any time:  "+ui.Cyan("lw scale "+repo+" --max 10")+"\n"+
			"  Watch it work:    "+ui.Cyan("lw status")+" · "+ui.Cyan("lw logs "+repo),
		"Autoscaling enabled")
	if !quietIntro {
		ui.Outro("Done.")
	}
	return nil
}

// retireAutoscaler stops a repo's autoscaler when it moves to a fixed pool,
// so the daemon can't keep spawning runners alongside the fixed ones.
func retireAutoscaler(repo string, existing *config.Runner) {
	if existing == nil || !existing.Auto() {
		return
	}
	service.Get().Uninstall(util.PoolLabel(repo))
	pool.ClearState(repo)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func pluralRunners(n int) string {
	if n == 1 {
		return "1 runner"
	}
	return strconv.Itoa(n) + " runners"
}

// teardownSlots removes the instances above keep when a repo scales down.
// Returns how many were torn down.
func teardownSlots(repo string, keep int, existing *config.Runner, s *ui.Spinner) int {
	from := keep
	if existing != nil && existing.Slots() > from {
		from = existing.Slots()
	}
	// Instances can outlive the config (an interrupted scale-down, a hand-edited
	// config), so probe disk past the recorded count too.
	from = runner.LocalSlots(repo, from, func(dir string) bool {
		return !ownedByAnotherRepo(repo, dir)
	})
	removed := 0
	for slot := from; slot > keep; slot-- {
		s.Start(fmt.Sprintf("Removing runner #%d", slot))
		runner.UninstallService(repo, slot)
		if existing != nil && existing.Isolated() {
			name := vm.Name(repo, slot)
			if existing.Isolation == "container" {
				name = container.Name(repo, slot)
				container.Delete(name)
			} else {
				vm.Delete(name)
			}
			vm.ClearState(name)
			vm.ForceDeregister(repo, existing.SlotName(slot))
		} else {
			runner.Deregister(repo, slot)
		}
		s.Stop(fmt.Sprintf("Runner #%d removed", slot))
		removed++
	}
	return removed
}

// ownedByAnotherRepo guards the slot probe: a repo genuinely named "foo-2"
// has the same instance directory as slot 2 of "foo", and must not be torn
// down as one.
func ownedByAnotherRepo(repo, dir string) bool {
	cfg := config.Load()
	for _, r := range cfg.Runners {
		if strings.EqualFold(r.Repo, repo) {
			continue
		}
		for slot := 1; slot <= r.Slots(); slot++ {
			if runner.Dir(r.Repo, slot) == dir {
				return true
			}
		}
	}
	return false
}

// addIsolated sets up a VM (tart) or container (docker) isolated runner: a
// daemon owns the lifecycle.
func addIsolated(repo, name string, labels []string, isolation string, count int, flags *Flags, cfg *config.Config, quietIntro bool) error {
	var err error
	if isolation == "vm" {
		err = vm.AssertSupport()
	} else {
		err = container.AssertSupport()
	}
	if err != nil {
		ui.Error(err.Error())
		os.Exit(1)
	}

	refresh := "never"
	if flags.Has("refresh") {
		refresh = flags.Get("refresh")
	}
	if !vm.ValidRefresh(refresh) {
		ui.Error(`Invalid --refresh "` + refresh + `" — use never, per-job, or an interval like 24h.`)
		os.Exit(1)
	}
	image := flags.Get("vm-image")
	if image == "" {
		if isolation == "vm" {
			image = vm.DefaultImages["macos-base"]
		} else {
			image = container.DefaultImage
		}
	}

	// Tear down surplus environments before rewriting the config — teardown
	// reads the old entry to know each slot's isolation backend and name.
	var s ui.Spinner
	prior := config.FindRunner(cfg, repo)
	retireAutoscaler(repo, prior)
	teardownSlots(repo, count, prior, &s)

	entry := config.Runner{
		Repo:      repo,
		Name:      name,
		Count:     count,
		Labels:    labels,
		Isolation: isolation,
		VM:        &config.VM{Image: image, Refresh: refresh},
		AddedAt:   util.Timestamp(),
		AddedBy:   util.GhLogin(),
	}
	config.RemoveRunner(cfg, repo)
	cfg.Runners = append(cfg.Runners, entry)
	if err := config.Save(*cfg); err != nil {
		return err
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// Service managers can't reach gh's keyring; the daemon needs the token
	// to mint registration tokens (hourly for per-job runners).
	token, err := util.Sh("gh", []string{"auth", "token"})
	if err != nil {
		return err
	}
	// One daemon per slot, each owning its own VM/container.
	for slot := 1; slot <= count; slot++ {
		label := util.RunnerLabel(repo, slot)
		if err := service.Get().Install(service.Spec{
			Label:       label,
			ProgramArgs: []string{exe, "vm", "daemon", repo, "--slot", strconv.Itoa(slot)},
			LogPath:     filepath.Join(util.LogsDir(), label+".log"),
			KeepAlive:   true,
			Env:         map[string]string{"GH_TOKEN": token},
		}); err != nil {
			return err
		}
	}

	ui.Success(fmt.Sprintf("%s attached to %s · %s isolation (image: %s, refresh: %s)",
		pluralRunners(count), repo, isolation, image, refresh))
	sizeNote := ""
	if isolation == "vm" {
		sizeNote = " — macOS images are 20-60 GB, this can take a while"
		if count > 1 {
			sizeNote += fmt.Sprintf(", and each of the %d VMs is a separate copy", count)
		}
	}
	logsHint := "lw logs " + repo
	if count > 1 {
		logsHint += " --slot N"
	}
	ui.Info("First start pulls the image" + sizeNote + ".\n" +
		"Watch progress with " + ui.Cyan(logsHint) + "; runners appear in " + ui.Cyan("lw status") + " when ready.")
	if !quietIntro {
		ui.Outro("Done.")
	}
	return nil
}
