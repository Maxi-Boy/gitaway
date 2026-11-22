package server

import (
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/Maxi-Boy/gitaway/internal/auth"
	"github.com/Maxi-Boy/gitaway/internal/store"
)

// handleRepos: GET список, POST добавить существующий репозиторий, DELETE убрать из реестра.
// Клонирование по URL и deploy-ключи будут добавлены отдельным этапом.
func (s *Server) handleRepos(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.repos.List())
		return nil

	case http.MethodPost:
		var req struct {
			Path string `json:"path"`
		}
		if err := decodeJSON(w, r, &req); err != nil {
			return err
		}
		dir, err := s.sandbox.Resolve(req.Path)
		if err != nil {
			return err
		}
		top, err := s.git.Run(r.Context(), dir, "rev-parse", "--show-toplevel")
		if err != nil {
			return errBad("not a git repository")
		}
		topReal, err := filepath.EvalSymlinks(strings.TrimSpace(top))
		if err != nil || topReal != dir {
			return errBad("path must be the root of a git repository")
		}
		remote, _ := s.git.Run(r.Context(), dir, "config", "--get", "remote.origin.url")
		repo, err := s.repos.Add(dir, stripCreds(strings.TrimSpace(remote)))
		if errors.Is(err, store.ErrDuplicate) {
			return &apiError{http.StatusConflict, "duplicate", "repository already added"}
		}
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusCreated, repo)
		return nil

	case http.MethodDelete:
		err := s.repos.Remove(r.URL.Query().Get("id"))
		if errors.Is(err, store.ErrNotFound) {
			return errNotFound("repository")
		}
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return nil
	}
	return errMethod
}

// stripCreds убирает логин и пароль из URL перед сохранением.
func stripCreds(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	u.User = nil
	return u.String()
}
