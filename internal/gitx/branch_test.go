package gitx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeAndAbort(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	dir := newRepo(t, r)
	mustGit(t, r, dir, "switch", "-q", "-c", "feature")
	writeFile(t, filepath.Join(dir, "f.txt"), "feature\n")
	commitAll(t, r, dir, "feature work")
	mustGit(t, r, dir, "switch", "-q", "main")

	if _, err := r.Merge(ctx, dir, "feature"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "f.txt")); string(b) != "feature\n" {
		t.Errorf("merge did not bring the file: %q", b)
	}

	// конфликт: обе ветки меняют README по-разному
	mustGit(t, r, dir, "switch", "-q", "-c", "other")
	writeFile(t, filepath.Join(dir, "README"), "other\n")
	commitAll(t, r, dir, "other")
	mustGit(t, r, dir, "switch", "-q", "main")
	writeFile(t, filepath.Join(dir, "README"), "main\n")
	commitAll(t, r, dir, "main")
	if _, err := r.Merge(ctx, dir, "other"); err == nil {
		t.Fatal("conflicting merge must fail")
	}
	st, err := r.Status(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Merging || len(st.Changes) == 0 || st.Changes[0].X != "U" {
		t.Errorf("status during conflict: %+v", st)
	}
	if _, err := r.MergeAbort(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if st, _ = r.Status(ctx, dir); st.Merging || len(st.Changes) != 0 {
		t.Errorf("status after abort: %+v", st)
	}

	var ie *InputError
	if _, err := r.Merge(ctx, dir, "--abort"); !errors.As(err, &ie) {
		t.Errorf("option as branch name: %v", err)
	}
}

func TestDeleteBranch(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	dir := newRepo(t, r)
	mustGit(t, r, dir, "branch", "merged")
	mustGit(t, r, dir, "switch", "-q", "-c", "unmerged")
	writeFile(t, filepath.Join(dir, "u.txt"), "u\n")
	commitAll(t, r, dir, "unmerged work")
	mustGit(t, r, dir, "switch", "-q", "main")

	if _, err := r.DeleteBranch(ctx, dir, "merged", false); err != nil {
		t.Fatal(err)
	}
	if _, err := r.DeleteBranch(ctx, dir, "unmerged", false); !errors.Is(err, ErrNotMerged) {
		t.Fatalf("unmerged without force: %v", err)
	}
	if _, err := r.DeleteBranch(ctx, dir, "unmerged", true); err != nil {
		t.Fatal(err)
	}
	if out := mustGit(t, r, dir, "branch", "--format=%(refname:short)"); strings.TrimSpace(out) != "main" {
		t.Errorf("branches left: %q", out)
	}
	if _, err := r.DeleteBranch(ctx, dir, "main", true); err == nil {
		t.Error("the current branch must not be deletable")
	}
	var ie *InputError
	if _, err := r.DeleteRemoteBranch(ctx, dir, "-o", "x"); !errors.As(err, &ie) {
		t.Errorf("bad remote: %v", err)
	}
}

func TestDeleteRemoteBranch(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	remote := t.TempDir()
	mustGit(t, r, remote, "init", "-q", "--bare")
	dir := newRepo(t, r)
	mustGit(t, r, dir, "remote", "add", "origin", remote)
	mustGit(t, r, dir, "push", "-q", "origin", "main:main", "main:old")
	if _, err := r.DeleteRemoteBranch(ctx, dir, "origin", "old"); err != nil {
		t.Fatal(err)
	}
	if out := mustGit(t, r, remote, "branch", "--format=%(refname:short)"); strings.TrimSpace(out) != "main" {
		t.Errorf("remote branches: %q", out)
	}
}
