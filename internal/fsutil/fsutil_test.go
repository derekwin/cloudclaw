package fsutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCopyDirCreatesDestinationWhenSourceMissing(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "missing")
	dst := filepath.Join(base, "dst")

	if err := CopyDir(src, dst); err != nil {
		t.Fatalf("CopyDir should create destination for missing source: %v", err)
	}
	if info, err := os.Stat(dst); err != nil || !info.IsDir() {
		t.Fatalf("expected destination dir to exist, err=%v info=%v", err, info)
	}
}

func TestCopyDirCopiesFilesAndSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior varies on windows")
	}

	base := t.TempDir()
	src := filepath.Join(base, "src")
	dst := filepath.Join(base, "dst")
	if err := os.MkdirAll(filepath.Join(src, "nested"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}

	realFile := filepath.Join(src, "nested", "a.txt")
	if err := os.WriteFile(realFile, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := os.Symlink("nested/a.txt", filepath.Join(src, "link.txt")); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	if err := CopyDir(src, dst); err != nil {
		t.Fatalf("CopyDir failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dst, "nested", "a.txt"))
	if err != nil {
		t.Fatalf("read copied file: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("unexpected copied data: %q", string(data))
	}

	linkPath := filepath.Join(dst, "link.txt")
	info, err := os.Lstat(linkPath)
	if err != nil {
		t.Fatalf("lstat copied link: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected symlink, got mode=%v", info.Mode())
	}
	target, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("readlink copied link: %v", err)
	}
	if target != "nested/a.txt" {
		t.Fatalf("unexpected symlink target: %q", target)
	}
}

func TestCopyDirRejectsFileSource(t *testing.T) {
	base := t.TempDir()
	srcFile := filepath.Join(base, "source.txt")
	dst := filepath.Join(base, "dst")
	if err := os.WriteFile(srcFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	if err := CopyDir(srcFile, dst); err == nil {
		t.Fatal("expected error when source is not a directory")
	}
}

func TestCopyDirRejectsDestinationInsideSource(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	dst := filepath.Join(src, "nested", "dst")
	if err := CopyDir(src, dst); err == nil {
		t.Fatal("expected error when destination is inside source")
	}
}

func TestCopyDirNoopWhenSourceEqualsDestination(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if err := CopyDir(src, src); err != nil {
		t.Fatalf("expected noop when src == dst, got: %v", err)
	}
}

func TestRemoveAndRecreateClearsDirectory(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "work")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "temp.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if err := RemoveAndRecreate(dir); err != nil {
		t.Fatalf("RemoveAndRecreate failed: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected empty dir after recreate, got %d entries", len(entries))
	}
}
