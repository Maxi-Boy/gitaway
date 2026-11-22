package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type planResp struct {
	Files []struct {
		Rel string `json:"rel"`
	} `json:"files"`
	Conflicts []struct {
		Rel           string `json:"rel"`
		Newer         string `json:"newer"`
		SrcCommitTime int64  `json:"src_commit_time"`
	} `json:"conflicts"`
}

func addRepo(t *testing.T, e *testEnv, dir string) string {
	t.Helper()
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
	return repo.ID
}

func TestCopyPlanAndExecute(t *testing.T) {
	e := newEnv(t)
	e.login(t)
	dir := e.makeRepo(t)
	id := addRepo(t, e, dir)
	out := filepath.Join(e.root, "out")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	body := func(extra map[string]any) map[string]any {
		m := map[string]any{
			"src":   map[string]string{"repo": id},
			"items": []string{"README", "sub"},
			"dst":   map[string]string{"dir": out},
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}

	// план: два файла, конфликтов нет
	code, data := e.do(t, "POST", "/api/copy/plan", body(nil), true)
	if code != http.StatusOK {
		t.Fatalf("plan: %d %s", code, data)
	}
	var plan planResp
	if err := json.Unmarshal(data, &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Files) != 2 || len(plan.Conflicts) != 0 {
		t.Fatalf("plan: %s", data)
	}

	// выполнение
	if code, data := e.do(t, "POST", "/api/copy", body(nil), true); code != http.StatusOK {
		t.Fatalf("copy: %d %s", code, data)
	}
	if got, _ := os.ReadFile(filepath.Join(out, "README")); string(got) != "hello\n" {
		t.Errorf("README in destination: %q", got)
	}
	if _, err := os.Stat(filepath.Join(out, "sub", "x.txt")); err != nil {
		t.Errorf("sub/x.txt must be copied: %v", err)
	}

	// источник изменился и стал новее: в плане конфликт README, у него есть время коммита
	readme := filepath.Join(dir, "README")
	if err := os.WriteFile(readme, []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(readme, future, future); err != nil {
		t.Fatal(err)
	}
	code, data = e.do(t, "POST", "/api/copy/plan", body(nil), true)
	if code != http.StatusOK {
		t.Fatalf("second plan: %d %s", code, data)
	}
	plan = planResp{}
	if err := json.Unmarshal(data, &plan); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range plan.Conflicts {
		if c.Rel == "README" {
			found = true
			if c.Newer != "src" || c.SrcCommitTime == 0 {
				t.Errorf("README conflict: %+v", c)
			}
		}
	}
	if !found || len(plan.Conflicts) != 2 {
		t.Fatalf("conflicts: %s", data)
	}

	// без решения 409 и ничего не меняется
	code, data = e.do(t, "POST", "/api/copy", body(nil), true)
	if code != http.StatusConflict {
		t.Fatalf("unresolved: %d %s", code, data)
	}
	if got, _ := os.ReadFile(filepath.Join(out, "README")); string(got) != "hello\n" {
		t.Errorf("README changed without a decision: %q", got)
	}

	// массовое решение: заменить только более новые
	code, data = e.do(t, "POST", "/api/copy", body(map[string]any{"policy": "replace_newer"}), true)
	if code != http.StatusOK {
		t.Fatalf("replace_newer: %d %s", code, data)
	}
	if got, _ := os.ReadFile(filepath.Join(out, "README")); string(got) != "changed\n" {
		t.Errorf("README after replace_newer: %q", got)
	}
}

func TestCopyRejectsDangerousRequests(t *testing.T) {
	e := newEnv(t)
	e.login(t)
	dir := e.makeRepo(t)
	id := addRepo(t, e, dir)
	out := filepath.Join(e.root, "out")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	src := map[string]string{"repo": id}
	dst := map[string]string{"dir": out}

	cases := []struct {
		name string
		body map[string]any
		want int
	}{
		{"into .git", map[string]any{"src": src, "items": []string{"README"}, "dst": map[string]string{"dir": filepath.Join(dir, ".git")}}, http.StatusBadRequest},
		{"from .git", map[string]any{"src": src, "items": []string{".git/config"}, "dst": dst}, http.StatusBadRequest},
		{"path traversal", map[string]any{"src": src, "items": []string{"../x"}, "dst": dst}, http.StatusForbidden},
		{"destination outside roots", map[string]any{"src": src, "items": []string{"README"}, "dst": map[string]string{"dir": t.TempDir()}}, http.StatusForbidden},
		{"no items", map[string]any{"src": src, "items": []string{}, "dst": dst}, http.StatusBadRequest},
		{"no location", map[string]any{"src": map[string]string{}, "items": []string{"README"}, "dst": dst}, http.StatusBadRequest},
		{"unknown policy", map[string]any{"src": src, "items": []string{"README"}, "dst": dst, "policy": "bogus"}, http.StatusBadRequest},
		{"unknown repo", map[string]any{"src": map[string]string{"repo": "nope"}, "items": []string{"README"}, "dst": dst}, http.StatusNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if code, data := e.do(t, "POST", "/api/copy", c.body, true); code != c.want {
				t.Errorf("got %d, want %d: %s", code, c.want, data)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(out, "README")); err == nil {
		t.Error("nothing may be copied by rejected requests")
	}
}
