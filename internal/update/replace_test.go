package update

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveExecutableDirectSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "versions", "lw")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "lw")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	exe, err := ResolveExecutable(link)
	if err != nil {
		t.Fatal(err)
	}
	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if exe.InvocationPath != link || exe.ResolvedPath != resolvedTarget || exe.Manager != ManagerDirect {
		t.Fatalf("unexpected executable: %+v", exe)
	}
}

func TestResolveExecutableDetectsHomebrew(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "Cellar", "local-worker", "0.2.0", "bin", "lw")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "bin", "lw")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	exe, err := ResolveExecutable(link)
	if err != nil {
		t.Fatal(err)
	}
	if exe.Manager != ManagerHomebrew {
		t.Fatalf("Manager = %q", exe.Manager)
	}
}

func TestReplaceCommitsAtomicallyAndPreservesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("old"), 0o750); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "lw")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	exe, err := ResolveExecutable(link)
	if err != nil {
		t.Fatal(err)
	}
	if err := Replace(context.Background(), exe, []byte("new"), "0.3.0", func(context.Context, string) (string, error) {
		return "0.3.0", nil
	}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Fatalf("target = %q", got)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink was not preserved: info=%v err=%v", info, err)
	}
	if info, err := os.Stat(target); err != nil || info.Mode().Perm() != 0o750 {
		t.Fatalf("mode = %v err=%v", info.Mode().Perm(), err)
	}
}

func TestReplaceFailurePreservesOldBinary(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "lw")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	exe, err := ResolveExecutable(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := Replace(context.Background(), exe, []byte("new"), "0.3.0", func(context.Context, string) (string, error) {
		return "0.2.0", nil
	}); err == nil {
		t.Fatal("Replace() expected validation error")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old" {
		t.Fatalf("target changed after failure: %q", got)
	}
}
