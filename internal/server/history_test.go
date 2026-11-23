package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Maxi-Boy/gitaway/internal/config"
	"github.com/Maxi-Boy/gitaway/internal/github"
	"github.com/Maxi-Boy/gitaway/internal/gitx"
)

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

func TestFilesAndSearch(t *testing.T) {
	e := newEnv(t)
	e.login(t)
	id, dir := e.addRepo(t)
	mkfile(t, filepath.Join(dir, "sub", "find me.go"), "package sub\n// TODO: fix\n")
	q := "?repo=" + url.QueryEscape(id)

	code, body := e.do(t, "GET", "/api/files"+q, nil, false)
	var files gitx.FileList
	if code != http.StatusOK || json.Unmarshal(body, &files) != nil {
		t.Fatalf("files: %d %s", code, body)
	}
	if got := strings.Join(files.Files, ","); !strings.Contains(got, "README") || !strings.Contains(got, "sub/find me.go") {
		t.Errorf("files: %s", got)
	}

	code, body = e.do(t, "GET", "/api/search"+q+"&q="+url.QueryEscape("todo:")+"&files="+url.QueryEscape("*.go, !vendor/"), nil, false)
	var res gitx.SearchResult
	if code != http.StatusOK || json.Unmarshal(body, &res) != nil || len(res.Matches) != 1 ||
		res.Matches[0].Path != "sub/find me.go" || res.Matches[0].Line != 2 {
		t.Errorf("search: %d %s", code, body)
	}
	for _, bad := range []string{"&q=", "&q=x&files=" + url.QueryEscape(":(top)x"), "&q=(&regex=1"} {
		if code, body := e.do(t, "GET", "/api/search"+q+bad, nil, false); code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", bad, code, body)
		}
	}
	if code, _ := e.do(t, "GET", "/api/files?repo=nope", nil, false); code != http.StatusNotFound {
		t.Errorf("unknown repo: %d", code)
	}
}

func TestHistoryAndCommit(t *testing.T) {
	e := newEnv(t)
	e.login(t)
	id, dir := e.addRepo(t)
	mkfile(t, filepath.Join(dir, "README"), "hello\nworld\n")
	gitRun(t, dir, "commit", "-q", "-am", "second\n\nmore text")
	head := gitOut(t, dir, "rev-parse", "HEAD")
	q := "?repo=" + url.QueryEscape(id)

	code, body := e.do(t, "GET", "/api/log"+q+"&limit=1", nil, false)
	var page gitx.LogPage
	if code != http.StatusOK || json.Unmarshal(body, &page) != nil || len(page.Commits) != 1 || !page.More || page.Commits[0].Hash != head {
		t.Fatalf("log: %d %s", code, body)
	}
	_, body = e.do(t, "GET", "/api/log"+q+"&path=sub/x.txt", nil, false)
	if json.Unmarshal(body, &page) != nil || len(page.Commits) != 1 || page.Commits[0].Subject != "init" {
		t.Errorf("file log: %s", body)
	}
	if code, _ := e.do(t, "GET", "/api/log"+q+"&path=../etc", nil, false); code != http.StatusBadRequest && code != http.StatusForbidden {
		t.Errorf("log outside: %d", code)
	}

	code, body = e.do(t, "GET", "/api/commit"+q+"&hash="+head[:7], nil, false)
	var d gitx.CommitDetail
	if code != http.StatusOK || json.Unmarshal(body, &d) != nil || d.Body != "more text" || len(d.Files) != 1 || d.Files[0].Added != 1 {
		t.Fatalf("commit: %d %s", code, body)
	}
	code, body = e.do(t, "GET", "/api/commit/diff"+q+"&hash="+head+"&path=README", nil, false)
	if code != http.StatusOK || !strings.Contains(string(body), "+world") {
		t.Errorf("commit diff: %d %s", code, body)
	}
	for _, bad := range []string{"/api/commit" + q + "&hash=HEAD", "/api/commit" + q + "&hash=--all", "/api/commit/diff" + q + "&hash=" + head + "&path=../x"} {
		if code, body := e.do(t, "GET", bad, nil, false); code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", bad, code, body)
		}
	}
}

// TestPullNeedsLogin проверяет понятную ошибку, когда remote по HTTP(S) просит логин.
func TestPullNeedsLogin(t *testing.T) {
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="GitHub"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer gh.Close()
	e := newEnv(t)
	e.login(t)
	id, dir := e.addRepo(t)
	gitRun(t, dir, "remote", "add", "origin", gh.URL+"/acme/private.git")
	code, body := e.do(t, "POST", "/api/git/fetch", map[string]any{"repo": id}, true)
	if code != http.StatusUnprocessableEntity || !strings.Contains(string(body), `"auth_required"`) {
		t.Errorf("fetch without login: %d %s", code, body)
	}
}

func TestGitHubAttachKey(t *testing.T) {
	data := t.TempDir()
	e := newEnv(t, func(c *config.Config) { c.DataDir = data })
	e.login(t)
	id, dir := e.addRepo(t)
	bare := filepath.Join(t.TempDir(), "widgets.git")
	gitRun(t, dir, "clone", "-q", "--bare", dir, bare)
	e.srv.ghURL = func(ref github.Ref, transport string) string { return bare }

	if code, body := e.do(t, "POST", "/api/github/key", map[string]any{"repo": id}, true); code != http.StatusBadRequest || !strings.Contains(string(body), "no_github_remote") {
		t.Errorf("no github remote: %d %s", code, body)
	}
	gitRun(t, dir, "remote", "add", "upstream", "https://github.com/someone/else.git")
	gitRun(t, dir, "remote", "add", "origin", "https://github.com/acme/widgets.git")
	code, body := e.do(t, "POST", "/api/github/key", map[string]any{"repo": id}, true)
	var key struct {
		KeyID string `json:"key_id"`
		Owner string `json:"owner"`
		Repo  string `json:"repo"`
	}
	if code != http.StatusOK || json.Unmarshal(body, &key) != nil || key.Owner != "acme" || key.Repo != "widgets" {
		t.Fatalf("key for repo: %d %s", code, body)
	}
	attach := map[string]any{"repo": id, "key_id": key.KeyID}
	if code, body := e.do(t, "POST", "/api/github/attach", attach, true); code != http.StatusConflict {
		t.Errorf("attach before check: %d %s", code, body)
	}
	if code, body := e.do(t, "POST", "/api/github/check", map[string]any{"key_id": key.KeyID}, true); code != http.StatusOK || !strings.Contains(string(body), `"ok":true`) {
		t.Fatalf("check: %d %s", code, body)
	}
	if code, body := e.do(t, "POST", "/api/github/attach", attach, true); code != http.StatusOK {
		t.Fatalf("attach: %d %s", code, body)
	}
	if got := gitOut(t, dir, "remote", "get-url", "origin"); got != bare {
		t.Errorf("origin url: %s", got)
	}
	if got := gitOut(t, dir, "remote", "get-url", "upstream"); got != "https://github.com/someone/else.git" {
		t.Errorf("other remote must stay: %s", got)
	}
	if got := gitOut(t, dir, "config", "core.sshCommand"); !strings.Contains(got, filepath.Join(data, "keys", key.KeyID)) {
		t.Errorf("core.sshCommand: %s", got)
	}
	if code, body := e.do(t, "POST", "/api/git/fetch", map[string]any{"repo": id, "remote": "origin"}, true); code != http.StatusOK {
		t.Errorf("fetch after attach: %d %s", code, body)
	}

	// ключ от другого репозитория GitHub подключить нельзя
	_, body = e.do(t, "POST", "/api/github/key", map[string]any{"url": "acme/other"}, true)
	var other struct {
		KeyID string `json:"key_id"`
	}
	_ = json.Unmarshal(body, &other)
	e.do(t, "POST", "/api/github/check", map[string]any{"key_id": other.KeyID}, true)
	gitRun(t, dir, "remote", "set-url", "origin", "git@github.com:acme/widgets.git")
	if code, body := e.do(t, "POST", "/api/github/attach", map[string]any{"repo": id, "key_id": other.KeyID}, true); code != http.StatusBadRequest {
		t.Errorf("foreign key: %d %s", code, body)
	}
}
