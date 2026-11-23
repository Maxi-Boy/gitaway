package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeAndDeleteBranch(t *testing.T) {
	e := newEnv(t)
	e.login(t)
	id, dir := e.addRepo(t)
	gitRun(t, dir, "switch", "-q", "-c", "feature")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("f\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "feature")
	gitRun(t, dir, "switch", "-q", "main")
	gitRun(t, dir, "branch", "spare")

	post := func(op string, body map[string]any) (int, string) {
		body["repo"] = id
		code, data := e.do(t, "POST", "/api/git/"+op, body, true)
		return code, string(data)
	}

	// неслитую ветку без force не удалить: 409 not_merged
	if code, data := post("delete-branch", map[string]any{"branch": "feature"}); code != http.StatusConflict || !strings.Contains(data, "not_merged") {
		t.Errorf("unmerged delete: %d %s", code, data)
	}
	if code, data := post("merge", map[string]any{"branch": "feature"}); code != http.StatusOK {
		t.Fatalf("merge: %d %s", code, data)
	}
	if _, err := os.Stat(filepath.Join(dir, "f.txt")); err != nil {
		t.Errorf("merged file missing: %v", err)
	}
	if code, data := post("delete-branch", map[string]any{"branch": "feature"}); code != http.StatusOK {
		t.Errorf("delete merged: %d %s", code, data)
	}
	if code, data := post("delete-branch", map[string]any{"branch": "spare", "force": true}); code != http.StatusOK {
		t.Errorf("force delete: %d %s", code, data)
	}

	// конфликт: статус показывает merging, merge-abort возвращает как было
	gitRun(t, dir, "switch", "-q", "-c", "other")
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "commit", "-q", "-am", "other")
	gitRun(t, dir, "switch", "-q", "main")
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "commit", "-q", "-am", "main")
	if code, data := post("merge", map[string]any{"branch": "other"}); code != http.StatusUnprocessableEntity || !strings.Contains(data, "CONFLICT") {
		t.Errorf("conflicting merge: %d %s", code, data)
	}
	var st struct {
		Merging bool `json:"merging"`
	}
	_, data := e.do(t, "GET", "/api/status?repo="+id, nil, false)
	if json.Unmarshal(data, &st) != nil || !st.Merging {
		t.Errorf("status must show merging: %s", data)
	}
	if code, data := post("merge-abort", map[string]any{}); code != http.StatusOK {
		t.Errorf("merge-abort: %d %s", code, data)
	}
	_, data = e.do(t, "GET", "/api/status?repo="+id, nil, false)
	st.Merging = false // поле с omitempty: при false его нет в JSON
	if json.Unmarshal(data, &st) != nil || st.Merging {
		t.Errorf("status after abort: %s", data)
	}

	bad := []struct {
		name, op string
		body     map[string]any
		want     int
	}{
		{"option as branch", "merge", map[string]any{"branch": "--abort"}, http.StatusBadRequest},
		{"current branch", "delete-branch", map[string]any{"branch": "main", "force": true}, http.StatusUnprocessableEntity},
		{"bad remote", "delete-branch", map[string]any{"remote": "-x", "branch": "main"}, http.StatusBadRequest},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			if code, data := post(c.op, c.body); code != c.want {
				t.Errorf("got %d, want %d: %s", code, c.want, data)
			}
		})
	}
}
