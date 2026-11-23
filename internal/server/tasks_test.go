package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type taskInfo struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Steps []string `json:"steps"`
	Cwd   string   `json:"cwd"`
	Scope string   `json:"scope"`
}

// waitSession ждёт завершения сеанса и возвращает его код выхода.
func (e *testEnv) waitSession(t *testing.T, id string) sessionInfo {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		_, data := e.do(t, "GET", "/api/sessions", nil, false)
		var list []sessionInfo
		if err := json.Unmarshal(data, &list); err != nil {
			t.Fatal(err)
		}
		for _, s := range list {
			if s.ID == id && !s.Running {
				return s
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("session %s did not finish", id)
	return sessionInfo{}
}

func (e *testEnv) runTask(t *testing.T, body map[string]any) (int, sessionInfo, string) {
	t.Helper()
	code, data := e.do(t, "POST", "/api/tasks/run", body, true)
	var info sessionInfo
	_ = json.Unmarshal(data, &info)
	return code, info, string(data)
}

func TestTasks(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	e := newEnv(t)
	e.login(t)
	id, dir := e.addRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// мои скрипты: в папке вкладки и в папке сервера
	mine := []map[string]any{
		{"name": "Ветка в файл", "steps": []string{"echo {branch} > branch.txt", "cat branch.txt"}, "scope": id},
		{"name": "Бэкап", "steps": []string{"mkdir -p backup", "echo ok > backup/{date}.txt"}, "cwd": e.root},
		{"name": "Долгая", "steps": []string{"sleep 30"}, "timeout": "1s"},
	}
	code, data := e.do(t, "PUT", "/api/tasks/mine", mine, true)
	if code != http.StatusOK {
		t.Fatalf("save mine: %d %s", code, data)
	}
	var saved []taskInfo
	if err := json.Unmarshal(data, &saved); err != nil || len(saved) != 3 || !strings.HasPrefix(saved[0].ID, "my:") {
		t.Fatalf("saved: %s", data)
	}

	code, data = e.do(t, "GET", "/api/tasks?repo="+id, nil, false)
	var list struct {
		Mine []taskInfo `json:"mine"`
		Auto []taskInfo `json:"auto"`
		Repo struct {
			Tasks   []taskInfo `json:"tasks"`
			Hash    string     `json:"hash"`
			Trusted bool       `json:"trusted"`
		} `json:"repo"`
	}
	if code != http.StatusOK || json.Unmarshal(data, &list) != nil || len(list.Mine) != 3 || len(list.Repo.Tasks) != 0 {
		t.Fatalf("list: %d %s", code, data)
	}
	autoTest := ""
	for _, a := range list.Auto {
		if a.Name == "go test" {
			autoTest = a.ID
		}
	}
	if autoTest == "" {
		t.Errorf("auto tasks: %+v", list.Auto)
	}

	// запуск в папке вкладки: подстановка ветки
	code, info, body := e.runTask(t, map[string]any{"repo": id, "id": saved[0].ID})
	if code != http.StatusCreated {
		t.Fatalf("run: %d %s", code, body)
	}
	if done := e.waitSession(t, info.ID); done.ExitCode != 0 {
		t.Errorf("task exit code %d", done.ExitCode)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "branch.txt")); strings.TrimSpace(string(b)) != "main" {
		t.Errorf("branch.txt = %q", b)
	}

	// запуск в папке сервера
	code, info, body = e.runTask(t, map[string]any{"repo": id, "id": saved[1].ID})
	if code != http.StatusCreated {
		t.Fatalf("run backup: %d %s", code, body)
	}
	e.waitSession(t, info.ID)
	if files, _ := os.ReadDir(filepath.Join(e.root, "backup")); len(files) != 1 || !strings.HasSuffix(files[0].Name(), ".txt") {
		t.Errorf("backup: %v", files)
	}

	// таймаут останавливает задачу
	code, info, body = e.runTask(t, map[string]any{"repo": id, "id": saved[2].ID})
	if code != http.StatusCreated {
		t.Fatalf("run long: %d %s", code, body)
	}
	if done := e.waitSession(t, info.ID); done.ExitCode == 0 {
		t.Error("timed out task must not succeed")
	}

	// .gitaway.json: без доверия не запускается, доверие привязано к содержимому
	repoFile := filepath.Join(dir, ".gitaway.json")
	if err := os.WriteFile(repoFile, []byte(`{"tasks":[{"name":"Из репо","steps":["touch from-repo"]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, data = e.do(t, "GET", "/api/tasks?repo="+id, nil, false)
	if err := json.Unmarshal(data, &list); err != nil || len(list.Repo.Tasks) != 1 || list.Repo.Trusted {
		t.Fatalf("repo tasks: %s", data)
	}
	if code, _, body := e.runTask(t, map[string]any{"repo": id, "id": "repo:0"}); code != http.StatusForbidden || !strings.Contains(body, "not_trusted") {
		t.Errorf("untrusted run: %d %s", code, body)
	}
	if code, _ := e.do(t, "POST", "/api/tasks/trust", map[string]any{"repo": id, "hash": "deadbeef"}, true); code != http.StatusConflict {
		t.Errorf("trust with a stale hash: %d", code)
	}
	if code, data := e.do(t, "POST", "/api/tasks/trust", map[string]any{"repo": id, "hash": list.Repo.Hash}, true); code != http.StatusOK {
		t.Fatalf("trust: %d %s", code, data)
	}
	code, info, body = e.runTask(t, map[string]any{"repo": id, "id": "repo:0"})
	if code != http.StatusCreated {
		t.Fatalf("trusted run: %d %s", code, body)
	}
	e.waitSession(t, info.ID)
	if _, err := os.Stat(filepath.Join(dir, "from-repo")); err != nil {
		t.Errorf("repo task did not run: %v", err)
	}
	if err := os.WriteFile(repoFile, []byte(`{"tasks":[{"name":"Из репо","steps":["touch changed"]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := e.runTask(t, map[string]any{"repo": id, "id": "repo:0"}); code != http.StatusForbidden {
		t.Errorf("changed file must need approval again: %d", code)
	}

	// автоопределённая задача запускается (go может отсутствовать: важен только старт сеанса)
	code, info, body = e.runTask(t, map[string]any{"repo": id, "id": autoTest})
	if code != http.StatusCreated {
		t.Errorf("auto task: %d %s", code, body)
	} else {
		e.waitSession(t, info.ID) // процесс пишет в папку теста: дождаться до её удаления
	}

	bad := []struct {
		name string
		body any
		want int
	}{
		{"relative cwd", []map[string]any{{"name": "x", "steps": []string{"ls"}, "cwd": "rel"}}, http.StatusBadRequest},
		{"cwd outside roots", []map[string]any{{"name": "x", "steps": []string{"ls"}, "cwd": t.TempDir()}}, http.StatusForbidden},
		{"unknown scope", []map[string]any{{"name": "x", "steps": []string{"ls"}, "scope": "nope"}}, http.StatusBadRequest},
		{"no steps", []map[string]any{{"name": "x", "steps": []string{}}}, http.StatusBadRequest},
		{"unknown field", []map[string]any{{"name": "x", "steps": []string{"ls"}, "shell": "zsh"}}, http.StatusBadRequest},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			if code, data := e.do(t, "PUT", "/api/tasks/mine", c.body, true); code != c.want {
				t.Errorf("got %d, want %d: %s", code, c.want, data)
			}
		})
	}
	if code, _, _ := e.runTask(t, map[string]any{"repo": id, "id": "my:nope"}); code != http.StatusNotFound {
		t.Errorf("unknown task: %d", code)
	}
	if code, _ := e.do(t, "POST", "/api/tasks/run", map[string]any{"repo": id, "id": saved[0].ID}, false); code != http.StatusForbidden {
		t.Errorf("run without CSRF: %d", code)
	}
}
