package gitx

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestLog(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	remote, dir := newRemote(t, r)
	_ = remote
	writeFile(t, filepath.Join(dir, "old.txt"), "one\ntwo\n")
	commitAll(t, r, dir, "add old")
	mustGit(t, r, dir, "mv", "old.txt", "new.txt")
	commitAll(t, r, dir, "rename")
	for i := 0; i < 3; i++ {
		writeFile(t, filepath.Join(dir, "README"), strings.Repeat("x", i+1)+"\n")
		commitAll(t, r, dir, "readme "+string(rune('a'+i)))
	}
	mustGit(t, r, dir, "tag", "v1")

	page, err := r.Log(ctx, dir, LogOptions{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Commits) != 2 || !page.More || page.Commits[0].Subject != "readme c" {
		t.Fatalf("first page: %+v", page)
	}
	refs := strings.Join(page.Commits[0].Refs, ",")
	if !strings.Contains(refs, "main") || !strings.Contains(refs, "tag: v1") {
		t.Errorf("refs: %q", refs)
	}
	all, _ := r.Log(ctx, dir, LogOptions{Skip: 4})
	if len(all.Commits) != 2 || all.More || all.Commits[1].Subject != "init" || len(all.Commits[1].Parents) != 0 {
		t.Errorf("last page: %+v", all)
	}
	file, _ := r.Log(ctx, dir, LogOptions{Path: "new.txt"})
	if len(file.Commits) != 2 || file.Commits[1].Subject != "add old" {
		t.Errorf("--follow must see the rename: %+v", file.Commits)
	}
	if un := r.Unpushed(ctx, dir, 100); len(un) != 5 || !un[page.Commits[0].Hash] {
		t.Errorf("unpushed: %v", un)
	}
	if _, err := r.Log(ctx, dir, LogOptions{Path: "../x"}); err == nil {
		t.Error("path outside the tree must fail")
	}
	empty := t.TempDir()
	mustGit(t, r, empty, "init", "-q")
	if p, err := r.Log(ctx, empty, LogOptions{}); err != nil || len(p.Commits) != 0 {
		t.Errorf("empty repo: %+v %v", p, err)
	}
}

func TestShowCommit(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	dir := newRepo(t, r)
	writeFile(t, filepath.Join(dir, "a.txt"), "1\n2\n3\n")
	writeFile(t, filepath.Join(dir, "bin.dat"), "\x00\x01")
	writeFile(t, filepath.Join(dir, "sp ace.txt"), "s\n")
	commitAll(t, r, dir, "base")
	writeFile(t, filepath.Join(dir, "a.txt"), "1\nтри\n3\n4\n")
	mustGit(t, r, dir, "mv", "sp ace.txt", "moved.txt")
	mustGit(t, r, dir, "rm", "-q", "bin.dat")
	mustGit(t, r, dir, "add", "-A")
	mustGit(t, r, dir, "commit", "-q", "-m", "change\n\nbody line")
	head := mustGit(t, r, dir, "rev-parse", "HEAD")

	d, err := r.ShowCommit(ctx, dir, head[:8])
	if err != nil {
		t.Fatal(err)
	}
	if d.Hash != head || d.Subject != "change" || d.Body != "body line" || d.Committer != "Test" || len(d.Parents) != 1 {
		t.Errorf("detail: %+v", d)
	}
	files := map[string]CommitFile{}
	for _, f := range d.Files {
		files[f.Path] = f
	}
	if f := files["a.txt"]; f.Status != "M" || f.Added != 2 || f.Deleted != 1 {
		t.Errorf("a.txt: %+v", f)
	}
	if f := files["moved.txt"]; f.Status != "R" || f.OldPath != "sp ace.txt" {
		t.Errorf("rename: %+v", f)
	}
	if f := files["bin.dat"]; f.Status != "D" || f.Added != -1 {
		t.Errorf("binary delete: %+v", f)
	}

	diff, err := r.CommitDiff(ctx, dir, head, "a.txt", "")
	if err != nil || !strings.Contains(diff.Text, "+три") || !strings.Contains(diff.Text, "-2") {
		t.Errorf("diff: %v %+v", err, diff)
	}
	ren, err := r.CommitDiff(ctx, dir, head, "moved.txt", "sp ace.txt")
	if err != nil || !strings.Contains(ren.Text, "rename from sp ace.txt") {
		t.Errorf("rename diff: %v %+v", err, ren)
	}
	first := mustGit(t, r, dir, "rev-list", "--max-parents=0", "HEAD")
	root, err := r.ShowCommit(ctx, dir, first)
	if err != nil || len(root.Files) != 1 || root.Files[0].Path != "README" || root.Files[0].Status != "A" {
		t.Errorf("root commit: %+v %v", root, err)
	}
	if rd, err := r.CommitDiff(ctx, dir, first, "README", ""); err != nil || !strings.Contains(rd.Text, "+hello") {
		t.Errorf("root diff: %v %+v", err, rd)
	}
	for _, bad := range []string{"", "HEAD", "-x", "zzzz", "main", strings.Repeat("a", 65)} {
		var ie *InputError
		if _, err := r.ShowCommit(ctx, dir, bad); !errors.As(err, &ie) {
			t.Errorf("%q: want input error, got %v", bad, err)
		}
	}
	if _, err := r.ShowCommit(ctx, dir, "deadbeef"); err == nil {
		t.Error("unknown commit must fail")
	}
}

func TestShowMergeCommit(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	dir := newRepo(t, r)
	mustGit(t, r, dir, "checkout", "-q", "-b", "side")
	writeFile(t, filepath.Join(dir, "side.txt"), "s\n")
	commitAll(t, r, dir, "side")
	mustGit(t, r, dir, "checkout", "-q", "main")
	writeFile(t, filepath.Join(dir, "main.txt"), "m\n")
	commitAll(t, r, dir, "main")
	mustGit(t, r, dir, "merge", "-q", "--no-edit", "side")
	d, err := r.ShowCommit(ctx, dir, mustGit(t, r, dir, "rev-parse", "HEAD"))
	if err != nil || len(d.Parents) != 2 || len(d.Files) != 1 || d.Files[0].Path != "side.txt" {
		t.Errorf("merge vs first parent: %+v %v", d, err)
	}
}
