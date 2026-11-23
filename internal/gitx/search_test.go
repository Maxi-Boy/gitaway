package gitx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListFiles(t *testing.T) {
	r := setup(t)
	dir := newRepo(t, r)
	writeFile(t, filepath.Join(dir, "src", "main go.go"), "package main\n")
	writeFile(t, filepath.Join(dir, "gone.txt"), "x\n")
	commitAll(t, r, dir, "files")
	writeFile(t, filepath.Join(dir, "new.txt"), "new\n")
	writeFile(t, filepath.Join(dir, ".gitignore"), "build/\n")
	writeFile(t, filepath.Join(dir, "build", "out.bin"), "x\n")
	writeFile(t, filepath.Join(dir, "ünï.txt"), "x\n")
	if err := os.Remove(filepath.Join(dir, "gone.txt")); err != nil { // в индексе есть, на диске нет
		t.Fatal(err)
	}
	list, err := r.ListFiles(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(list.Files, ",")
	for _, want := range []string{"README", "src/main go.go", "new.txt", ".gitignore", "ünï.txt"} {
		if !strings.Contains(","+got+",", ","+want+",") {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	if strings.Contains(got, "build/") || strings.Contains(got, "gone.txt") {
		t.Errorf("ignored or deleted files listed: %s", got)
	}
}

func TestSearch(t *testing.T) {
	r := setup(t)
	dir := newRepo(t, r)
	writeFile(t, filepath.Join(dir, "a.go"), "package a\n// Hello world\nvar helloThere = 1\n")
	writeFile(t, filepath.Join(dir, "docs", "b.md"), "say hello\n")
	writeFile(t, filepath.Join(dir, "vendor", "v.go"), "hello vendor\n")
	writeFile(t, filepath.Join(dir, "bin.dat"), "hello\x00binary")
	commitAll(t, r, dir, "files")
	writeFile(t, filepath.Join(dir, "new.txt"), "untracked hello -e\n")
	ctx := context.Background()

	count := func(o SearchOptions) (int, string) {
		t.Helper()
		res, err := r.Search(ctx, dir, o)
		if err != nil {
			t.Fatalf("%+v: %v", o, err)
		}
		var paths []string
		for _, m := range res.Matches {
			paths = append(paths, m.Path)
		}
		return len(res.Matches), strings.Join(paths, ",")
	}
	cases := []struct {
		o     SearchOptions
		n     int
		paths string
	}{
		{SearchOptions{Query: "hello"}, 6, ""},
		{SearchOptions{Query: "hello", Case: true}, 5, ""},
		{SearchOptions{Query: "hello", Word: true}, 5, ""},
		{SearchOptions{Query: "hello", Globs: []string{"*.go"}}, 3, "a.go,a.go,vendor/v.go"},
		{SearchOptions{Query: "hello", Globs: []string{"*.go", "!vendor/"}}, 2, "a.go,a.go"},
		{SearchOptions{Query: "hello", Globs: []string{"!vendor/", "!*.md"}}, 4, ""},
		{SearchOptions{Query: "hello", Globs: []string{"docs/"}}, 1, "docs/b.md"},
		{SearchOptions{Query: "h.llo[A-Z]", Regex: true, Case: true}, 1, "a.go"},
		{SearchOptions{Query: "h.llo"}, 0, ""}, // обычный текст: точка это точка
		{SearchOptions{Query: "-e"}, 1, "new.txt"},
		{SearchOptions{Query: "--no-index"}, 0, ""},
	}
	for _, c := range cases {
		n, paths := count(c.o)
		if n != c.n || (c.paths != "" && paths != c.paths) {
			t.Errorf("%+v: got %d %s, want %d %s", c.o, n, paths, c.n, c.paths)
		}
	}
	res, _ := r.Search(ctx, dir, SearchOptions{Query: "helloThere", Case: true})
	if len(res.Matches) != 1 || res.Matches[0].Line != 3 || res.Matches[0].Col != 5 || res.Matches[0].Text != "var helloThere = 1" || res.Files != 1 {
		t.Errorf("match: %+v", res)
	}
	bad := []SearchOptions{
		{Query: " "}, {Query: "a\nb"}, {Query: "x", Globs: []string{":(top)x"}}, {Query: "x", Globs: []string{"../x"}},
		{Query: "x", Globs: []string{".git/"}}, {Query: "(", Regex: true},
	}
	for _, o := range bad {
		var ie *InputError
		if _, err := r.Search(ctx, dir, o); !errors.As(err, &ie) {
			t.Errorf("%+v: want input error, got %v", o, err)
		}
	}
}

func TestSearchLimit(t *testing.T) {
	r := setup(t)
	dir := newRepo(t, r)
	writeFile(t, filepath.Join(dir, "many.txt"), strings.Repeat("x match\n", MaxMatches+10))
	res, err := r.Search(context.Background(), dir, SearchOptions{Query: "match"})
	if err != nil || len(res.Matches) != MaxMatches || !res.Truncated {
		t.Fatalf("limit: %d %v %v", len(res.Matches), res.Truncated, err)
	}
}

func TestClipLine(t *testing.T) {
	long := strings.Repeat("я", 500) + "needle" + strings.Repeat("ж", 500)
	col := len(strings.Repeat("я", 500)) + 1
	s := clipLine([]byte(long), col)
	if !strings.Contains(s, "needle") || !strings.HasPrefix(s, "…") || !strings.HasSuffix(s, "…") || strings.ContainsRune(s, '�') {
		t.Errorf("clip: %q", s)
	}
	if clipLine([]byte("short\r"), 1) != "short" {
		t.Error("short line")
	}
}
