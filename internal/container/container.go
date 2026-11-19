// Package container is the Docker-based isolation backend — the Linux
// counterpart of Tart VMs (also usable on macOS hosts that have a Docker
// daemon, for cheap Linux runners). The Go port of src/container.js.
//
// Same refresh policies as VMs: "never" | "per-job" | "<n>h". The daemon loop
// mirrors internal/vm but speaks `docker` instead of `tart`+ssh.
//
// Default image is GitHub's official runner image (runner preinstalled under
// /home/runner). Jobs inside the container cannot run Docker themselves
// unless you opt into mounting the host socket — which largely defeats the
// isolation, so we don't.
package container

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ssukru/local-worker/internal/config"
	"github.com/ssukru/local-worker/internal/runner"
	"github.com/ssukru/local-worker/internal/util"
	"github.com/ssukru/local-worker/internal/vm"
)

const DefaultImage = "ghcr.io/actions/actions-runner:latest"

// Name is the container backing a repo's Nth runner slot — one container per
// slot, so slots stay isolated from each other as well as from the host.
func Name(repo string, slot int) string {
	return "lw-" + strings.ToLower(util.InstanceID(repo, slot))
}

func DockerAvailable() bool {
	out, ok := util.TrySh("docker", []string{"info", "--format", "ok"})
	return ok && out != ""
}

func AssertSupport() error {
	if !DockerAvailable() {
		return errors.New(
			"Container isolation needs a running Docker daemon (Linux: https://get.docker.com, macOS: OrbStack/Colima).")
	}
	return nil
}

func exists(name string) bool {
	out, _ := util.TrySh("docker", []string{"inspect", "--format", "ok", name})
	return out == "ok"
}

func running(name string) bool {
	out, _ := util.TrySh("docker", []string{"inspect", "--format", "{{.State.Running}}", name})
	return out == "true"
}

func Delete(name string) {
	util.TrySh("docker", []string{"rm", "-f", name})
}

func create(name, image string) error {
	_, err := util.Sh("docker", []string{"run", "-d", "--name", name, image, "tail", "-f", "/dev/null"},
		util.Opt{InheritStdout: true})
	return err
}

func execTry(name, cmd string) (string, bool) {
	return util.TrySh("docker", []string{"exec", "-u", "runner", "-w", "/home/runner", name, "bash", "-c", cmd})
}

func execRun(name, cmd string) error {
	_, err := util.Sh("docker", []string{"exec", "-u", "runner", "-w", "/home/runner", name, "bash", "-c", cmd},
		util.Opt{InheritStdout: true})
	return err
}

type configureOpts struct {
	repo       string
	runnerName string
	labels     []string
	ephemeral  bool
}

func configureRunner(name string, opts configureOpts) error {
	if already, _ := execTry(name, "test -f .runner && echo yes"); already == "yes" {
		if !opts.ephemeral {
			return nil
		}
		// A locally-configured runner refuses reconfiguration (--replace only
		// covers the server side) — unconfigure first, force-cleaning if the
		// graceful remove fails.
		if rt, err := runner.RemovalToken(opts.repo); err == nil {
			execTry(name, `./config.sh remove --token "`+rt+`"`)
		}
		execTry(name, "rm -f .runner .credentials .credentials_rsaparams")
	}
	token, err := runner.RegistrationToken(opts.repo)
	if err != nil {
		return err
	}
	cmd := `./config.sh --url "https://github.com/` + opts.repo + `" --token "` + token + `" ` +
		`--name "` + opts.runnerName + `" --labels "` + strings.Join(opts.labels, ",") + `" --work _work --unattended --replace`
	if opts.ephemeral {
		cmd += " --ephemeral"
	}
	return execRun(name, cmd)
}

// Daemon owns one repo slot's container; mirrors vm.Daemon.
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
			time.Sleep(30 * time.Second)
		}
	}
}

var errNoLongerConfigured = errors.New("no longer configured")

func daemonCycle(repo string, slot int, log func(string)) error {
	cfg := config.Load()
	entry := config.FindRunner(&cfg, repo)
	if entry == nil || entry.VM == nil {
		log(repo + " no longer configured for container isolation — exiting.")
		return errNoLongerConfigured
	}
	if slot > entry.Slots() {
		log(fmt.Sprintf("%s scaled down to %d runners — slot %d exiting.", repo, entry.Slots(), slot))
		return errNoLongerConfigured
	}
	image, refresh := entry.VM.Image, entry.VM.Refresh
	name := Name(repo, slot)
	state := vm.ReadState(name)

	if !exists(name) || refreshDue(refresh, state) || (state != nil && state.RecreateRequested) {
		log("(re)creating container " + name + " from " + image)
		Delete(name)
		if err := create(name, image); err != nil {
			return err
		}
		state = vm.NewState(image)
		if err := vm.WriteState(name, state); err != nil {
			return err
		}
	}
	if state == nil {
		// Adopt a pre-existing environment (e.g. state file lost).
		state = vm.NewState(image)
		if err := vm.WriteState(name, state); err != nil {
			return err
		}
	}
	if !running(name) {
		if _, err := util.Sh("docker", []string{"start", name}); err != nil {
			return err
		}
	}

	// A previous daemon's run.sh may still be alive inside the container
	// (killing the docker exec client doesn't kill it) — stop it before
	// configuring, or config.sh refuses to touch a running runner.
	execTry(name, "pkill -f Runner.Listener")

	ephemeral := refresh == "per-job"
	if err := configureRunner(name, configureOpts{
		repo:       repo,
		runnerName: entry.SlotName(slot),
		labels:     append(append([]string{}, entry.Labels...), "container"),
		ephemeral:  ephemeral,
	}); err != nil {
		return err
	}

	suffix := ""
	if ephemeral {
		suffix = " (ephemeral)"
	}
	log("runner up in container " + name + ", waiting for jobs" + suffix)
	if err := execRun(name, "./run.sh"); err != nil {
		return err
	}

	if s := vm.ReadState(name); s != nil {
		state = s
	}
	state.JobsRun++
	vm.WriteState(name, state)

	if ephemeral {
		log("job finished — discarding " + name)
		Delete(name)
	}
	return nil
}

func refreshDue(policy string, state *vm.State) bool {
	if state == nil {
		return false
	}
	hours := vm.ParseRefreshHours(policy)
	if hours < 0 {
		return false
	}
	created, err := time.Parse(time.RFC3339, state.CreatedAt)
	if err != nil {
		return false
	}
	return time.Since(created) > time.Duration(hours*float64(time.Hour))
}
