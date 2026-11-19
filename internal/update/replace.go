package update

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type VersionValidator func(context.Context, string) (string, error)

func Replace(ctx context.Context, executable Executable, binary []byte, expectedVersion string, validate VersionValidator) error {
	if executable.Manager == ManagerHomebrew {
		return fmt.Errorf("lw is managed by Homebrew")
	}
	current, err := os.Stat(executable.ResolvedPath)
	if err != nil {
		return fmt.Errorf("recheck executable %s: %w", executable.ResolvedPath, err)
	}
	if executable.Info != nil && (!os.SameFile(executable.Info, current) || executable.Info.Size() != current.Size() || !executable.Info.ModTime().Equal(current.ModTime())) {
		return fmt.Errorf("lw changed while the update was being prepared; retry the update")
	}
	if current.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return fmt.Errorf("refusing to replace an executable with special permission bits")
	}
	if stat, ok := current.Sys().(*syscall.Stat_t); ok && int(stat.Uid) != os.Geteuid() {
		return fmt.Errorf("refusing to replace lw owned by uid %d", stat.Uid)
	}
	if validate == nil {
		validate = BinaryVersion
	}
	dir := filepath.Dir(executable.ResolvedPath)
	staged, err := os.CreateTemp(dir, ".lw-update-*")
	if err != nil {
		return fmt.Errorf("stage update beside %s: %w", executable.ResolvedPath, err)
	}
	stagedPath := staged.Name()
	committed := false
	defer func() {
		_ = staged.Close()
		if !committed {
			_ = os.Remove(stagedPath)
		}
	}()

	mode := executable.Mode.Perm()
	if mode&0o111 == 0 {
		mode = 0o755
	}
	if err := staged.Chmod(mode); err != nil {
		return fmt.Errorf("set staged executable mode: %w", err)
	}
	if _, err := staged.Write(binary); err != nil {
		return fmt.Errorf("write staged executable: %w", err)
	}
	if err := staged.Sync(); err != nil {
		return fmt.Errorf("sync staged executable: %w", err)
	}
	if err := staged.Close(); err != nil {
		return fmt.Errorf("close staged executable: %w", err)
	}

	actualVersion, err := validate(ctx, stagedPath)
	if err != nil {
		return fmt.Errorf("validate staged executable: %w", err)
	}
	expected, err := DisplayVersion(expectedVersion)
	if err != nil {
		return err
	}
	actual, err := DisplayVersion(actualVersion)
	if err != nil {
		return fmt.Errorf("staged executable reported %q: %w", actualVersion, err)
	}
	if actual != expected {
		return fmt.Errorf("staged executable reports %s, expected %s", actual, expected)
	}
	if err := os.Rename(stagedPath, executable.ResolvedPath); err != nil {
		return fmt.Errorf("replace %s: %w", executable.ResolvedPath, err)
	}
	committed = true
	if directory, err := os.Open(dir); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return nil
}

func BinaryVersion(ctx context.Context, path string) (string, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
	}
	out, err := exec.CommandContext(ctx, path, "version").Output()
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(out))
	value = strings.TrimPrefix(value, "lw ")
	if value == "" {
		return "", fmt.Errorf("empty version output")
	}
	return value, nil
}
