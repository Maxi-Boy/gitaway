package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Maxi-Boy/gitaway/internal/auth"
	"github.com/Maxi-Boy/gitaway/internal/config"
	"github.com/Maxi-Boy/gitaway/internal/fsx"
	"github.com/Maxi-Boy/gitaway/internal/gitx"
	"github.com/Maxi-Boy/gitaway/internal/store"
)

type testEnv struct {
	ts    *httptest.Server
	token string
	root  string
	c     *http.Client
	csrf  string
	srv   *Server
}

// newEnv поднимает сервер с тестовым окружением git; opts меняют конфигурацию.
func newEnv(t *testing.T, opts ...func(*config.Config)) *testEnv {
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

	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sb, err := fsx.NewSandbox([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	repos, err := store.Open(filepath.Join(t.TempDir(), "repos.json"))
	if err != nil {
		t.Fatal(err)
	}
	settings, err := store.OpenSettings(filepath.Join(t.TempDir(), "settings.json"), store.DefaultSettings(""))
	if err != nil {
		t.Fatal(err)
	}
	ta, err := auth.NewTokenAuth()
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.SessionTTL = config.Duration{Duration: time.Hour}
	for _, o := range opts {
		o(&cfg)
	}
	srv := New(Deps{
		Cfg: cfg, Auth: ta, Sandbox: sb, Repos: repos, Settings: settings, Git: gitx.NewRunner(),
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &testEnv{ts: ts, token: ta.Token(), root: root, c: &http.Client{Jar: jar}, srv: srv}
}

func (e *testEnv) do(t *testing.T, method, path string, body any, withCSRF bool) (int, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, e.ts.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if withCSRF {
		req.Header.Set("X-CSRF-Token", e.csrf)
	}
	resp, err := e.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

func (e *testEnv) login(t *testing.T) {
	t.Helper()
	code, data := e.do(t, "POST", "/api/login", map[string]string{"username": "u", "password": e.token}, false)
	if code != http.StatusOK {
		t.Fatalf("login: %d %s", code, data)
	}
	var out struct {
		CSRF string `json:"csrf"`
	}
	if err := json.Unmarshal(data, &out); err != nil || out.CSRF == "" {
		t.Fatalf("login response: %s (%v)", data, err)
	}
	e.csrf = out.CSRF
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// makeRepo создаёт репозиторий внутри корня: README и sub/x.txt, один коммит "init".
func (e *testEnv) makeRepo(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(e.root, "proj")
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "init", "-q")
	gitRun(t, dir, "symbolic-ref", "HEAD", "refs/heads/main")
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "x.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func TestHealthNeedsNoLogin(t *testing.T) {
	e := newEnv(t)
	if code, _ := e.do(t, "GET", "/healthz", nil, false); code != http.StatusOK {
		t.Fatalf("healthz: %d", code)
	}
}

func TestAuthRequired(t *testing.T) {
	e := newEnv(t)
	if code, _ := e.do(t, "GET", "/api/repos", nil, false); code != http.StatusUnauthorized {
		t.Errorf("without login: %d", code)
	}
	code, _ := e.do(t, "POST", "/api/login", map[string]string{"username": "u", "password": "wrong"}, false)
	if code != http.StatusUnauthorized {
		t.Errorf("wrong password: %d", code)
	}
	e.login(t)
	if code, _ := e.do(t, "GET", "/api/repos", nil, false); code != http.StatusOK {
		t.Errorf("after login: %d", code)
	}
	if code, _ := e.do(t, "POST", "/api/logout", struct{}{}, true); code != http.StatusOK {
		t.Errorf("logout: %d", code)
	}
	if code, _ := e.do(t, "GET", "/api/repos", nil, false); code != http.StatusUnauthorized {
		t.Errorf("after logout: %d", code)
	}
}

func TestLoginIsRateLimited(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 5; i++ {
		e.do(t, "POST", "/api/login", map[string]string{"password": "wrong"}, false)
	}
	code, _ := e.do(t, "POST", "/api/login", map[string]string{"password": e.token}, false)
	if code != http.StatusTooManyRequests {
		t.Fatalf("after 5 failures even the right token is blocked, got %d", code)
	}
}

func TestCSRFAndOriginAreEnforced(t *testing.T) {
	e := newEnv(t)
	e.login(t)
	body := map[string]string{"path": e.root}
	if code, _ := e.do(t, "POST", "/api/repos", body, false); code != http.StatusForbidden {
		t.Errorf("no CSRF token: %d", code)
	}

	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", e.ts.URL+"/api/repos", bytes.NewReader(b))
	req.Header.Set("X-CSRF-Token", e.csrf)
	req.Header.Set("Origin", "https://evil.example")
	resp, err := e.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("foreign Origin: %d", resp.StatusCode)
	}
}

func TestRepoFlow(t *testing.T) {
	e := newEnv(t)
	e.login(t)
	dir := e.makeRepo(t)

	// добавление репозитория
	code, data := e.do(t, "POST", "/api/repos", map[string]string{"path": dir}, true)
	if code != http.StatusCreated {
		t.Fatalf("add repo: %d %s", code, data)
	}
	var repo struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &repo); err != nil || repo.ID == "" {
		t.Fatalf("repo response: %s", data)
	}
	if code, _ := e.do(t, "POST", "/api/repos", map[string]string{"path": dir}, true); code != http.StatusConflict {
		t.Errorf("duplicate: %d", code)
	}
	outside := t.TempDir()
	if code, _ := e.do(t, "POST", "/api/repos", map[string]string{"path": outside}, true); code != http.StatusForbidden {
		t.Errorf("path outside roots: %d", code)
	}
	if code, _ := e.do(t, "POST", "/api/repos", map[string]string{"path": filepath.Join(dir, "sub")}, true); code != http.StatusBadRequest {
		t.Errorf("not a repository root: %d", code)
	}

	// ветки и статус
	code, data = e.do(t, "GET", "/api/branches?repo="+repo.ID, nil, false)
	if code != http.StatusOK || !strings.Contains(string(data), `"main"`) {
		t.Errorf("branches: %d %s", code, data)
	}
	code, data = e.do(t, "GET", "/api/status?repo="+repo.ID, nil, false)
	if code != http.StatusOK || !strings.Contains(string(data), `"branch":"main"`) {
		t.Errorf("status: %d %s", code, data)
	}

	// таблица файлов: .git скрыт, каталоги идут первыми
	code, data = e.do(t, "GET", "/api/tree?repo="+repo.ID, nil, false)
	if code != http.StatusOK {
		t.Fatalf("tree: %d %s", code, data)
	}
	var tree struct {
		Entries []struct {
			Name  string `json:"name"`
			IsDir bool   `json:"is_dir"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(data, &tree); err != nil {
		t.Fatal(err)
	}
	if len(tree.Entries) != 2 || tree.Entries[0].Name != "sub" || !tree.Entries[0].IsDir || tree.Entries[1].Name != "README" {
		t.Errorf("tree entries: %+v", tree.Entries)
	}

	// последние коммиты
	code, data = e.do(t, "GET", "/api/commits?repo="+repo.ID, nil, false)
	if code != http.StatusOK || !strings.Contains(string(data), `"subject":"init"`) {
		t.Errorf("commits: %d %s", code, data)
	}

	// чтение и сохранение файла
	code, data = e.do(t, "GET", "/api/file?repo="+repo.ID+"&path=README", nil, false)
	if code != http.StatusOK {
		t.Fatalf("read file: %d %s", code, data)
	}
	var file struct {
		Content string `json:"content"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &file); err != nil || file.Content != "hello\n" || file.Version == "" {
		t.Fatalf("file: %s", data)
	}
	put := map[string]any{"content": "changed\n", "version": file.Version}
	code, data = e.do(t, "PUT", "/api/file?repo="+repo.ID+"&path=README", put, true)
	if code != http.StatusOK {
		t.Fatalf("save: %d %s", code, data)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "README")); string(got) != "changed\n" {
		t.Errorf("file on disk: %q", got)
	}
	var saved struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &saved); err != nil || saved.Version == "" || saved.Version == file.Version {
		t.Errorf("version after save: %s", data)
	}
	// старая версия: файл уже изменён
	stale := map[string]any{"content": "again\n", "version": file.Version}
	if code, _ := e.do(t, "PUT", "/api/file?repo="+repo.ID+"&path=README", stale, true); code != http.StatusConflict {
		t.Errorf("stale version must conflict: %d", code)
	}
	// изменение на диске в пределах той же секунды тоже замечается
	full := filepath.Join(dir, "README")
	info, err := os.Stat(full)
	if err != nil {
		t.Fatal(err)
	}
	same := info.ModTime().Truncate(time.Second).Add(time.Millisecond)
	if err := os.Chtimes(full, same, same); err != nil {
		t.Fatal(err)
	}
	_, data = e.do(t, "GET", "/api/file?repo="+repo.ID+"&path=README", nil, false)
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	later := same.Add(500 * time.Millisecond)
	if err := os.Chtimes(full, later, later); err != nil {
		t.Fatal(err)
	}
	sub := map[string]any{"content": "x\n", "version": file.Version}
	if code, _ := e.do(t, "PUT", "/api/file?repo="+repo.ID+"&path=README", sub, true); code != http.StatusConflict {
		t.Errorf("sub-second change must conflict: %d", code)
	}

	// выход за пределы репозитория
	if code, _ := e.do(t, "GET", "/api/file?repo="+repo.ID+"&path=../../etc/passwd", nil, false); code != http.StatusForbidden {
		t.Errorf("path traversal: %d", code)
	}
	if code, _ := e.do(t, "GET", "/api/tree?repo="+repo.ID+"&path=../", nil, false); code != http.StatusForbidden {
		t.Errorf("tree traversal: %d", code)
	}

	// неизвестный репозиторий и неверные данные
	if code, _ := e.do(t, "GET", "/api/branches?repo=nope", nil, false); code != http.StatusNotFound {
		t.Errorf("unknown repo: %d", code)
	}
	bad := map[string]string{"repo": repo.ID, "branch": "--delete"}
	if code, _ := e.do(t, "POST", "/api/git/switch", bad, true); code != http.StatusBadRequest {
		t.Errorf("option-like branch: %d", code)
	}
}

func TestWorktreeEndpoints(t *testing.T) {
	e := newEnv(t)
	e.login(t)
	dir := e.makeRepo(t)
	_, data := e.do(t, "POST", "/api/repos", map[string]string{"path": dir}, true)
	var repo struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &repo); err != nil {
		t.Fatal(err)
	}

	req := map[string]any{"repo": repo.ID, "branch": "feature/login", "create": true}
	code, data := e.do(t, "POST", "/api/worktrees", req, true)
	if code != http.StatusCreated {
		t.Fatalf("create worktree: %d %s", code, data)
	}
	var created struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(data, &created); err != nil {
		t.Fatal(err)
	}
	if filepath.Base(created.Path) != "feature-login" {
		t.Errorf("worktree path: %q", created.Path)
	}

	// файлы новой вкладки читаются через параметр wt
	code, data = e.do(t, "GET", "/api/tree?repo="+repo.ID+"&wt="+created.Path, nil, false)
	if code != http.StatusOK || !strings.Contains(string(data), "README") {
		t.Errorf("tree of worktree: %d %s", code, data)
	}
	// произвольный каталог как wt не принимается
	if code, _ := e.do(t, "GET", "/api/tree?repo="+repo.ID+"&wt="+t.TempDir(), nil, false); code != http.StatusNotFound {
		t.Errorf("foreign wt: %d", code)
	}

	code, data = e.do(t, "DELETE", "/api/worktrees?repo="+repo.ID+"&wt="+created.Path, nil, true)
	if code != http.StatusOK {
		t.Errorf("delete worktree: %d %s", code, data)
	}
}
