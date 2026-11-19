package update

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Manager string

const (
	ManagerDirect   Manager = "direct"
	ManagerHomebrew Manager = "homebrew"
)

type Executable struct {
	InvocationPath string
	ResolvedPath   string
	Manager        Manager
	Mode           os.FileMode
	Info           os.FileInfo
}

func ResolveExecutable(argv0 string) (Executable, error) {
	invocation, err := invocationPath(argv0)
	if err != nil {
		return Executable{}, err
	}
	resolved, err := filepath.EvalSymlinks(invocation)
	if err != nil {
		return Executable{}, fmt.Errorf("resolve executable path %s: %w", invocation, err)
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return Executable{}, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return Executable{}, fmt.Errorf("inspect executable %s: %w", resolved, err)
	}
	if !info.Mode().IsRegular() {
		return Executable{}, fmt.Errorf("executable target is not a regular file: %s", resolved)
	}
	manager := ManagerDirect
	if IsHomebrewPath(resolved) {
		manager = ManagerHomebrew
	}
	return Executable{InvocationPath: invocation, ResolvedPath: resolved, Manager: manager, Mode: info.Mode(), Info: info}, nil
}

func IsHomebrewPath(value string) bool {
	parts := strings.Split(filepath.Clean(value), string(filepath.Separator))
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == "Cellar" && parts[i+1] == "local-worker" {
			return true
		}
	}
	return false
}

func invocationPath(argv0 string) (string, error) {
	if argv0 == "" {
		return os.Executable()
	}
	value := argv0
	if !strings.ContainsRune(value, filepath.Separator) {
		resolved, err := exec.LookPath(value)
		if err != nil {
			return "", fmt.Errorf("locate executable %s: %w", value, err)
		}
		value = resolved
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	return filepath.Clean(absolute), nil
}
