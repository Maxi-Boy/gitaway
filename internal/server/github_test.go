package server

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Maxi-Boy/gitaway/internal/config"
	"github.com/Maxi-Boy/gitaway/internal/github"
)

func TestGitHubConnectAndClone(t *testing.T) {
	data := t.TempDir()
	e := newEnv(t, func(c *config.Config) { c.DataDir = data })
	e.login(t)

	// «GitHub» в тесте это локальный bare-репозиторий
	src := e.makeRepo(t)
	bare := filepath.Join(t.TempDir(), "widgets.git")
	gitRun(t, src, "clone", "-q", "--bare", src, bare)
	e.srv.ghURL = func(ref github.Ref, transport string) string { return bare }

	code, body := e.do(t, "POST", "/api/github/key", map[string]any{"url": "https://github.com/acme/widgets"}, true)
	var key struct {
		KeyID     string `json:"key_id"`
		PublicKey string `json:"public_key"`
		KeysURL   string `json:"keys_url"`
	}
	if code != http.StatusOK || json.Unmarshal(body, &key) != nil || key.KeyID == "" ||
		!strings.HasPrefix(key.PublicKey, "ssh-ed25519 ") || key.KeysURL != "https://github.com/acme/widgets/settings/keys/new" {
		t.Fatalf("key: %d %s", code, body)
	}
	if fi, err := os.Stat(filepath.Join(data, "keys", key.KeyID)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("private key file: %v %v", fi, err)
	}
	if fi, err := os.Stat(filepath.Join(data, "keys")); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("keys dir: %v %v", fi, err)
	}
	// тот же репозиторий: тот же ключ (его уже могли добавить на GitHub)
	_, body = e.do(t, "POST", "/api/github/key", map[string]any{"url": "git@github.com:acme/widgets.git"}, true)
	if !strings.Contains(string(body), key.KeyID) {
		t.Errorf("second key request must reuse the key: %s", body)
	}

	dest := filepath.Join(e.root, "widgets")
	clone := map[string]any{"key_id": key.KeyID, "dest": dest}
	if code, body := e.do(t, "POST", "/api/github/clone", clone, true); code != http.StatusConflict || !strings.Contains(string(body), "not_checked") {
		t.Errorf("clone before check: %d %s", code, body)
	}
	code, body = e.do(t, "POST", "/api/github/check", map[string]any{"key_id": key.KeyID}, true)
	if code != http.StatusOK || !strings.Contains(string(body), `"ok":true`) || !strings.Contains(string(body), `"transport":"ssh22"`) {
		t.Fatalf("check: %d %s", code, body)
	}

	code, body = e.do(t, "POST", "/api/github/clone", clone, true)
	var started struct {
		Session sessionInfo `json:"session"`
		Dest    string      `json:"dest"`
	}
	if code != http.StatusCreated || json.Unmarshal(body, &started) != nil || started.Dest != dest {
		t.Fatalf("clone: %d %s", code, body)
	}
	if done := e.waitSession(t, started.Session.ID); done.ExitCode != 0 {
		t.Fatalf("clone exit code %d", done.ExitCode)
	}
	if _, err := os.Stat(filepath.Join(dest, "README")); err != nil {
		t.Fatalf("cloned files missing: %v", err)
	}
	// репозиторий в списке (регистрация идёт сразу после выхода процесса)
	var repos []struct {
		Path  string `json:"path"`
		KeyID string `json:"key_id"`
	}
	for i := 0; i < 100; i++ {
		_, body = e.do(t, "GET", "/api/repos", nil, false)
		if json.Unmarshal(body, &repos) == nil && len(repos) == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(repos) != 1 || repos[0].Path != dest || repos[0].KeyID != key.KeyID {
		t.Fatalf("repos: %s", body)
	}
	out, err := exec.Command("git", "-C", dest, "config", "core.sshCommand").Output()
	if err != nil || !strings.Contains(string(out), filepath.Join(data, "keys", key.KeyID)) || !strings.Contains(string(out), "StrictHostKeyChecking=yes") {
		t.Errorf("core.sshCommand: %q %v", out, err)
	}
	if kh, err := os.ReadFile(filepath.Join(data, "known_hosts")); err != nil || string(kh) != github.KnownHosts {
		t.Errorf("known_hosts: %v", err)
	}

	bad := []struct {
		name string
		path string
		body map[string]any
		want int
	}{
		{"not github", "/api/github/key", map[string]any{"url": "https://gitlab.com/a/b"}, http.StatusBadRequest},
		{"unknown key check", "/api/github/check", map[string]any{"key_id": "nope"}, http.StatusNotFound},
		{"dest exists", "/api/github/clone", map[string]any{"key_id": key.KeyID, "dest": dest}, http.StatusConflict},
		{"dest outside roots", "/api/github/clone", map[string]any{"key_id": key.KeyID, "dest": filepath.Join(t.TempDir(), "x")}, http.StatusForbidden},
		{"relative dest", "/api/github/clone", map[string]any{"key_id": key.KeyID, "dest": "x"}, http.StatusBadRequest},
		{"no source", "/api/github/clone", map[string]any{"dest": filepath.Join(e.root, "y")}, http.StatusBadRequest},
		{"public bad url", "/api/github/clone", map[string]any{"public": true, "url": "evil.com/a/b", "dest": filepath.Join(e.root, "z")}, http.StatusBadRequest},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			if code, data := e.do(t, "POST", c.path, c.body, true); code != c.want {
				t.Errorf("got %d, want %d: %s", code, c.want, data)
			}
		})
	}

	// недоступный «GitHub»: проверка отвечает ok:false с причиной, а не ошибкой сервера
	e.srv.ghURL = func(github.Ref, string) string { return filepath.Join(t.TempDir(), "missing.git") }
	code, body = e.do(t, "POST", "/api/github/check", map[string]any{"key_id": key.KeyID}, true)
	if code != http.StatusOK || !strings.Contains(string(body), `"ok":false`) {
		t.Errorf("failed check: %d %s", code, body)
	}
}
