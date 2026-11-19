package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/ssukru/local-worker/internal/config"
	"github.com/ssukru/local-worker/internal/container"
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
	if config.FindRunner(&cfg, repo) != nil {
		ui.Warn(repo + " is already attached. Re-registering it (safe, uses --replace).")
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
		return addIsolated(repo, name, extraLabels, isolation, flags, &cfg, quietIntro)
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

	s.Start(fmt.Sprintf("Registering %q with %s", name, repo))
	if _, err := runner.Configure(runner.ConfigureOpts{Repo: repo, Name: name, Labels: extraLabels, DistDir: distDir}); err != nil {
		return err
	}
	archLabel := "X64"
	if util.MachineArch() == "arm64" {
		archLabel = "ARM64"
	}
	s.Stop(fmt.Sprintf("Registered with labels: self-hosted, %s, %s, %s", util.OsLabel(), archLabel, strings.Join(extraLabels, ", ")))

	s.Start("Installing background service (auto-start, restart on crash)")
	label, err := runner.InstallService(repo)
	if err != nil {
		return err
	}
	s.Stop("Service loaded: " + label)

	entry := config.Runner{
		Repo:    repo,
		Name:    name,
		Labels:  extraLabels,
		AddedAt: util.Timestamp(),
		AddedBy: util.GhLogin(),
	}
	config.RemoveRunner(&cfg, repo)
	cfg.Runners = append(cfg.Runners, entry)
	if err := config.Save(cfg); err != nil {
		return err
	}

	// Confirm GitHub sees it online.
	s.Start("Waiting for GitHub to see the runner online")
	online := false
	for i := 0; i < 10 && !online; i++ {
		time.Sleep(2 * time.Second)
		remote := runner.Remote(repo)
		if mine := runner.FindRemote(remote, name); mine != nil && mine.Status == "online" {
			online = true
		}
	}
	if online {
		s.Stop(ui.Green("Runner is ONLINE on GitHub ✔"))
	} else {
		s.Stop(ui.Yellow("Not online yet — check `lw status` in a minute"))
	}

	ui.Note(
		"Route jobs here by changing workflows in "+repo+":\n"+
			"  runs-on: [self-hosted]\n\n"+
			"Run "+ui.Cyan("lw integrate "+repo)+" for a copy-paste patch with a\n"+
			"hosted-runner fallback input.",
		"Next step")
	if !quietIntro {
		ui.Outro("Done.")
	}
	return nil
}

// addIsolated sets up a VM (tart) or container (docker) isolated runner: a
// daemon owns the lifecycle.
func addIsolated(repo, name string, labels []string, isolation string, flags *Flags, cfg *config.Config, quietIntro bool) error {
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

	entry := config.Runner{
		Repo:      repo,
		Name:      name,
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

	label := util.RunnerLabel(repo)
	exe, err := util.ExecutablePath(os.Args[0])
	if err != nil {
		return err
	}
	// Service managers can't reach gh's keyring; the daemon needs the token
	// to mint registration tokens (hourly for per-job runners).
	token, err := util.Sh("gh", []string{"auth", "token"})
	if err != nil {
		return err
	}
	if err := service.Get().Install(service.Spec{
		Label:       label,
		ProgramArgs: []string{exe, "vm", "daemon", repo},
		LogPath:     filepath.Join(util.LogsDir(), label+".log"),
		KeepAlive:   true,
		Env:         map[string]string{"GH_TOKEN": token},
	}); err != nil {
		return err
	}

	ui.Success(fmt.Sprintf("%s daemon installed for %s (image: %s, refresh: %s)", isolation, repo, image, refresh))
	sizeNote := ""
	if isolation == "vm" {
		sizeNote = " — macOS images are 20-60 GB, this can take a while"
	}
	ui.Info("First start pulls the image" + sizeNote + ".\n" +
		"Watch progress with " + ui.Cyan("lw logs "+repo) + "; the runner appears in " + ui.Cyan("lw status") + " when ready.")
	if !quietIntro {
		ui.Outro("Done.")
	}
	return nil
}
