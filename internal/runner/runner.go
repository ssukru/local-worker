// Package runner downloads, registers and manages actions/runner instances —
// the Go port of src/runner.js.
package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ssukru/local-worker/internal/service"
	"github.com/ssukru/local-worker/internal/util"
)

type Asset struct {
	Version string
	URL     string
	Name    string
}

// LatestAsset resolves the latest actions/runner download for this machine
// (or a given os-arch slug like "osx-arm64").
func LatestAsset(slug string) (Asset, error) {
	var release struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := util.GhJSON(&release, "repos/actions/runner/releases/latest"); err != nil {
		return Asset{}, err
	}
	wanted := slug
	if wanted == "" {
		wanted = util.RunnerOsSlug() + "-" + util.MachineArch()
	}
	for _, a := range release.Assets {
		if strings.HasPrefix(a.Name, "actions-runner-"+wanted+"-") &&
			strings.HasSuffix(a.Name, ".tar.gz") &&
			!strings.Contains(a.Name, "noexternals") &&
			!strings.Contains(a.Name, "noruntime") {
			return Asset{Version: release.TagName, URL: a.BrowserDownloadURL, Name: a.Name}, nil
		}
	}
	return Asset{}, fmt.Errorf("no runner asset found for %s", wanted)
}

// DownloadDist downloads and extracts the runner distribution once per
// version, returning the cache directory. Instances get full copies (not
// symlinks) because each stores its own credentials and work directory.
func DownloadDist(asset Asset) (string, error) {
	cacheDir := filepath.Join(util.Root(), "dist", asset.Version)
	marker := filepath.Join(cacheDir, ".complete")
	if _, err := os.Stat(marker); err == nil {
		return cacheDir, nil
	}
	os.RemoveAll(cacheDir)
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", err
	}
	tarball := filepath.Join(cacheDir, asset.Name)
	if _, err := util.Sh("curl", []string{"-fsSL", "-o", tarball, asset.URL}); err != nil {
		return "", err
	}
	if _, err := util.Sh("tar", []string{"xzf", tarball, "-C", cacheDir}); err != nil {
		return "", err
	}
	os.Remove(tarball)
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		return "", err
	}
	return cacheDir, nil
}

// Dir is the instance directory for a repo's Nth runner slot. Each slot is a
// full runner install: its own credentials, its own _work tree, its own
// Runner.Listener process — which is what lets slots take jobs in parallel.
func Dir(repo string, slot int) string {
	return filepath.Join(util.RunnersDir(), util.InstanceID(repo, slot))
}

// LocalSlots reports how many instance directories exist for a repo, probing
// upward from atLeast. Scaling down (or a config that drifted) leaves
// higher-numbered instances on disk; this finds them so they can be torn down.
// owned reports whether a directory belongs to this repo — a repo genuinely
// named "foo-2" would otherwise be mistaken for slot 2 of "foo".
func LocalSlots(repo string, atLeast int, owned func(dir string) bool) int {
	n := atLeast
	for slot := atLeast + 1; slot <= util.MaxSlots; slot++ {
		dir := Dir(repo, slot)
		if _, err := os.Stat(dir); err != nil {
			break
		}
		if owned != nil && !owned(dir) {
			break
		}
		n = slot
	}
	return n
}

func RegistrationToken(repo string) (string, error) {
	return util.Sh("gh", []string{
		"api", "-X", "POST",
		"repos/" + repo + "/actions/runners/registration-token",
		"--jq", ".token",
	})
}

func RemovalToken(repo string) (string, error) {
	return util.Sh("gh", []string{
		"api", "-X", "POST",
		"repos/" + repo + "/actions/runners/remove-token",
		"--jq", ".token",
	})
}

type ConfigureOpts struct {
	Repo    string
	Slot    int
	Name    string
	Labels  []string
	DistDir string
	// Ephemeral registers a runner that accepts exactly one job and then
	// deregisters itself. The autoscaler relies on this: shrinking the pool
	// leaves no stale registrations behind on GitHub.
	Ephemeral bool
}

// ClearRegistration deletes an instance's local registration files so it can
// be configured again. An ephemeral runner removes itself from GitHub after
// its job but leaves these behind, and config.sh refuses to reconfigure over
// them. The _work tree is deliberately kept — warm checkouts and caches are
// most of the speed advantage of a self-hosted runner.
func ClearRegistration(repo string, slot int) {
	dir := Dir(repo, slot)
	for _, name := range []string{".runner", ".credentials", ".credentials_rsaparams"} {
		os.Remove(filepath.Join(dir, name))
	}
}

// Configure sets up a runner instance for a repo slot (idempotent via
// --replace).
func Configure(opts ConfigureOpts) (string, error) {
	if err := util.EnsureDirs(); err != nil {
		return "", err
	}
	dir := Dir(opts.Repo, opts.Slot)
	if _, err := os.Stat(filepath.Join(dir, "config.sh")); err != nil {
		os.RemoveAll(dir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		// cp -R (not os.CopyFS): the dist tree contains symlinks and
		// executable bits that must survive the copy.
		if _, err := util.Sh("cp", []string{"-R", opts.DistDir + "/.", dir}); err != nil {
			return "", err
		}
		os.Remove(filepath.Join(dir, ".complete"))
	}
	token, err := RegistrationToken(opts.Repo)
	if err != nil {
		return "", err
	}
	args := []string{
		"--url", "https://github.com/" + opts.Repo,
		"--token", token,
		"--name", opts.Name,
		"--labels", strings.Join(opts.Labels, ","),
		"--work", "_work",
		"--unattended",
		"--replace",
	}
	if opts.Ephemeral {
		args = append(args, "--ephemeral")
	}
	if _, err := util.Sh("./config.sh", args, util.Opt{Dir: dir, QuietStderr: true}); err != nil {
		return "", err
	}
	return dir, nil
}

func InstallService(repo string, slot int) (string, error) {
	label := util.RunnerLabel(repo, slot)
	dir := Dir(repo, slot)
	err := service.Get().Install(service.Spec{
		Label:       label,
		ProgramArgs: []string{filepath.Join(dir, "run.sh")},
		WorkingDir:  dir,
		LogPath:     filepath.Join(util.LogsDir(), label+".log"),
		KeepAlive:   true,
	})
	if err != nil {
		return "", err
	}
	return label, nil
}

func UninstallService(repo string, slot int) {
	service.Get().Uninstall(util.RunnerLabel(repo, slot))
}

// Deregister removes a slot's runner from GitHub and deletes its local
// instance.
func Deregister(repo string, slot int) {
	dir := Dir(repo, slot)
	if _, err := os.Stat(filepath.Join(dir, "config.sh")); err == nil {
		token, ok := util.TrySh("gh", []string{
			"api", "-X", "POST",
			"repos/" + repo + "/actions/runners/remove-token",
			"--jq", ".token",
		})
		if ok && token != "" {
			// Repo may be gone or token rejected; still clean up locally.
			util.Sh("./config.sh", []string{"remove", "--token", token}, util.Opt{Dir: dir, QuietStderr: true})
		}
	}
	os.RemoveAll(dir)
}

type RemoteRunner struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Busy   bool   `json:"busy"`
}

// Remote returns GitHub's view of the runners registered to a repo, or nil
// when the API is unreachable (no access / network down).
func Remote(repo string) []RemoteRunner {
	var data struct {
		Runners []RemoteRunner `json:"runners"`
	}
	if err := util.GhJSON(&data, "repos/"+repo+"/actions/runners"); err != nil {
		return nil
	}
	if data.Runners == nil {
		return []RemoteRunner{}
	}
	return data.Runners
}

func FindRemote(runners []RemoteRunner, name string) *RemoteRunner {
	for i := range runners {
		if runners[i].Name == name {
			return &runners[i]
		}
	}
	return nil
}

// ListenerRunning reports whether the Runner.Listener process for this repo
// slot is alive locally.
func ListenerRunning(repo string, slot int) bool {
	out, ok := util.TrySh("pgrep", []string{"-fl", "Runner.Listener"})
	if !ok {
		return false
	}
	dir := Dir(repo, slot)
	for _, line := range strings.Split(out, "\n") {
		if listenerMatches(line, dir) {
			return true
		}
	}
	return false
}

// listenerMatches reports whether a pgrep line belongs to the instance rooted
// at dir. It matches path components rather than substrings: slot 1's
// directory is a prefix of every other slot's ("…/owner__repo" vs
// "…/owner__repo-2"), so a substring test would report slot 1 as running
// whenever any slot of that repo was.
func listenerMatches(psLine, dir string) bool {
	for _, field := range strings.Fields(psLine) {
		if field == dir || strings.HasPrefix(field, dir+"/") {
			return true
		}
	}
	return false
}
