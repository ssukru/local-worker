// Package util holds paths, subprocess helpers and small shared utilities —
// the Go port of src/util.js.
package util

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"golang.org/x/term"
)

const WatchdogLabel = "com.local-worker.watchdog"

func Home() string {
	h, _ := os.UserHomeDir()
	return h
}

func Root() string       { return filepath.Join(Home(), ".local-worker") }
func RunnersDir() string { return filepath.Join(Root(), "runners") }
func LogsDir() string    { return filepath.Join(Root(), "logs") }
func ConfigPath() string { return filepath.Join(Root(), "config.json") }

func EnsureDirs() error {
	for _, dir := range []string{Root(), RunnersDir(), LogsDir()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// Opt tweaks how a command is run.
type Opt struct {
	Dir           string
	Env           []string // extra KEY=VALUE entries appended to the environment
	InheritStdout bool     // stream stdout to the terminal instead of capturing
	QuietStderr   bool     // discard stderr instead of streaming it
}

func command(name string, args []string, opt Opt) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.Dir = opt.Dir
	if len(opt.Env) > 0 {
		cmd.Env = append(os.Environ(), opt.Env...)
	}
	return cmd
}

// Sh runs a command and returns its trimmed stdout; the error carries stderr
// context. Mirrors sh() in src/util.js (stderr streams to the parent).
func Sh(name string, args []string, opts ...Opt) (string, error) {
	var opt Opt
	if len(opts) > 0 {
		opt = opts[0]
	}
	cmd := command(name, args, opt)
	if opt.QuietStderr {
		cmd.Stderr = nil
	} else {
		cmd.Stderr = os.Stderr
	}
	if opt.InheritStdout {
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
		}
		return "", nil
	}
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// TrySh runs a command quietly; ok is false on any failure. Mirrors trySh()
// (which returned null on failure — empty output and failure are distinct).
func TrySh(name string, args []string, opts ...Opt) (string, bool) {
	var opt Opt
	if len(opts) > 0 {
		opt = opts[0]
	}
	opt.QuietStderr = true
	out, err := Sh(name, args, opt)
	if err != nil {
		return "", false
	}
	return out, true
}

// Run executes a command with inherited stdio (interactive).
func Run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

// GhJSON calls `gh api ...` and decodes the JSON response into v.
func GhJSON(v any, args ...string) error {
	out, err := Sh("gh", append([]string{"api"}, args...))
	if err != nil {
		return err
	}
	if out == "" {
		return nil
	}
	return json.Unmarshal([]byte(out), v)
}

func GhAuthed() bool {
	_, ok := TrySh("gh", []string{"auth", "status"})
	return ok
}

func GhLogin() string {
	login, _ := TrySh("gh", []string{"api", "user", "--jq", ".login"})
	return login
}

var unsafeChars = regexp.MustCompile(`[^a-zA-Z0-9-]`)

// RepoID turns "owner/repo" into a filesystem/service-safe id like "owner__repo".
func RepoID(repo string) string {
	return unsafeChars.ReplaceAllString(repo, "__")
}

func RunnerLabel(repo string) string {
	return "com.local-worker.runner." + RepoID(repo)
}

// MachineArch reports arm64/x64 as used in runner asset names. A native Go
// binary can't be under Rosetta the way Node could, but keep the sysctl
// check for a darwin/amd64 build running on Apple Silicon.
func MachineArch() string {
	if runtime.GOOS == "darwin" && runtime.GOARCH != "arm64" {
		if out, ok := TrySh("sysctl", []string{"-n", "hw.optional.arm64"}); ok && out == "1" {
			return "arm64"
		}
		return "x64"
	}
	if runtime.GOARCH == "arm64" {
		return "arm64"
	}
	return "x64"
}

// RunnerOsSlug is the OS slug used in actions/runner release asset names.
func RunnerOsSlug() string {
	if runtime.GOOS == "darwin" {
		return "osx"
	}
	return "linux"
}

// OsLabel is the human label GitHub auto-assigns to runners on this OS.
func OsLabel() string {
	if runtime.GOOS == "darwin" {
		return "macOS"
	}
	return "Linux"
}

func IsTTY() bool {
	return term.IsTerminal(int(os.Stdout.Fd())) && term.IsTerminal(int(os.Stdin.Fd()))
}

func Timestamp() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
}
