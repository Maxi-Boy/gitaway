package gitx

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setup изолирует окружение git (домашний каталог, автор коммитов) и возвращает Runner.
func setup(t *testing.T) *Runner {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")
	return NewRunner()
}

func mustGit(t *testing.T, r *Runner, dir string, args ...string) string {
	t.Helper()
	out, err := r.Run(context.Background(), dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commitAll(t *testing.T, r *Runner, dir, msg string) {
	t.Helper()
	mustGit(t, r, dir, "add", "-A")
	mustGit(t, r, dir, "commit", "-q", "-m", msg)
}

// newRepo создаёт репозиторий с веткой main и одним коммитом (файл README).
func newRepo(t *testing.T, r *Runner) string {
	t.Helper()
	dir := t.TempDir()
	mustGit(t, r, dir, "init", "-q")
	mustGit(t, r, dir, "symbolic-ref", "HEAD", "refs/heads/main")
	writeFile(t, filepath.Join(dir, "README"), "hello\n")
	commitAll(t, r, dir, "init")
	return dir
}

// newRemote создаёт bare-репозиторий и репозиторий a, подключённый к нему как origin.
func newRemote(t *testing.T, r *Runner) (remote, a string) {
	t.Helper()
	remote = t.TempDir()
	mustGit(t, r, remote, "init", "-q", "--bare")
	mustGit(t, r, remote, "symbolic-ref", "HEAD", "refs/heads/main")
	a = newRepo(t, r)
	mustGit(t, r, a, "remote", "add", "origin", remote)
	mustGit(t, r, a, "push", "-q", "-u", "origin", "main")
	return remote, a
}

func TestValidBranchName(t *testing.T) {
	good := []string{"main", "feature/login", "fix-123", "release/2.1", "ветка"}
	bad := []string{"", "-x", "a..b", "a b", "a~1", "a:b", "x.lock", "a/", "/a", "@", "a@{1}", "a//b", "a/.hidden", "a?b", "a\\b"}
	for _, s := range good {
		if err := ValidBranchName(s); err != nil {
			t.Errorf("%q must be valid: %v", s, err)
		}
	}
	for _, s := range bad {
		if err := ValidBranchName(s); err == nil {
			t.Errorf("%q must be invalid", s)
		}
	}
}

func TestValidRemoteName(t *testing.T) {
	for _, s := range []string{"origin", "up-stream", "r_1", "a.b"} {
		if err := ValidRemoteName(s); err != nil {
			t.Errorf("%q must be valid: %v", s, err)
		}
	}
	for _, s := range []string{"", "-o", ".", "a b", "a/b"} {
		if err := ValidRemoteName(s); err == nil {
			t.Errorf("%q must be invalid", s)
		}
	}
}

func TestValidCloneURL(t *testing.T) {
	good := []string{"https://github.com/o/r.git", "git@github.com:o/r.git", "ssh://git@host/o/r.git"}
	bad := []string{"", "-oProxyCommand=x", "ext::sh -c x", "file:///etc", "http://x/y", "git@-host:x",
		"https://", "git@host:path with space", "git://host/repo"}
	for _, s := range good {
		if err := ValidCloneURL(s); err != nil {
			t.Errorf("%q must be valid: %v", s, err)
		}
	}
	for _, s := range bad {
		if err := ValidCloneURL(s); err == nil {
			t.Errorf("%q must be invalid", s)
		}
	}
}

func TestRedact(t *testing.T) {
	got := redact("fatal: https://user:secret@github.com/o/r.git failed")
	if strings.Contains(got, "secret") || strings.Contains(got, "user") {
		t.Errorf("credentials leaked: %q", got)
	}
}

func TestParseTrack(t *testing.T) {
	cases := []struct {
		in            string
		ahead, behind int
		gone          bool
	}{
		{"", 0, 0, false},
		{"[ahead 1]", 1, 0, false},
		{"[behind 2]", 0, 2, false},
		{"[ahead 1, behind 2]", 1, 2, false},
		{"[gone]", 0, 0, true},
	}
	for _, c := range cases {
		a, b, g := parseTrack(c.in)
		if a != c.ahead || b != c.behind || g != c.gone {
			t.Errorf("%q: got %d %d %v", c.in, a, b, g)
		}
	}
}

func TestParseStatus(t *testing.T) {
	out := "## main...origin/main [ahead 1, behind 2]\x00 M a.txt\x00?? b.txt\x00R  new.txt\x00old.txt\x00"
	st := parseStatus(out)
	if st.Branch != "main" || st.Upstream != "origin/main" || st.Ahead != 1 || st.Behind != 2 {
		t.Fatalf("branch line: %+v", st)
	}
	if len(st.Changes) != 3 {
		t.Fatalf("changes: %+v", st.Changes)
	}
	if st.Changes[0].Path != "a.txt" || st.Changes[0].Y != "M" {
		t.Errorf("first change: %+v", st.Changes[0])
	}
	if st.Changes[1].X != "?" || st.Changes[1].Path != "b.txt" {
		t.Errorf("untracked: %+v", st.Changes[1])
	}
	if st.Changes[2].Path != "new.txt" {
		t.Errorf("rename must keep the new path: %+v", st.Changes[2])
	}

	if st := parseStatus("## No commits yet on main\x00"); st.Branch != "main" {
		t.Errorf("unborn: %+v", st)
	}
	if st := parseStatus("## HEAD (no branch)\x00"); !st.Detached {
		t.Errorf("detached: %+v", st)
	}
	if st := parseStatus("## feature\x00"); st.Branch != "feature" || st.Upstream != "" {
		t.Errorf("no upstream: %+v", st)
	}
}

func TestParseWorktrees(t *testing.T) {
	out := "worktree /r/main\nHEAD abc\nbranch refs/heads/main\n\n" +
		"worktree /r/feat\nHEAD def\nbranch refs/heads/feature/x\nlocked\n\n" +
		"worktree /r/det\nHEAD 123\ndetached\n"
	list := parseWorktrees(out)
	if len(list) != 3 {
		t.Fatalf("got %+v", list)
	}
	if !list[0].Main || list[0].Branch != "main" {
		t.Errorf("main: %+v", list[0])
	}
	if list[1].Branch != "feature/x" || !list[1].Locked || list[1].Main {
		t.Errorf("feature: %+v", list[1])
	}
	if !list[2].Detached {
		t.Errorf("detached: %+v", list[2])
	}
}

func TestSafeDirName(t *testing.T) {
	cases := map[string]string{
		"feature/login": "feature-login",
		"../x":          "x",
		"ветка":         "branch",
		"v1.2_rc-3":     "v1.2_rc-3",
	}
	for in, want := range cases {
		if got := SafeDirName(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

func TestBranchesAndStatus(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	dir := newRepo(t, r)
	mustGit(t, r, dir, "branch", "feature/x")
	writeFile(t, filepath.Join(dir, "README"), "changed\n")
	writeFile(t, filepath.Join(dir, "new.txt"), "x\n")

	bs, err := r.Branches(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	cur := ""
	names := map[string]bool{}
	for _, b := range bs {
		names[b.Name] = true
		if b.Current {
			cur = b.Name
		}
	}
	if cur != "main" || !names["feature/x"] {
		t.Fatalf("branches: %+v", bs)
	}

	st, err := r.Status(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Branch != "main" || len(st.Changes) != 2 {
		t.Fatalf("status: %+v", st)
	}
	if b, err := r.CurrentBranch(ctx, dir); err != nil || b != "main" {
		t.Fatalf("current branch: %q, %v", b, err)
	}
}

func TestAheadBehindPullPush(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	remote, a := newRemote(t, r)
	b := t.TempDir()
	mustGit(t, r, "", "clone", "-q", remote, b)

	// новый коммит в a уходит на сервер
	writeFile(t, filepath.Join(a, "a.txt"), "1\n")
	commitAll(t, r, a, "a1")
	mustGit(t, r, a, "push", "-q")

	if _, err := r.Fetch(ctx, b, ""); err != nil {
		t.Fatal(err)
	}
	st, err := r.Status(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	if st.Behind != 1 || st.Ahead != 0 {
		t.Fatalf("after fetch: %+v", st)
	}

	if _, err := r.Pull(ctx, b); err != nil {
		t.Fatal(err)
	}
	if st, _ = r.Status(ctx, b); st.Behind != 0 {
		t.Fatalf("after pull: %+v", st)
	}

	writeFile(t, filepath.Join(b, "b.txt"), "2\n")
	commitAll(t, r, b, "b1")
	if st, _ = r.Status(ctx, b); st.Ahead != 1 {
		t.Fatalf("after local commit: %+v", st)
	}

	if _, err := r.Push(ctx, b, PushOptions{}); err != nil {
		t.Fatal(err)
	}
	if st, _ = r.Status(ctx, b); st.Ahead != 0 {
		t.Fatalf("after push: %+v", st)
	}
}

func TestPushRejectsBadArguments(t *testing.T) {
	r := setup(t)
	dir := newRepo(t, r)
	ctx := context.Background()
	if _, err := r.Push(ctx, dir, PushOptions{Remote: "origin"}); err == nil {
		t.Error("remote without branch must fail")
	}
	if _, err := r.Push(ctx, dir, PushOptions{Remote: "origin", Branch: "--delete"}); err == nil {
		t.Error("option-like branch must fail")
	}
	if _, err := r.Fetch(ctx, dir, "-x"); err == nil {
		t.Error("option-like remote must fail")
	}
}

func TestUpdateNonCurrentBranch(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	remote, a := newRemote(t, r)

	// в a создаём ветку feature и отправляем её
	mustGit(t, r, a, "switch", "-c", "feature")
	writeFile(t, filepath.Join(a, "f1.txt"), "1\n")
	commitAll(t, r, a, "f1")
	mustGit(t, r, a, "push", "-q", "-u", "origin", "feature")

	// в b появляется локальная ветка feature, отслеживающая origin/feature
	b := t.TempDir()
	mustGit(t, r, "", "clone", "-q", remote, b)
	mustGit(t, r, b, "branch", "feature", "origin/feature")

	// ещё один коммит в feature на сервере
	writeFile(t, filepath.Join(a, "f2.txt"), "2\n")
	commitAll(t, r, a, "f2")
	mustGit(t, r, a, "push", "-q")

	// b сейчас на main, feature обновляется без переключения
	if _, err := r.UpdateBranch(ctx, b, "feature"); err != nil {
		t.Fatal(err)
	}
	if got, want := mustGit(t, r, b, "rev-parse", "feature"), mustGit(t, r, a, "rev-parse", "HEAD"); got != want {
		t.Fatalf("feature = %s, want %s", got, want)
	}
	if cur := mustGit(t, r, b, "branch", "--show-current"); cur != "main" {
		t.Fatalf("current branch changed to %q", cur)
	}

	if _, err := r.UpdateBranch(ctx, b, "nonexistent"); err == nil {
		t.Error("branch without upstream must fail")
	}
}

func TestWorktrees(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	dir := newRepo(t, r)
	wt := filepath.Join(t.TempDir(), "wt-feature")

	if _, err := r.AddWorktree(ctx, dir, AddWorktreeOptions{Path: wt, Branch: "feature/x", CreateBranch: true}); err != nil {
		t.Fatal(err)
	}
	list, err := r.Worktrees(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || !list[0].Main {
		t.Fatalf("worktrees: %+v", list)
	}
	found := false
	for _, w := range list {
		if w.Branch == "feature/x" && filepath.Base(w.Path) == "wt-feature" {
			found = true
		}
	}
	if !found {
		t.Fatalf("new worktree not listed: %+v", list)
	}

	if _, err := r.RemoveWorktree(ctx, dir, t.TempDir(), false); !errors.Is(err, ErrUnknownWorktree) {
		t.Errorf("unknown path: got %v", err)
	}
	if _, err := r.RemoveWorktree(ctx, dir, list[0].Path, false); err == nil {
		t.Error("main worktree must not be removable")
	}
	if _, err := r.RemoveWorktree(ctx, dir, wt, false); err != nil {
		t.Fatal(err)
	}
	if list, _ = r.Worktrees(ctx, dir); len(list) != 1 {
		t.Fatalf("after remove: %+v", list)
	}
}

func TestLastCommits(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	dir := newRepo(t, r) // README, коммит "init"

	writeFile(t, filepath.Join(dir, "a.txt"), "1\n")
	commitAll(t, r, dir, "add a")
	writeFile(t, filepath.Join(dir, "b.txt"), "1\n")
	commitAll(t, r, dir, "add b")
	writeFile(t, filepath.Join(dir, "a.txt"), "2\n")
	commitAll(t, r, dir, "change a")
	writeFile(t, filepath.Join(dir, "sub", "c.txt"), "1\n")
	commitAll(t, r, dir, "add c")
	writeFile(t, filepath.Join(dir, "untracked.txt"), "u\n")

	cache := NewCommitCache()
	got, err := cache.Get(ctx, r, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"README": "init", "a.txt": "change a", "b.txt": "add b", "sub": "add c"}
	for name, subject := range want {
		if got[name].Subject != subject {
			t.Errorf("%s: got %q, want %q", name, got[name].Subject, subject)
		}
	}
	if _, ok := got["untracked.txt"]; ok {
		t.Error("untracked files must not be listed")
	}

	sub, err := cache.Get(ctx, r, dir, "sub")
	if err != nil {
		t.Fatal(err)
	}
	if sub["c.txt"].Subject != "add c" {
		t.Errorf("sub/c.txt: %+v", sub["c.txt"])
	}
}

func TestLastCommitsEmptyRepo(t *testing.T) {
	r := setup(t)
	dir := t.TempDir()
	mustGit(t, r, dir, "init", "-q")
	got, err := NewCommitCache().Get(context.Background(), r, dir, "")
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}
