package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type changeResp struct {
	Commit    string   `json:"commit"`
	Discarded []string `json:"discarded"`
	Status    struct {
		Branch  string `json:"branch"`
		Changes []struct {
			Path string `json:"path"`
			X    string `json:"x"`
			Y    string `json:"y"`
		} `json:"changes"`
	} `json:"status"`
}

func (r changeResp) marks() map[string]string {
	m := map[string]string{}
	for _, c := range r.Status.Changes {
		m[c.Path] = c.X + c.Y
	}
	return m
}

// addRepo создаёт тестовый репозиторий и регистрирует его, возвращает id и путь.
func (e *testEnv) addRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := e.makeRepo(t)
	code, data := e.do(t, "POST", "/api/repos", map[string]string{"path": dir}, true)
	if code != http.StatusCreated {
		t.Fatalf("add repo: %d %s", code, data)
	}
	var repo struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &repo); err != nil {
		t.Fatal(err)
	}
	return repo.ID, dir
}

func (e *testEnv) change(t *testing.T, op string, body map[string]any) changeResp {
	t.Helper()
	code, data := e.do(t, "POST", "/api/git/"+op, body, true)
	if code != http.StatusOK {
		t.Fatalf("%s: %d %s", op, code, data)
	}
	var r changeResp
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestChangeFlow(t *testing.T) {
	e := newEnv(t)
	e.login(t)
	id, dir := e.addRepo(t)
	mkfile(t, filepath.Join(dir, "README"), "edited\n")
	mkfile(t, filepath.Join(dir, "new.txt"), "new\n")

	// diff рабочей папки и неотслеживаемого файла
	code, data := e.do(t, "GET", "/api/diff?repo="+id+"&path=README", nil, false)
	if code != http.StatusOK || !strings.Contains(string(data), `+edited`) {
		t.Errorf("diff: %d %s", code, data)
	}
	code, data = e.do(t, "GET", "/api/diff?repo="+id+"&path=new.txt", nil, false)
	if code != http.StatusOK || !strings.Contains(string(data), `"untracked":true`) {
		t.Errorf("untracked diff: %d %s", code, data)
	}

	r := e.change(t, "stage", map[string]any{"repo": id, "paths": []string{"README", "new.txt"}})
	if m := r.marks(); m["README"] != "M " || m["new.txt"] != "A " {
		t.Fatalf("after stage: %v", m)
	}
	code, data = e.do(t, "GET", "/api/diff?repo="+id+"&path=README&staged=1", nil, false)
	if code != http.StatusOK || !strings.Contains(string(data), `+edited`) {
		t.Errorf("staged diff: %d %s", code, data)
	}

	r = e.change(t, "unstage", map[string]any{"repo": id, "paths": []string{"new.txt"}})
	if m := r.marks(); m["new.txt"] != "??" {
		t.Fatalf("after unstage: %v", m)
	}

	r = e.change(t, "commit", map[string]any{"repo": id, "message": "edit readme"})
	if len(r.Commit) < 40 || r.Status.Branch != "main" {
		t.Errorf("commit: %+v", r)
	}
	if m := r.marks(); len(m) != 1 || m["new.txt"] != "??" {
		t.Errorf("after commit: %v", m)
	}

	// отмена: правка возвращается, новый файл удаляется
	mkfile(t, filepath.Join(dir, "sub", "x.txt"), "oops\n")
	r = e.change(t, "discard", map[string]any{"repo": id, "paths": []string{"sub/x.txt", "new.txt"}})
	if len(r.Discarded) != 2 || len(r.Status.Changes) != 0 {
		t.Errorf("discard: %+v", r)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "sub", "x.txt")); string(b) != "x\n" {
		t.Errorf("x.txt = %q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "new.txt")); !os.IsNotExist(err) {
		t.Errorf("new.txt must be removed: %v", err)
	}
}

func TestChangeRejects(t *testing.T) {
	e := newEnv(t)
	e.login(t)
	id, dir := e.addRepo(t)
	outside := t.TempDir()
	mkfile(t, filepath.Join(outside, "secret.txt"), "secret\n")
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}

	post := []struct {
		name, op string
		body     map[string]any
		want     int
	}{
		{"no paths", "stage", map[string]any{"repo": id}, http.StatusBadRequest},
		{"traversal", "stage", map[string]any{"repo": id, "paths": []string{"../x"}}, http.StatusBadRequest},
		{"absolute", "discard", map[string]any{"repo": id, "paths": []string{"/etc"}}, http.StatusBadRequest},
		{"into .git", "discard", map[string]any{"repo": id, "paths": []string{".git/config"}}, http.StatusBadRequest},
		{"via symlink", "discard", map[string]any{"repo": id, "paths": []string{"link/secret.txt"}}, http.StatusForbidden},
		{"unknown repo", "stage", map[string]any{"repo": "nope", "paths": []string{"README"}}, http.StatusNotFound},
		{"unknown field", "stage", map[string]any{"repo": id, "paths": []string{"README"}, "force": true}, http.StatusBadRequest},
		{"empty message", "commit", map[string]any{"repo": id, "message": " "}, http.StatusBadRequest},
		{"nothing staged", "commit", map[string]any{"repo": id, "message": "x"}, http.StatusBadRequest},
		{"commit with paths", "commit", map[string]any{"repo": id, "message": "x", "paths": []string{"README"}}, http.StatusBadRequest},
	}
	for _, c := range post {
		t.Run(c.name, func(t *testing.T) {
			if code, data := e.do(t, "POST", "/api/git/"+c.op, c.body, true); code != c.want {
				t.Errorf("got %d, want %d: %s", code, c.want, data)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(outside, "secret.txt")); err != nil {
		t.Errorf("file outside must survive: %v", err)
	}
	if code, _ := e.do(t, "POST", "/api/git/stage", map[string]any{"repo": id, "paths": []string{"README"}}, false); code != http.StatusForbidden {
		t.Errorf("without CSRF: %d", code)
	}

	diff := func(path string) string {
		return "/api/diff?repo=" + id + "&path=" + url.QueryEscape(path)
	}
	gets := []struct {
		name, query string
		want        int
	}{
		{"no path", "/api/diff?repo=" + id, http.StatusBadRequest},
		{"whole tree", diff("."), http.StatusBadRequest},
		{"traversal", diff("../../etc/passwd"), http.StatusBadRequest},
		{"via symlink", diff("link/secret.txt"), http.StatusForbidden},
		{"post", "", http.StatusMethodNotAllowed},
	}
	for _, c := range gets {
		t.Run("diff "+c.name, func(t *testing.T) {
			method, q := "GET", c.query
			if c.name == "post" {
				method, q = "POST", diff("README")
			}
			if code, data := e.do(t, method, q, nil, true); code != c.want {
				t.Errorf("got %d, want %d: %s", code, c.want, data)
			}
		})
	}
}
