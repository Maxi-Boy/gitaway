package gitx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// statusMap возвращает "XY" по каждому пути рабочей папки.
func statusMap(t *testing.T, r *Runner, dir string) map[string]string {
	t.Helper()
	st, err := r.Status(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	for _, c := range st.Changes {
		m[c.Path] = c.X + c.Y
	}
	return m
}

func TestCleanPaths(t *testing.T) {
	good := map[string]string{"a.txt": "a.txt", "sub/./b": "sub/b", "sub/../c": "c", ".": ".", "*.go": "*.go"}
	for in, want := range good {
		got, err := CleanPaths([]string{in})
		if err != nil || got[0] != want {
			t.Errorf("CleanPaths(%q) = %v, %v; want %q", in, got, err, want)
		}
	}
	bad := []string{"", "/etc/passwd", "..", "../x", "a/../../x", ".git", "sub/.git/config", "a\\b", "a\x00b"}
	for _, p := range bad {
		var ie *InputError
		if _, err := CleanPaths([]string{p}); !errors.As(err, &ie) {
			t.Errorf("CleanPaths(%q) must fail with InputError, got %v", p, err)
		}
	}
	if _, err := CleanPaths(nil); err == nil {
		t.Error("empty list must fail")
	}
	if _, err := CleanPaths(make([]string, MaxPaths+1)); err == nil {
		t.Error("too many paths must fail")
	}
}

func TestStageUnstageCommit(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	dir := newRepo(t, r)
	writeFile(t, filepath.Join(dir, "README"), "changed\n")
	writeFile(t, filepath.Join(dir, "new.txt"), "new\n")
	writeFile(t, filepath.Join(dir, "*.txt"), "star\n") // имя со звёздочкой: не шаблон

	if err := r.Stage(ctx, dir, []string{"*.txt"}); err != nil {
		t.Fatal(err)
	}
	m := statusMap(t, r, dir)
	if m["*.txt"] != "A " || m["new.txt"] != "??" {
		t.Fatalf("literal pathspec: %v", m)
	}

	if err := r.Stage(ctx, dir, []string{"."}); err != nil {
		t.Fatal(err)
	}
	m = statusMap(t, r, dir)
	if m["README"] != "M " || m["new.txt"] != "A " {
		t.Fatalf("after stage all: %v", m)
	}

	if err := r.Unstage(ctx, dir, []string{"new.txt"}); err != nil {
		t.Fatal(err)
	}
	if m = statusMap(t, r, dir); m["new.txt"] != "??" || m["README"] != "M " {
		t.Fatalf("after unstage: %v", m)
	}

	if _, _, err := r.Commit(ctx, dir, "   "); err == nil {
		t.Error("empty message must fail")
	}
	hash, _, err := r.Commit(ctx, dir, "-m --amend\n\nтело")
	if err != nil {
		t.Fatal(err)
	}
	if len(hash) < 40 {
		t.Errorf("hash = %q", hash)
	}
	if msg := mustGit(t, r, dir, "log", "-1", "--format=%B"); msg != "-m --amend\n\nтело" {
		t.Errorf("message = %q", msg)
	}
	if n := mustGit(t, r, dir, "rev-list", "--count", "HEAD"); n != "2" {
		t.Errorf("commits = %s, the message must not act as --amend", n)
	}
	if m = statusMap(t, r, dir); len(m) != 1 || m["new.txt"] != "??" {
		t.Errorf("after commit: %v", m)
	}

	var ie *InputError
	if _, _, err := r.Commit(ctx, dir, "nothing"); !errors.As(err, &ie) {
		t.Errorf("commit without staged changes: %v", err)
	}
	if err := r.Stage(ctx, dir, []string{"../outside"}); !errors.As(err, &ie) {
		t.Errorf("stage outside: %v", err)
	}
}

func TestUnstageBeforeFirstCommit(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	dir := t.TempDir()
	mustGit(t, r, dir, "init", "-q")
	writeFile(t, filepath.Join(dir, "a.txt"), "a\n")
	if err := r.Stage(ctx, dir, []string{"a.txt"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Unstage(ctx, dir, []string{"a.txt"}); err != nil {
		t.Fatal(err)
	}
	if m := statusMap(t, r, dir); m["a.txt"] != "??" {
		t.Errorf("status: %v", m)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); err != nil {
		t.Errorf("file must stay on disk: %v", err)
	}
}

func TestDiscard(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	dir := newRepo(t, r)
	writeFile(t, filepath.Join(dir, "keep.txt"), "keep\n")
	writeFile(t, filepath.Join(dir, "gone.txt"), "gone\n")
	commitAll(t, r, dir, "two files")

	writeFile(t, filepath.Join(dir, "README"), "edited\n")
	mustGit(t, r, dir, "add", "README")
	writeFile(t, filepath.Join(dir, "README"), "edited twice\n") // и в индексе, и на диске
	if err := os.Remove(filepath.Join(dir, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "added.txt"), "added\n")
	mustGit(t, r, dir, "add", "added.txt")
	writeFile(t, filepath.Join(dir, "tmp", "untracked.txt"), "u\n")
	writeFile(t, filepath.Join(dir, "keep.txt"), "not selected\n")
	writeFile(t, filepath.Join(dir, ".gitignore"), "*.log\n")
	writeFile(t, filepath.Join(dir, "tmp", "debug.log"), "ignored\n")

	touched, err := r.Discard(ctx, dir, []string{"README", "gone.txt", "added.txt", "tmp"})
	if err != nil {
		t.Fatal(err)
	}
	if len(touched) != 4 {
		t.Errorf("touched = %v", touched)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "README")); string(b) != "hello\n" {
		t.Errorf("README = %q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "gone.txt")); err != nil {
		t.Errorf("deleted file must be restored: %v", err)
	}
	for _, p := range []string{"added.txt", "tmp/untracked.txt"} {
		if _, err := os.Stat(filepath.Join(dir, p)); !os.IsNotExist(err) {
			t.Errorf("%s must be removed: %v", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "tmp", "debug.log")); err != nil {
		t.Errorf("ignored file must stay: %v", err)
	}
	m := statusMap(t, r, dir)
	if len(m) != 2 || m["keep.txt"] != " M" || m[".gitignore"] != "??" {
		t.Errorf("status after discard: %v", m)
	}

	// ничего не изменено: пустой список, не ошибка
	touched, err = r.Discard(ctx, dir, []string{"README"})
	if err != nil || len(touched) != 0 {
		t.Errorf("clean file: %v %v", touched, err)
	}
}

func TestFileDiff(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	dir := newRepo(t, r)
	writeFile(t, filepath.Join(dir, "README"), "staged\n")
	mustGit(t, r, dir, "add", "README")
	writeFile(t, filepath.Join(dir, "README"), "on disk\n")
	writeFile(t, filepath.Join(dir, "new.txt"), "brand new\n")

	d, err := r.FileDiff(ctx, dir, "README", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d.Text, "-staged") || !strings.Contains(d.Text, "+on disk") {
		t.Errorf("worktree diff:\n%s", d.Text)
	}
	d, err = r.FileDiff(ctx, dir, "README", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d.Text, "-hello") || !strings.Contains(d.Text, "+staged") {
		t.Errorf("staged diff:\n%s", d.Text)
	}
	d, err = r.FileDiff(ctx, dir, "new.txt", false)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Untracked || !strings.Contains(d.Text, "+brand new") {
		t.Errorf("untracked diff: %+v", d)
	}
	if d, err = r.FileDiff(ctx, dir, "new.txt", true); err != nil || d.Text != "" {
		t.Errorf("untracked staged diff: %+v %v", d, err)
	}
	if _, err := r.FileDiff(ctx, dir, ".", false); err == nil {
		t.Error("diff of the whole tree must fail")
	}
}
