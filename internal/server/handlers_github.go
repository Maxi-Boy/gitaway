package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Maxi-Boy/gitaway/internal/auth"
	"github.com/Maxi-Boy/gitaway/internal/github"
	"github.com/Maxi-Boy/gitaway/internal/gitx"
	"github.com/Maxi-Boy/gitaway/internal/session"
	"github.com/Maxi-Boy/gitaway/internal/store"
)

// KeysDoc хранит описания deploy-ключей; закрытые части лежат в data_dir/keys/<id> (0600).
type KeysDoc = store.Doc[[]github.Key]

func (s *Server) keysDir() string { return filepath.Join(s.cfg.DataDir, "keys") }

func (s *Server) keyPath(id string) string { return filepath.Join(s.keysDir(), id) }

// knownHosts записывает (если нужно) закреплённый ключ хоста GitHub и возвращает путь к файлу.
func (s *Server) knownHosts() (string, error) {
	path := filepath.Join(s.cfg.DataDir, "known_hosts")
	if b, err := os.ReadFile(path); err == nil && string(b) == github.KnownHosts {
		return path, nil
	}
	if err := os.MkdirAll(s.cfg.DataDir, 0o700); err != nil {
		return "", err
	}
	return path, os.WriteFile(path, []byte(github.KnownHosts), 0o600)
}

func (s *Server) sshCommand(keyID string) (string, error) {
	kh, err := s.knownHosts()
	if err != nil {
		return "", err
	}
	return github.SSHCommand(s.keyPath(keyID), kh), nil
}

func (s *Server) findKey(id string) (github.Key, bool) {
	for _, k := range s.keys.Get() {
		if k.ID == id {
			return k, true
		}
	}
	return github.Key{}, false
}

// remoteURL это адрес для git; в тестах подменяется на локальный bare-репозиторий.
func (s *Server) remoteURL(ref github.Ref, transport string) string {
	if s.ghURL != nil {
		return s.ghURL(ref, transport)
	}
	if transport == "https" {
		return ref.HTTPSURL()
	}
	return ref.SSHURL(transport)
}

// handleGitHubKey: POST {url} или {repo} — deploy-ключ для репозитория (новый или уже созданный для него).
// В ответе открытая часть и ссылка на страницу добавления ключа в настройках репозитория.
func (s *Server) handleGitHubKey(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
	if r.Method != http.MethodPost {
		return errMethod
	}
	var req struct {
		URL  string `json:"url"`
		Repo string `json:"repo"` // уже добавленный репозиторий: адрес берётся из его remote
	}
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	var ref github.Ref
	var err error
	if req.Repo != "" {
		repo, ok := s.repos.Get(req.Repo)
		if !ok {
			return errNotFound("repository")
		}
		dir, err := s.sandbox.Resolve(repo.Path)
		if err != nil {
			return err
		}
		if _, ref, err = s.githubRemote(r.Context(), dir); err != nil {
			return err
		}
	} else if ref, err = github.Parse(req.URL); err != nil {
		return errBad("%v", err)
	}
	key, ok := github.Key{}, false
	for _, k := range s.keys.Get() {
		if strings.EqualFold(k.Ref.String(), ref.String()) {
			if _, err := os.Stat(s.keyPath(k.ID)); err == nil {
				key, ok = k, true
			}
		}
	}
	if !ok {
		host, _ := os.Hostname()
		priv, pub, err := github.GenerateKey("gitaway@" + host + " " + ref.String())
		if err != nil {
			return err
		}
		b := make([]byte, 6)
		_, _ = rand.Read(b)
		key = github.Key{ID: strings.ToLower(ref.Owner+"-"+ref.Repo) + "-" + hex.EncodeToString(b), Ref: ref, Public: pub, Created: time.Now().UTC()}
		if err := os.MkdirAll(s.keysDir(), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(s.keyPath(key.ID), priv, 0o600); err != nil {
			return err
		}
		if err := s.keys.Update(func(v *[]github.Key) error { *v = append(*v, key); return nil }); err != nil {
			_ = os.Remove(s.keyPath(key.ID))
			return err
		}
		s.log.Info("deploy key created", "repo", ref.String(), "key", key.ID)
	}
	host, _ := os.Hostname()
	writeJSON(w, http.StatusOK, map[string]any{
		"key_id": key.ID, "owner": ref.Owner, "repo": ref.Repo, "public_key": key.Public,
		"keys_url": ref.KeysPage(), "title": "gitaway@" + host, "transport": key.Transport,
	})
	return nil
}

// handleGitHubCheck: POST {key_id} — проверить доступ ключом: git ls-remote по SSH (порт 22,
// при сетевой ошибке SSH через 443). Ответ всегда 200 с ok и причиной: это результат проверки.
func (s *Server) handleGitHubCheck(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
	if r.Method != http.MethodPost {
		return errMethod
	}
	var req struct {
		KeyID string `json:"key_id"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	key, ok := s.findKey(req.KeyID)
	if !ok {
		return errNotFound("key")
	}
	sshCmd, err := s.sshCommand(key.ID)
	if err != nil {
		return err
	}
	var code, output string
	for _, transport := range []string{github.SSH22, github.SSH443} {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		out, err := s.lsRemote(ctx, s.remoteURL(key.Ref, transport), sshCmd)
		cancel()
		if err == nil {
			_ = s.keys.Update(func(v *[]github.Key) error {
				for i := range *v {
					if (*v)[i].ID == key.ID {
						(*v)[i].Transport = transport
					}
				}
				return nil
			})
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "transport": transport, "branches": strings.Count(out, "refs/heads/")})
			return nil
		}
		output = errText(err)
		code = github.Classify(output)
		if code != github.FailNetwork {
			break // ключ не добавлен или нет репозитория: другой порт не поможет
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": false, "code": code, "output": lastLines(output, 6)})
	return nil
}

func (s *Server) lsRemote(ctx context.Context, url, sshCmd string) (string, error) {
	if err := gitx.ValidCloneURL(url); err != nil && !filepath.IsAbs(url) {
		return "", err
	}
	return s.git.RunEnv(ctx, "", []string{"GIT_SSH_COMMAND=" + sshCmd}, "ls-remote", "--heads", "--", url)
}

func errText(err error) string {
	var ge *gitx.Error
	if errors.As(err, &ge) {
		return ge.Output
	}
	return err.Error()
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// handleGitHubClone: POST {key_id, dest} или {url, public: true, dest} — клонировать в новую
// папку внутри корней. Клонирование идёт сеансом (вывод в терминале); после успеха репозиторий
// получает core.sshCommand с ключом (fetch, pull, push работают и из терминала) и попадает в список.
func (s *Server) handleGitHubClone(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
	if r.Method != http.MethodPost {
		return errMethod
	}
	var req struct {
		KeyID  string `json:"key_id"`
		URL    string `json:"url"`
		Public bool   `json:"public"`
		Dest   string `json:"dest"`
		Cols   uint16 `json:"cols"`
		Rows   uint16 `json:"rows"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	var ref github.Ref
	var url, sshCmd string
	var key github.Key
	switch {
	case req.KeyID != "":
		var ok bool
		if key, ok = s.findKey(req.KeyID); !ok {
			return errNotFound("key")
		}
		if key.Transport == "" {
			return &apiError{http.StatusConflict, "not_checked", "check access with the key first"}
		}
		var err error
		if sshCmd, err = s.sshCommand(key.ID); err != nil {
			return err
		}
		ref, url = key.Ref, s.remoteURL(key.Ref, key.Transport)
	case req.Public:
		var err error
		if ref, err = github.Parse(req.URL); err != nil {
			return errBad("%v", err)
		}
		url = s.remoteURL(ref, "https")
	default:
		return errBad("key_id or a public url is required")
	}

	dest, err := s.sandbox.ResolveNew(req.Dest)
	if err != nil {
		return err
	}
	if hasGitSegment(dest) {
		return errBad("cannot clone into .git")
	}
	if _, err := os.Lstat(dest); err == nil {
		return &apiError{http.StatusConflict, "exists", "the target folder already exists"}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := requireDir(filepath.Dir(dest)); err != nil {
		return err
	}
	unlock, ok := s.locks.TryLock("clone:" + dest)
	if !ok {
		return errBusy
	}

	env := []string{"GIT_TERMINAL_PROMPT=0"}
	if sshCmd != "" {
		env = append(env, "GIT_SSH_COMMAND="+sshCmd)
	}
	gitBin := s.git.GitBin
	if gitBin == "" {
		gitBin = "git"
	}
	sess, err := s.terms.Start(session.Options{
		Kind: "clone", Title: "clone " + ref.String(), Dir: filepath.Dir(dest), Cols: req.Cols, Rows: req.Rows,
		Args: []string{gitBin, "clone", "--progress", "--", url, dest}, Env: env, Timeout: time.Hour,
	})
	if err != nil {
		unlock()
		return err
	}
	go s.afterClone(sess, unlock, dest, ref, key, sshCmd)
	s.log.Info("clone started", "repo", ref.String(), "dest", dest)
	writeJSON(w, http.StatusCreated, map[string]any{"session": sess.Info(), "dest": dest})
	return nil
}

// afterClone ждёт конца клонирования и регистрирует репозиторий.
func (s *Server) afterClone(sess *session.Session, unlock func(), dest string, ref github.Ref, key github.Key, sshCmd string) {
	defer unlock()
	<-sess.Done()
	if sess.Info().ExitCode != 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if sshCmd != "" {
		if _, err := s.git.Run(ctx, dest, "config", "core.sshCommand", sshCmd); err != nil {
			s.log.Error("set core.sshCommand", "dest", dest, "err", err)
		}
	}
	repo, err := s.repos.Add(dest, ref.HTTPSURL())
	if err != nil {
		s.log.Error("register cloned repo", "dest", dest, "err", err)
		return
	}
	if key.ID != "" {
		if err := s.repos.SetKeyID(repo.ID, key.ID); err != nil {
			s.log.Error("link key", "dest", dest, "err", err)
		}
	}
	s.log.Info("repository cloned", "repo", ref.String(), "dest", dest)
}

// githubRemote находит remote репозитория на github.com: origin, иначе первый подходящий.
func (s *Server) githubRemote(ctx context.Context, dir string) (string, github.Ref, error) {
	out, err := s.git.RunEnv(ctx, dir, []string{"GIT_OPTIONAL_LOCKS=0"}, "config", "--get-regexp", `^remote\..*\.url$`)
	var ge *gitx.Error
	if err != nil && !(errors.As(err, &ge) && ge.Code == 1) { // код 1: ни одного remote
		return "", github.Ref{}, err
	}
	name, ref, found := "", github.Ref{}, false
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		key, url, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		remote := strings.TrimSuffix(strings.TrimPrefix(key, "remote."), ".url")
		rf, err := github.Parse(url)
		if err != nil || gitx.ValidRemoteName(remote) != nil {
			continue
		}
		if !found || remote == "origin" {
			name, ref, found = remote, rf, true
		}
	}
	if !found {
		return "", github.Ref{}, &apiError{http.StatusBadRequest, "no_github_remote", "the repository has no github.com remote"}
	}
	return name, ref, nil
}

// handleGitHubAttach: POST {repo, key_id} — подключить проверенный deploy-ключ к уже
// добавленному репозиторию: remote на github.com переводится на SSH, в .git/config пишется
// core.sshCommand с ключом (общий для всех вкладок-веток). Ключ должен быть для того же
// репозитория GitHub, что и remote.
func (s *Server) handleGitHubAttach(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
	if r.Method != http.MethodPost {
		return errMethod
	}
	var req struct {
		Repo  string `json:"repo"`
		KeyID string `json:"key_id"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	repo, ok := s.repos.Get(req.Repo)
	if !ok {
		return errNotFound("repository")
	}
	key, ok := s.findKey(req.KeyID)
	if !ok {
		return errNotFound("key")
	}
	if key.Transport == "" {
		return &apiError{http.StatusConflict, "not_checked", "check access with the key first"}
	}
	dir, err := s.sandbox.Resolve(repo.Path)
	if err != nil {
		return err
	}
	unlock, ok := s.locks.TryLock(repo.ID)
	if !ok {
		return errBusy
	}
	defer unlock()
	remote, ref, err := s.githubRemote(r.Context(), dir)
	if err != nil {
		return err
	}
	if !strings.EqualFold(ref.String(), key.Ref.String()) {
		return errBad("the key is for %s, but the remote points to %s", key.Ref, ref)
	}
	sshCmd, err := s.sshCommand(key.ID)
	if err != nil {
		return err
	}
	url := s.remoteURL(key.Ref, key.Transport)
	if _, err := s.git.Run(r.Context(), dir, "config", "core.sshCommand", sshCmd); err != nil {
		return err
	}
	if _, err := s.git.Run(r.Context(), dir, "remote", "set-url", "--", remote, url); err != nil {
		return err
	}
	if err := s.repos.SetKeyID(repo.ID, key.ID); err != nil {
		return err
	}
	s.log.Info("deploy key attached", "repo", ref.String(), "path", dir, "remote", remote)
	writeJSON(w, http.StatusOK, map[string]any{"remote": remote, "url": url})
	return nil
}
