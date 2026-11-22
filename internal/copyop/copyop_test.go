package copyop

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/Maxi-Boy/gitaway/internal/fsx"
)

var t0 = time.Unix(1_700_000_000, 0)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func setTime(t *testing.T, path string, tm time.Time) {
	t.Helper()
	if err := os.Chtimes(path, tm, tm); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func fileRels(files []FileOp) []string {
	out := []string{}
	for _, f := range files {
		out = append(out, f.Rel)
	}
	sort.Strings(out)
	return out
}

func dirRels(dirs []DirOp) []string {
	out := []string{}
	for _, d := range dirs {
		out = append(out, d.Rel)
	}
	sort.Strings(out)
	return out
}

func sorted(s []string) []string {
	out := append([]string{}, s...)
	sort.Strings(out)
	return out
}

type env struct {
	root, src, dst string
	g              *fsx.Sandbox
}

func newEnv(t *testing.T) *env {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := &env{root: root, src: filepath.Join(root, "src"), dst: filepath.Join(root, "dst")}
	for _, d := range []string{e.src, e.dst} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	g, err := fsx.NewSandbox([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	e.g = g
	return e
}

func (e *env) req(items ...string) Request {
	return Request{SrcBase: e.src, Items: items, DstDir: e.dst}
}

func (e *env) exec(policy Policy, decisions map[string]Action, items ...string) (*Result, error) {
	return Execute(context.Background(), e.g, ExecRequest{Request: e.req(items...), Decisions: decisions, Policy: policy})
}

// conflictEnv: a.txt (источник новее), b.txt (назначение новее), n.txt (без конфликта).
func conflictEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	write(t, filepath.Join(e.src, "a.txt"), "new content")
	setTime(t, filepath.Join(e.src, "a.txt"), t0)
	write(t, filepath.Join(e.dst, "a.txt"), "old")
	setTime(t, filepath.Join(e.dst, "a.txt"), t0.Add(-48*time.Hour))
	write(t, filepath.Join(e.src, "b.txt"), "bb")
	setTime(t, filepath.Join(e.src, "b.txt"), t0)
	write(t, filepath.Join(e.dst, "b.txt"), "dst newer")
	setTime(t, filepath.Join(e.dst, "b.txt"), t0.Add(time.Hour))
	write(t, filepath.Join(e.src, "n.txt"), "fresh")
	return e
}

func TestPlanBasic(t *testing.T) {
	e := newEnv(t)
	write(t, filepath.Join(e.src, "a.txt"), "aaa")
	write(t, filepath.Join(e.src, "dir", "b.txt"), "bb")
	write(t, filepath.Join(e.src, "dir", "sub", "c.txt"), "c")
	write(t, filepath.Join(e.src, "dir", ".git", "config"), "x")
	if err := os.Symlink(filepath.Join(e.src, "a.txt"), filepath.Join(e.src, "dir", "link")); err != nil {
		t.Skip("symlinks are not available:", err)
	}

	plan, err := BuildPlan(e.g, e.req("a.txt", "dir"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := fileRels(plan.Files), []string{"a.txt", "dir/b.txt", "dir/sub/c.txt"}; !reflect.DeepEqual(got, want) {
		t.Errorf("files = %v, want %v", got, want)
	}
	if got, want := dirRels(plan.Dirs), []string{"dir", "dir/sub"}; !reflect.DeepEqual(got, want) {
		t.Errorf("dirs = %v, want %v", got, want)
	}
	if plan.TotalBytes != 6 {
		t.Errorf("total bytes = %d", plan.TotalBytes)
	}
	reasons := map[string]string{}
	for _, s := range plan.Skipped {
		reasons[s.Rel] = s.Reason
	}
	if reasons["dir/.git"] != ReasonGitDir || reasons["dir/link"] != ReasonSymlink || len(reasons) != 2 {
		t.Errorf("skipped = %+v", plan.Skipped)
	}
	if len(plan.Conflicts) != 0 || len(plan.Blocked) != 0 {
		t.Errorf("unexpected conflicts or blocked: %+v %+v", plan.Conflicts, plan.Blocked)
	}
	if exists(filepath.Join(e.dst, "a.txt")) {
		t.Error("planning must not change the disk")
	}
}

func TestPlanConflictDetails(t *testing.T) {
	e := conflictEnv(t)
	write(t, filepath.Join(e.src, "c.txt"), "same")
	write(t, filepath.Join(e.dst, "c.txt"), "same")
	setTime(t, filepath.Join(e.src, "c.txt"), t0)
	setTime(t, filepath.Join(e.dst, "c.txt"), t0)

	plan, err := BuildPlan(e.g, e.req("a.txt", "b.txt", "c.txt", "n.txt"))
	if err != nil {
		t.Fatal(err)
	}
	byRel := map[string]Conflict{}
	for _, c := range plan.Conflicts {
		byRel[c.Rel] = c
	}
	if len(byRel) != 3 {
		t.Fatalf("conflicts = %+v", plan.Conflicts)
	}
	if byRel["a.txt"].Newer != "src" || byRel["b.txt"].Newer != "dst" || byRel["c.txt"].Newer != "same" {
		t.Errorf("newer flags: %+v", byRel)
	}
	a := byRel["a.txt"]
	if a.Src.Size != 11 || a.Dst.Size != 3 || a.Src.MTime != t0.Unix() || a.Dst.MTime != t0.Add(-48*time.Hour).Unix() {
		t.Errorf("a.txt details: %+v", a)
	}
	if byRel["a.txt"].SameFile {
		t.Error("different files must not be reported as the same file")
	}
	if len(plan.Files) != 4 {
		t.Errorf("files = %v", fileRels(plan.Files))
	}
}

func TestExecuteReplaceAllPreservesMtime(t *testing.T) {
	e := conflictEnv(t)
	res, err := e.exec(PolicyReplaceAll, nil, "a.txt", "b.txt", "n.txt")
	if err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(e.dst, "a.txt")); got != "new content" {
		t.Errorf("a.txt = %q", got)
	}
	if got := read(t, filepath.Join(e.dst, "b.txt")); got != "bb" {
		t.Errorf("b.txt = %q", got)
	}
	st, err := os.Stat(filepath.Join(e.dst, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if st.ModTime().Unix() != t0.Unix() {
		t.Errorf("mtime not preserved: %v", st.ModTime())
	}
	if !reflect.DeepEqual(sorted(res.Replaced), []string{"a.txt", "b.txt"}) || !reflect.DeepEqual(res.Copied, []string{"n.txt"}) {
		t.Errorf("result: %+v", res)
	}
	if len(res.Failed) != 0 {
		t.Errorf("failed: %+v", res.Failed)
	}
	entries, _ := os.ReadDir(e.dst)
	for _, en := range entries {
		if len(en.Name()) > 13 && en.Name()[:13] == ".gitaway-copy" {
			t.Errorf("temporary file left behind: %s", en.Name())
		}
	}
}

func TestExecutePolicies(t *testing.T) {
	cases := []struct {
		name         string
		policy       Policy
		wantA, wantB string
		extra        []string
	}{
		{"skip_all", PolicySkipAll, "old", "dst newer", nil},
		{"replace_all", PolicyReplaceAll, "new content", "bb", nil},
		{"replace_newer", PolicyReplaceNewer, "new content", "dst newer", nil},
		{"keep_both_all", PolicyKeepBothAll, "old", "dst newer", []string{"a (2).txt", "b (2).txt"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := conflictEnv(t)
			if _, err := e.exec(c.policy, nil, "a.txt", "b.txt", "n.txt"); err != nil {
				t.Fatal(err)
			}
			if got := read(t, filepath.Join(e.dst, "a.txt")); got != c.wantA {
				t.Errorf("a.txt = %q, want %q", got, c.wantA)
			}
			if got := read(t, filepath.Join(e.dst, "b.txt")); got != c.wantB {
				t.Errorf("b.txt = %q, want %q", got, c.wantB)
			}
			if got := read(t, filepath.Join(e.dst, "n.txt")); got != "fresh" {
				t.Errorf("n.txt = %q", got)
			}
			for _, name := range c.extra {
				if !exists(filepath.Join(e.dst, name)) {
					t.Errorf("%s must exist", name)
				}
			}
			if c.policy == PolicyKeepBothAll {
				if got := read(t, filepath.Join(e.dst, "a (2).txt")); got != "new content" {
					t.Errorf("a (2).txt = %q", got)
				}
			}
		})
	}
}

func TestDecisionOverridesPolicy(t *testing.T) {
	e := conflictEnv(t)
	res, err := e.exec(PolicySkipAll, map[string]Action{"a.txt": ActionKeepBoth}, "a.txt", "b.txt")
	if err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(e.dst, "a.txt")); got != "old" {
		t.Errorf("a.txt = %q", got)
	}
	if got := read(t, filepath.Join(e.dst, "a (2).txt")); got != "new content" {
		t.Errorf("a (2).txt = %q", got)
	}
	if got := read(t, filepath.Join(e.dst, "b.txt")); got != "dst newer" {
		t.Errorf("b.txt = %q", got)
	}
	if len(res.Renamed) != 1 || res.Renamed[0].From != "a.txt" || res.Renamed[0].To != "a (2).txt" {
		t.Errorf("renamed: %+v", res.Renamed)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].Reason != ReasonUser {
		t.Errorf("skipped: %+v", res.Skipped)
	}
}

func TestUnresolvedConflictsChangeNothing(t *testing.T) {
	e := conflictEnv(t)
	_, err := e.exec(PolicyNone, map[string]Action{"a.txt": ActionReplace}, "a.txt", "b.txt", "n.txt")
	var ue *UnresolvedError
	if !errors.As(err, &ue) {
		t.Fatalf("expected UnresolvedError, got %v", err)
	}
	if !reflect.DeepEqual(ue.Rels, []string{"b.txt"}) {
		t.Errorf("unresolved = %v", ue.Rels)
	}
	if got := read(t, filepath.Join(e.dst, "a.txt")); got != "old" {
		t.Errorf("a.txt changed: %q", got)
	}
	if exists(filepath.Join(e.dst, "n.txt")) {
		t.Error("nothing may be copied while conflicts are unresolved")
	}
}

func TestCopyIntoItselfIsRejected(t *testing.T) {
	e := newEnv(t)
	write(t, filepath.Join(e.src, "d", "sub", "f.txt"), "x")
	for _, dst := range []string{filepath.Join(e.src, "d", "sub"), filepath.Join(e.src, "d")} {
		_, err := BuildPlan(e.g, Request{SrcBase: e.src, Items: []string{"d"}, DstDir: dst})
		if !errors.Is(err, ErrIntoItself) {
			t.Errorf("dst %s: got %v", dst, err)
		}
	}
}

func TestTypeConflictsAreBlocked(t *testing.T) {
	e := newEnv(t)
	write(t, filepath.Join(e.src, "x.txt"), "file")
	write(t, filepath.Join(e.src, "y", "f.txt"), "in dir")
	write(t, filepath.Join(e.src, "ok.txt"), "ok")
	if err := os.MkdirAll(filepath.Join(e.dst, "x.txt"), 0o755); err != nil { // каталог на месте файла
		t.Fatal(err)
	}
	write(t, filepath.Join(e.dst, "y"), "i am a file") // файл на месте каталога

	plan, err := BuildPlan(e.g, e.req("x.txt", "y", "ok.txt"))
	if err != nil {
		t.Fatal(err)
	}
	blocked := []string{}
	for _, b := range plan.Blocked {
		blocked = append(blocked, b.Rel)
		if b.Reason != ReasonTypeConflict {
			t.Errorf("reason = %q", b.Reason)
		}
	}
	if !reflect.DeepEqual(sorted(blocked), []string{"x.txt", "y"}) {
		t.Errorf("blocked = %v", blocked)
	}

	res, err := e.exec(PolicyReplaceAll, nil, "x.txt", "y", "ok.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Copied, []string{"ok.txt"}) || len(res.Skipped) != 2 {
		t.Errorf("result: %+v", res)
	}
	if fi, err := os.Stat(filepath.Join(e.dst, "x.txt")); err != nil || !fi.IsDir() {
		t.Error("destination directory x.txt must stay a directory")
	}
	if got := read(t, filepath.Join(e.dst, "y")); got != "i am a file" {
		t.Errorf("destination file y changed: %q", got)
	}
}

func TestDuplicateTargetsAndNestedItems(t *testing.T) {
	e := newEnv(t)
	write(t, filepath.Join(e.src, "a", "x.txt"), "1")
	write(t, filepath.Join(e.src, "b", "x.txt"), "2")
	plan, err := BuildPlan(e.g, e.req("a/x.txt", "b/x.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Files) != 1 || len(plan.Blocked) != 1 || plan.Blocked[0].Reason != ReasonDuplicate {
		t.Errorf("plan: files=%v blocked=%+v", fileRels(plan.Files), plan.Blocked)
	}

	write(t, filepath.Join(e.src, "dir", "b.txt"), "b")
	plan, err = BuildPlan(e.g, e.req("dir", "dir/b.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fileRels(plan.Files), []string{"dir/b.txt"}) || len(plan.Blocked) != 0 {
		t.Errorf("nested item must not be planned twice: %v %+v", fileRels(plan.Files), plan.Blocked)
	}
}

func TestMove(t *testing.T) {
	e := newEnv(t)
	write(t, filepath.Join(e.src, "m1.txt"), "1")
	write(t, filepath.Join(e.src, "d", "m2.txt"), "2")
	write(t, filepath.Join(e.src, "c.txt"), "src c")
	write(t, filepath.Join(e.dst, "c.txt"), "dst c")

	req := ExecRequest{
		Request:   Request{SrcBase: e.src, Items: []string{"m1.txt", "d", "c.txt"}, DstDir: e.dst, Move: true},
		Decisions: map[string]Action{"c.txt": ActionSkip},
	}
	if _, err := Execute(context.Background(), e.g, req); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(e.src, "m1.txt")) || exists(filepath.Join(e.src, "d")) {
		t.Error("moved files and emptied directories must be removed from the source")
	}
	if got := read(t, filepath.Join(e.src, "c.txt")); got != "src c" {
		t.Errorf("skipped file must stay in the source, got %q", got)
	}
	if read(t, filepath.Join(e.dst, "m1.txt")) != "1" || read(t, filepath.Join(e.dst, "d", "m2.txt")) != "2" {
		t.Error("moved files are missing in the destination")
	}
	if got := read(t, filepath.Join(e.dst, "c.txt")); got != "dst c" {
		t.Errorf("c.txt in the destination changed: %q", got)
	}
}

func TestSameFileIsNeverDestroyed(t *testing.T) {
	e := newEnv(t)
	write(t, filepath.Join(e.src, "a.txt"), "x")
	req := Request{SrcBase: e.src, Items: []string{"a.txt"}, DstDir: e.src}

	plan, err := BuildPlan(e.g, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Conflicts) != 1 || !plan.Conflicts[0].SameFile {
		t.Fatalf("conflicts: %+v", plan.Conflicts)
	}

	// "заменить" файл самим собой и перемещение не должны ничего удалять
	req.Move = true
	res, err := Execute(context.Background(), e.g, ExecRequest{Request: req, Policy: PolicyReplaceAll})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].Reason != ReasonSameFile {
		t.Errorf("skipped: %+v", res.Skipped)
	}
	if got := read(t, filepath.Join(e.src, "a.txt")); got != "x" {
		t.Errorf("file changed: %q", got)
	}

	// "оставить оба" в том же каталоге создаёт копию
	req.Move = false
	if _, err := Execute(context.Background(), e.g, ExecRequest{Request: req, Policy: PolicyKeepBothAll}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(e.src, "a (2).txt")); got != "x" {
		t.Errorf("copy = %q", got)
	}
}

func TestDestinationSymlinkEscape(t *testing.T) {
	e := newEnv(t)
	outside, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(e.dst, "out")); err != nil {
		t.Skip("symlinks are not available:", err)
	}
	write(t, filepath.Join(e.src, "out", "f.txt"), "x")
	if _, err := BuildPlan(e.g, e.req("out")); !errors.Is(err, fsx.ErrOutside) {
		t.Fatalf("got %v, want ErrOutside", err)
	}
	if exists(filepath.Join(outside, "f.txt")) {
		t.Error("nothing may be written outside the allowed area")
	}
}

func TestInvalidRequests(t *testing.T) {
	e := newEnv(t)
	write(t, filepath.Join(e.src, "a.txt"), "x")

	if _, err := BuildPlan(e.g, e.req()); !errors.Is(err, ErrInvalid) {
		t.Errorf("no items: %v", err)
	}
	if _, err := BuildPlan(e.g, e.req("/etc/passwd")); !errors.Is(err, ErrInvalid) {
		t.Errorf("absolute item: %v", err)
	}
	if _, err := BuildPlan(e.g, e.req(".")); !errors.Is(err, ErrInvalid) {
		t.Errorf("dot item: %v", err)
	}
	if _, err := BuildPlan(e.g, e.req("../x")); !errors.Is(err, fsx.ErrOutside) {
		t.Errorf("dotdot item: %v", err)
	}
	if _, err := BuildPlan(e.g, e.req("missing.txt")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing item: %v", err)
	}
	bad := Request{SrcBase: e.src, Items: []string{"a.txt"}, DstDir: filepath.Join(e.root, "nodir")}
	if _, err := BuildPlan(e.g, bad); !errors.Is(err, ErrInvalid) {
		t.Errorf("missing destination: %v", err)
	}
	if _, err := e.exec(Policy("bogus"), nil, "a.txt"); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad policy: %v", err)
	}
	if _, err := e.exec(PolicyNone, map[string]Action{"a.txt": Action("bogus")}, "a.txt"); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad action: %v", err)
	}
}

func TestCanceledContext(t *testing.T) {
	e := newEnv(t)
	write(t, filepath.Join(e.src, "a.txt"), "x")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Execute(ctx, e.g, ExecRequest{Request: e.req("a.txt")})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if exists(filepath.Join(e.dst, "a.txt")) {
		t.Error("nothing may be copied after cancellation")
	}
}

func TestLargeFileIntegrity(t *testing.T) {
	e := newEnv(t)
	data := bytes.Repeat([]byte("0123456789abcdef"), 200000) // около 3 МБ
	big := filepath.Join(e.src, "big.bin")
	if err := os.WriteFile(big, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(big, 0o640); err != nil { // umask не должен влиять на проверку прав
		t.Fatal(err)
	}
	res, err := e.exec(PolicyNone, nil, "big.bin")
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(e.dst, "big.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) || res.Bytes != int64(len(data)) {
		t.Error("copied data differs from the source")
	}
	if st, _ := os.Stat(filepath.Join(e.dst, "big.bin")); st.Mode().Perm() != 0o640 {
		t.Errorf("permissions = %v, want 0640", st.Mode().Perm())
	}
}

func TestUniqueName(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.txt"), "")
	write(t, filepath.Join(dir, "a (2).txt"), "")
	write(t, filepath.Join(dir, ".bashrc"), "")
	write(t, filepath.Join(dir, "noext"), "")
	cases := map[string]string{
		"a.txt":   "a (3).txt",
		".bashrc": ".bashrc (2)",
		"noext":   "noext (2)",
	}
	for name, want := range cases {
		got, err := uniqueName(filepath.Join(dir, name))
		if err != nil || got != filepath.Join(dir, want) {
			t.Errorf("%s: got %q, %v, want %q", name, got, err, want)
		}
	}
}

func TestBirthTimeIsSane(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	write(t, path, "x")
	if tm, ok := birthTime(path); ok && tm.After(time.Now().Add(time.Minute)) {
		t.Errorf("birth time in the future: %v", tm)
	}
}
