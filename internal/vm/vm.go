// Package vm implements Tart-based VM isolation (macOS Apple Silicon hosts
// only) — the Go port of src/vm.js.
//
// One daemon per repo owns the VM lifecycle and unifies every refresh policy:
//
//	loop {
//	  recreate VM from base image if missing or refresh policy says it's due
//	  boot it, wait for SSH
//	  provision the actions runner inside (idempotent)
//	  register (ephemeral for per-job policy) and block on run.sh
//	  count the job; per-job policy deletes the VM here
//	}
//
// The daemon itself runs under the same service manager as plain runners, so
// KeepAlive/Restart=always revives it and `lw remove` tears it down.
package vm

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/ssukru/local-worker/internal/config"
	"github.com/ssukru/local-worker/internal/runner"
	"github.com/ssukru/local-worker/internal/util"
)

var DefaultImages = map[string]string{
	"macos-base":  "ghcr.io/cirruslabs/macos-sequoia-base:latest",
	"macos-xcode": "ghcr.io/cirruslabs/macos-sequoia-xcode:latest",
	"ubuntu":      "ghcr.io/cirruslabs/ubuntu:latest",
}

// cirruslabs images ship admin/admin
const sshUser = "admin"
const sshPass = "admin"

// Name is the Tart VM name backing a repo's Nth runner slot — one VM per
// slot, so slots stay isolated from each other as well as from the host.
func Name(repo string, slot int) string {
	return "lw-" + strings.ToLower(util.InstanceID(repo, slot))
}

func TartAvailable() bool {
	_, ok := util.TrySh("tart", []string{"--version"})
	return ok
}

func sshpassAvailable() bool {
	_, ok := util.TrySh("sshpass", []string{"-V"})
	return ok
}

func AssertSupport() error {
	if runtime.GOOS != "darwin" || util.MachineArch() != "arm64" {
		return errors.New("VM isolation needs an Apple Silicon Mac (Tart).")
	}
	if !TartAvailable() {
		return errors.New("Tart is not installed — brew install cirruslabs/cli/tart")
	}
	if !sshpassAvailable() {
		return errors.New("sshpass is not installed — brew install sshpass")
	}
	return nil
}

// ---------------------------------------------------------------- tart layer

type tartVM struct {
	Name  string `json:"Name"`
	State string `json:"State"`
}

func tartList() []tartVM {
	out, ok := util.TrySh("tart", []string{"list", "--format", "json"})
	if !ok || out == "" {
		return nil
	}
	var vms []tartVM
	if err := json.Unmarshal([]byte(out), &vms); err != nil {
		return nil
	}
	return vms
}

func Exists(name string) bool {
	for _, v := range tartList() {
		if v.Name == name {
			return true
		}
	}
	return false
}

func Running(name string) bool {
	for _, v := range tartList() {
		if v.Name == name && v.State == "running" {
			return true
		}
	}
	return false
}

func Delete(name string) {
	if !Exists(name) {
		return
	}
	if Running(name) {
		util.TrySh("tart", []string{"stop", name, "--timeout", "30"})
	}
	util.TrySh("tart", []string{"delete", name})
}

func clone(name, image string) error {
	// Pulls the base image on first use — can be 2 GB (Linux) to 60+ GB (Xcode).
	_, err := util.Sh("tart", []string{"clone", image, name}, util.Opt{InheritStdout: true})
	return err
}

func boot(name string) error {
	cmd := exec.Command("tart", []string{"run", name, "--no-graphics"}...)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// ----------------------------------------------------------------- ssh layer

func sshArgs(ip, cmd string) []string {
	return []string{
		"-p", sshPass, "ssh",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"-o", "ConnectTimeout=10",
		sshUser + "@" + ip,
		cmd,
	}
}

func sshTry(ip, cmd string) (string, bool) {
	return util.TrySh("sshpass", sshArgs(ip, cmd))
}

// sshRun blocks with output going to the daemon log.
func sshRun(ip, cmd string) error {
	_, err := util.Sh("sshpass", sshArgs(ip, cmd), util.Opt{InheritStdout: true})
	return err
}

func waitForSsh(name string, log func(string)) (string, error) {
	for i := 0; i < 60; i++ {
		if ip, ok := util.TrySh("tart", []string{"ip", name}); ok && ip != "" {
			if _, ok := sshTry(ip, "true"); ok {
				return ip, nil
			}
		}
		if i == 10 {
			log("still waiting for " + name + " to become reachable…")
		}
		time.Sleep(5 * time.Second)
	}
	return "", fmt.Errorf("VM %s did not become reachable over SSH", name)
}

// -------------------------------------------------------------- provisioning

type provisionOpts struct {
	repo      string
	name      string
	labels    []string
	ephemeral bool
}

var archRe = regexp.MustCompile(`arm|aarch`)

// provisionRunner installs the runner inside the VM (idempotent) and
// configures it.
func provisionRunner(ip string, opts provisionOpts) error {
	unameOut, _ := sshTry(ip, "uname -sm")
	fields := strings.Fields(unameOut)
	osName, cpu := "", ""
	if len(fields) > 0 {
		osName = fields[0]
	}
	if len(fields) > 1 {
		cpu = fields[1]
	}
	slugOs := "linux"
	if osName == "Darwin" {
		slugOs = "osx"
	}
	slugArch := "x64"
	if archRe.MatchString(cpu) {
		slugArch = "arm64"
	}
	asset, err := runner.LatestAsset(slugOs + "-" + slugArch)
	if err != nil {
		return err
	}

	if err := sshRun(ip,
		`set -e; mkdir -p runner; cd runner; `+
			`[ -x config.sh ] || { curl -fsSL -o r.tgz "`+asset.URL+`"; tar xzf r.tgz; rm r.tgz; }`,
	); err != nil {
		return err
	}

	// Persistent VMs stay configured across daemon restarts; ephemeral runners
	// deregister themselves after each job, so configure every cycle.
	if already, _ := sshTry(ip, "test -f runner/.runner && echo yes"); already == "yes" {
		if !opts.ephemeral {
			return nil
		}
		// A locally-configured runner refuses reconfiguration — unconfigure
		// first, force-cleaning if the graceful remove fails.
		if rt, err := runner.RemovalToken(opts.repo); err == nil {
			sshTry(ip, `cd runner && ./config.sh remove --token "`+rt+`"`)
		}
		sshTry(ip, "cd runner && rm -f .runner .credentials .credentials_rsaparams")
	}

	token, err := runner.RegistrationToken(opts.repo)
	if err != nil {
		return err
	}
	cmd := `cd runner && ./config.sh --url "https://github.com/` + opts.repo + `" --token "` + token + `" ` +
		`--name "` + opts.name + `" --labels "` + strings.Join(opts.labels, ",") + `" --work _work --unattended --replace`
	if opts.ephemeral {
		cmd += " --ephemeral"
	}
	return sshRun(ip, cmd)
}

// -------------------------------------------------------------------- daemon

// Daemon is the long-running daemon owning one repo slot's VM; stdout/stderr
// go to the service log. Slots run as independent daemons, one service each.
func Daemon(repo string, slot int) error {
	prefix := ""
	if slot > 1 {
		prefix = fmt.Sprintf("#%d ", slot)
	}
	log := func(msg string) { fmt.Printf("[%s] %s%s\n", util.Timestamp(), prefix, msg) }
	if err := AssertSupport(); err != nil {
		return err
	}

	for {
		err := daemonCycle(repo, slot, log)
		if err == errNoLongerConfigured {
			return nil
		}
		if err != nil {
			log("daemon error: " + err.Error())
			time.Sleep(30 * time.Second) // don't spin on persistent failures
		}
	}
}

var errNoLongerConfigured = errors.New("no longer configured")

func daemonCycle(repo string, slot int, log func(string)) error {
	cfg := config.Load()
	entry := config.FindRunner(&cfg, repo)
	if entry == nil || entry.VM == nil {
		log(repo + " no longer configured for VM isolation — exiting.")
		return errNoLongerConfigured
	}
	if slot > entry.Slots() {
		log(fmt.Sprintf("%s scaled down to %d runners — slot %d exiting.", repo, entry.Slots(), slot))
		return errNoLongerConfigured
	}
	image, refresh := entry.VM.Image, entry.VM.Refresh
	name := Name(repo, slot)
	state := ReadState(name)

	if !Exists(name) || refreshDue(refresh, state) || (state != nil && state.RecreateRequested) {
		log("(re)creating VM " + name + " from " + image)
		Delete(name)
		if err := clone(name, image); err != nil {
			return err
		}
		state = NewState(image)
		if err := WriteState(name, state); err != nil {
			return err
		}
	}
	if state == nil {
		// Adopt a pre-existing environment (e.g. state file lost).
		state = NewState(image)
		if err := WriteState(name, state); err != nil {
			return err
		}
	}
	if !Running(name) {
		log("booting " + name)
		if err := boot(name); err != nil {
			return err
		}
	}
	ip, err := waitForSsh(name, log)
	if err != nil {
		return err
	}

	// A previous daemon's run.sh may still be alive inside the VM (a broken
	// ssh session doesn't kill it) — stop it before configuring, or
	// config.sh refuses to touch a running runner.
	sshTry(ip, "pkill -f Runner.Listener")

	ephemeral := refresh == "per-job"
	if err := provisionRunner(ip, provisionOpts{
		repo:      repo,
		name:      entry.SlotName(slot),
		labels:    append(append([]string{}, entry.Labels...), "vm"),
		ephemeral: ephemeral,
	}); err != nil {
		return err
	}

	suffix := ""
	if ephemeral {
		suffix = " (ephemeral)"
	}
	log("runner up in " + name + " (" + ip + "), waiting for jobs" + suffix)
	if err := sshRun(ip, "cd runner && ./run.sh"); err != nil {
		return err
	}

	if s := ReadState(name); s != nil {
		state = s
	}
	state.JobsRun++
	WriteState(name, state)

	if ephemeral {
		log("job finished — discarding " + name)
		Delete(name)
	}
	return nil
}

// ForceDeregister removes a VM runner from GitHub by name (the VM may be gone).
func ForceDeregister(repo, runnerName string) {
	remote := runner.Remote(repo)
	if mine := runner.FindRemote(remote, runnerName); mine != nil {
		// Repo gone or offline — nothing to clean remotely.
		util.TrySh("gh", []string{"api", "-X", "DELETE", fmt.Sprintf("repos/%s/actions/runners/%d", repo, mine.ID)})
	}
}
