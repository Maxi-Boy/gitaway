package fsx

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// setup создаёт корень с файлами и каталог снаружи, на который ведёт symlink.
func setup(t *testing.T) (*Sandbox, string, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outside, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a", "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("s"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skip("symlinks are not available:", err)
	}
	sb, err := NewSandbox([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	return sb, root, outside
}

func TestResolve(t *testing.T) {
	sb, root, outside := setup(t)
	if got, err := sb.Resolve(filepath.Join(root, "a", "f.txt")); err != nil || got != filepath.Join(root, "a", "f.txt") {
		t.Errorf("inside: got %q, %v", got, err)
	}
	if _, err := sb.Resolve(outside); !errors.Is(err, ErrOutside) {
		t.Errorf("outside: got %v", err)
	}
	if _, err := sb.Resolve(filepath.Join(root, "link", "secret")); !errors.Is(err, ErrOutside) {
		t.Errorf("symlink escape: got %v", err)
	}
	if _, err := sb.Resolve(filepath.Join(root, "a", "..", "..", "etc")); err == nil {
		t.Error("dotdot escape must fail")
	}
	if _, err := sb.Resolve("relative/path"); !errors.Is(err, ErrNotAbsolute) {
		t.Errorf("relative: got %v", err)
	}
	if _, err := sb.Resolve(filepath.Join(root, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing: got %v", err)
	}
}

func TestResolveNew(t *testing.T) {
	sb, root, outside := setup(t)
	want := filepath.Join(root, "a", "new.txt")
	if got, err := sb.ResolveNew(want); err != nil || got != want {
		t.Errorf("new in root: got %q, %v", got, err)
	}
	if _, err := sb.ResolveNew(filepath.Join(outside, "new.txt")); !errors.Is(err, ErrOutside) {
		t.Errorf("new outside: got %v", err)
	}
	if _, err := sb.ResolveNew(filepath.Join(root, "link", "new.txt")); !errors.Is(err, ErrOutside) {
		t.Errorf("new via symlink: got %v", err)
	}
	if _, err := sb.ResolveNew(filepath.Join(root, "nodir", "new.txt")); err == nil {
		t.Error("missing parent must fail")
	}
}

func TestResolveUnder(t *testing.T) {
	sb, root, _ := setup(t)
	base := filepath.Join(root, "a")
	if got, err := sb.ResolveUnder(base, "f.txt", true); err != nil || got != filepath.Join(base, "f.txt") {
		t.Errorf("file: got %q, %v", got, err)
	}
	if got, err := sb.ResolveUnder(base, "", true); err != nil || got != base {
		t.Errorf("empty rel: got %q, %v", got, err)
	}
	if got, err := sb.ResolveUnder(base, "new.txt", false); err != nil || got != filepath.Join(base, "new.txt") {
		t.Errorf("new file: got %q, %v", got, err)
	}
	if _, err := sb.ResolveUnder(base, "../link/secret", true); !errors.Is(err, ErrOutside) {
		t.Errorf("dotdot to symlink: got %v", err)
	}
	if _, err := sb.ResolveUnder(base, "b/../f.txt", true); err != nil {
		t.Errorf("dotdot that stays inside base must be allowed: %v", err)
	}
	if _, err := sb.ResolveUnder(base, "../a/f.txt", true); !errors.Is(err, ErrOutside) {
		t.Errorf("leading dotdot is always rejected: got %v", err)
	}
	if _, err := sb.ResolveUnder(root, "link/secret", true); !errors.Is(err, ErrOutside) {
		t.Errorf("symlink escape: got %v", err)
	}
	if _, err := sb.ResolveUnder(base, "/etc/passwd", true); !errors.Is(err, ErrNotRelative) {
		t.Errorf("absolute rel: got %v", err)
	}
	if _, err := sb.ResolveUnder(base, "../../x", false); !errors.Is(err, ErrOutside) {
		t.Errorf("escape: got %v", err)
	}
}
