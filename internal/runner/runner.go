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

func Dir(repo string) string {
	return filepath.Join(util.RunnersDir(), util.RepoID(repo))
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
	Name    string
	Labels  []string
	DistDir string
}

// Configure sets up a runner instance for a repo (idempotent via --replace).
func Configure(opts ConfigureOpts) (string, error) {
	if err := util.EnsureDirs(); err != nil {
		return "", err
	}
	dir := Dir(opts.Repo)
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
	_, err = util.Sh("./config.sh", []string{
		"--url", "https://github.com/" + opts.Repo,
		"--token", token,
		"--name", opts.Name,
		"--labels", strings.Join(opts.Labels, ","),
		"--work", "_work",
		"--unattended",
		"--replace",
	}, util.Opt{Dir: dir, QuietStderr: true})
	if err != nil {
		return "", err
	}
	return dir, nil
}

func InstallService(repo string) (string, error) {
	label := util.RunnerLabel(repo)
	dir := Dir(repo)
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

func UninstallService(repo string) {
	service.Get().Uninstall(util.RunnerLabel(repo))
}

// Deregister removes the runner from GitHub and deletes the local instance.
func Deregister(repo string) {
	dir := Dir(repo)
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
// is alive locally.
func ListenerRunning(repo string) bool {
	out, ok := util.TrySh("pgrep", []string{"-fl", "Runner.Listener"})
	if !ok {
		return false
	}
	dir := Dir(repo)
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, dir) {
			return true
		}
	}
	return false
}
