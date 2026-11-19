package util

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExecutablePathPreservesInvocationSymlink(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	t.Setenv("HOME", home)
	target := filepath.Join(dir, "Cellar", "local-worker", "0.3.0", "bin", "lw")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, ".local", "bin", "lw")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	got, err := ExecutablePath(link)
	if err != nil {
		t.Fatal(err)
	}
	if got != link {
		t.Fatalf("ExecutablePath() = %q, want stable symlink %q", got, link)
	}
}

func TestExecutablePathResolvesNameThroughPATH(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "lw")
	if err := os.WriteFile(binary, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	got, err := ExecutablePath("lw")
	if err != nil {
		t.Fatal(err)
	}
	if got != binary {
		t.Fatalf("ExecutablePath() = %q, want %q", got, binary)
	}
}
