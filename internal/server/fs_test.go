package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

type fsResp struct {
	Dir     string `json:"dir"`
	Path    string `json:"path"`
	Entries []struct {
		Name  string `json:"name"`
		IsDir bool   `json:"is_dir"`
		Size  int64  `json:"size"`
		MTime int64  `json:"mtime"`
	} `json:"entries"`
	Truncated bool `json:"truncated"`
}

func fsQuery(dir, path string) string {
	q := url.Values{}
	if dir != "" {
		q.Set("dir", dir)
	}
	if path != "" {
		q.Set("path", path)
	}
	return "/api/fs?" + q.Encode()
}

func entryNames(r fsResp) []string {
	out := []string{}
	for _, e := range r.Entries {
		out = append(out, e.Name)
	}
	return out
}

func mkfile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFSList(t *testing.T) {
	e := newEnv(t)
	e.login(t)
	out := filepath.Join(e.root, "out")
	mkfile(t, filepath.Join(out, "a.txt"), "aaa")
	mkfile(t, filepath.Join(out, "B.txt"), "b")
	mkfile(t, filepath.Join(out, "sub", "c.txt"), "c")
	mkfile(t, filepath.Join(out, ".git", "config"), "x")

	code, data := e.do(t, "GET", fsQuery(out, ""), nil, false)
	if code != http.StatusOK {
		t.Fatalf("list: %d %s", code, data)
	}
	var r fsResp
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	got := entryNames(r)
	want := []string{"sub", "a.txt", "B.txt"} // каталоги первыми, без учёта регистра, без .git
	if len(got) != len(want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("entries = %v, want %v", got, want)
		}
	}
	if r.Dir != out || r.Path != "" || r.Truncated {
		t.Errorf("response header: %+v", r)
	}
	if !r.Entries[0].IsDir || r.Entries[1].Size != 3 || r.Entries[1].MTime == 0 {
		t.Errorf("entry details: %+v", r.Entries)
	}

	// подкаталог
	code, data = e.do(t, "GET", fsQuery(out, "sub"), nil, false)
	if code != http.StatusOK {
		t.Fatalf("subdir: %d %s", code, data)
	}
	r = fsResp{}
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	if names := entryNames(r); len(names) != 1 || names[0] != "c.txt" || r.Path != "sub" {
		t.Errorf("subdir: %+v", r)
	}

	// без dir открывается первый корень, раз папка по умолчанию не задана
	code, data = e.do(t, "GET", "/api/fs", nil, false)
	if code != http.StatusOK {
		t.Fatalf("default: %d %s", code, data)
	}
	r = fsResp{}
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	if r.Dir != e.root {
		t.Errorf("default dir = %q, want %q", r.Dir, e.root)
	}

	cases := []struct {
		name, query string
		want        int
	}{
		{"outside roots", fsQuery(t.TempDir(), ""), http.StatusForbidden},
		{"traversal", fsQuery(out, "../.."), http.StatusForbidden},
		{"into .git", fsQuery(out, ".git"), http.StatusBadRequest},
		{"file, not a folder", fsQuery(out, "a.txt"), http.StatusBadRequest},
		{"missing", fsQuery(filepath.Join(e.root, "nope"), ""), http.StatusNotFound},
		{"relative dir", fsQuery("out", ""), http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if code, data := e.do(t, "GET", c.query, nil, false); code != c.want {
				t.Errorf("got %d, want %d: %s", code, c.want, data)
			}
		})
	}
}

func TestFSListNeedsLogin(t *testing.T) {
	e := newEnv(t)
	if code, _ := e.do(t, "GET", fsQuery(e.root, ""), nil, false); code != http.StatusUnauthorized {
		t.Errorf("without login: %d", code)
	}
}

func TestMkdir(t *testing.T) {
	e := newEnv(t)
	e.login(t)
	out := filepath.Join(e.root, "out")
	mkfile(t, filepath.Join(out, "file.txt"), "x")

	body := map[string]string{"dir": out, "path": "", "name": "new folder"}
	code, data := e.do(t, "POST", "/api/fs/mkdir", body, true)
	if code != http.StatusCreated {
		t.Fatalf("mkdir: %d %s", code, data)
	}
	if fi, err := os.Stat(filepath.Join(out, "new folder")); err != nil || !fi.IsDir() {
		t.Fatalf("folder not created: %v", err)
	}
	var resp struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(data, &resp); err != nil || resp.Path != "new folder" {
		t.Errorf("response: %s", data)
	}

	if code, _ := e.do(t, "POST", "/api/fs/mkdir", body, true); code != http.StatusConflict {
		t.Errorf("second mkdir: %d", code)
	}
	if code, _ := e.do(t, "POST", "/api/fs/mkdir", body, false); code != http.StatusForbidden {
		t.Errorf("without CSRF: %d", code)
	}

	bad := []struct {
		name string
		body map[string]string
		want int
	}{
		{"dot dot", map[string]string{"dir": out, "name": ".."}, http.StatusBadRequest},
		{"slash", map[string]string{"dir": out, "name": "a/b"}, http.StatusBadRequest},
		{"git name", map[string]string{"dir": out, "name": ".git"}, http.StatusBadRequest},
		{"empty", map[string]string{"dir": out, "name": ""}, http.StatusBadRequest},
		{"inside .git", map[string]string{"dir": out, "path": ".git", "name": "x"}, http.StatusBadRequest},
		{"parent is a file", map[string]string{"dir": out, "path": "file.txt", "name": "x"}, http.StatusBadRequest},
		{"outside roots", map[string]string{"dir": t.TempDir(), "name": "x"}, http.StatusForbidden},
		{"traversal", map[string]string{"dir": out, "path": "../..", "name": "x"}, http.StatusForbidden},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			if code, data := e.do(t, "POST", "/api/fs/mkdir", c.body, true); code != c.want {
				t.Errorf("got %d, want %d: %s", code, c.want, data)
			}
		})
	}
}

func TestSettings(t *testing.T) {
	e := newEnv(t)
	e.login(t)
	out := filepath.Join(e.root, "out")
	mkfile(t, filepath.Join(out, "only-here.txt"), "x")

	code, data := e.do(t, "GET", "/api/settings", nil, false)
	if code != http.StatusOK {
		t.Fatalf("get: %d %s", code, data)
	}
	var st struct {
		DefaultFolder  string `json:"default_folder"`
		OpenOnStart    bool   `json:"open_on_start"`
		AskOnOverwrite bool   `json:"ask_on_overwrite"`
		MoveByDefault  bool   `json:"move_by_default"`
	}
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatal(err)
	}
	if st.DefaultFolder != "" || !st.OpenOnStart || !st.AskOnOverwrite || st.MoveByDefault {
		t.Errorf("defaults: %+v", st)
	}

	// частичное обновление: остальные поля сохраняются
	code, data = e.do(t, "PUT", "/api/settings", map[string]any{"default_folder": out, "move_by_default": true}, true)
	if code != http.StatusOK {
		t.Fatalf("put: %d %s", code, data)
	}
	st = struct {
		DefaultFolder  string `json:"default_folder"`
		OpenOnStart    bool   `json:"open_on_start"`
		AskOnOverwrite bool   `json:"ask_on_overwrite"`
		MoveByDefault  bool   `json:"move_by_default"`
	}{}
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatal(err)
	}
	if st.DefaultFolder != out || !st.MoveByDefault || !st.OpenOnStart || !st.AskOnOverwrite {
		t.Errorf("after put: %+v", st)
	}

	// теперь серверная панель без dir открывает эту папку
	code, data = e.do(t, "GET", "/api/fs", nil, false)
	if code != http.StatusOK {
		t.Fatalf("list default: %d %s", code, data)
	}
	var r fsResp
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	if r.Dir != out || len(r.Entries) != 1 || r.Entries[0].Name != "only-here.txt" {
		t.Errorf("default folder listing: %+v", r)
	}

	// /api/me тоже видит новую папку по умолчанию
	code, data = e.do(t, "GET", "/api/me", nil, false)
	var me struct {
		DefaultFolder string `json:"default_folder"`
	}
	if code != http.StatusOK || json.Unmarshal(data, &me) != nil || me.DefaultFolder != out {
		t.Errorf("me: %d %s", code, data)
	}

	// папка по умолчанию исчезла: панель открывает корень, а не ошибку
	if err := os.RemoveAll(out); err != nil {
		t.Fatal(err)
	}
	code, data = e.do(t, "GET", "/api/fs", nil, false)
	r = fsResp{}
	if code != http.StatusOK || json.Unmarshal(data, &r) != nil || r.Dir != e.root {
		t.Errorf("fallback to root: %d %s", code, data)
	}

	bad := []struct {
		name string
		body map[string]any
		want int
	}{
		{"outside roots", map[string]any{"default_folder": t.TempDir()}, http.StatusForbidden},
		{"missing folder", map[string]any{"default_folder": filepath.Join(e.root, "nope")}, http.StatusNotFound},
		{"unknown field", map[string]any{"colour": "red"}, http.StatusBadRequest},
		{"wrong type", map[string]any{"open_on_start": "yes"}, http.StatusBadRequest},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			if code, data := e.do(t, "PUT", "/api/settings", c.body, true); code != c.want {
				t.Errorf("got %d, want %d: %s", code, c.want, data)
			}
		})
	}
	if code, _ := e.do(t, "PUT", "/api/settings", map[string]any{"move_by_default": false}, false); code != http.StatusForbidden {
		t.Errorf("without CSRF: %d", code)
	}
}
