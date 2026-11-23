package server

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Maxi-Boy/gitaway/internal/auth"
	"github.com/Maxi-Boy/gitaway/internal/gitx"
)

// searchTimeout ограничивает поиск и историю: на очень большом репозитории лучше
// показать часть результатов, чем держать запрос.
const searchTimeout = 20 * time.Second

// handleFiles: GET /api/files?repo=&wt= — все файлы вкладки для быстрого открытия (Ctrl+P).
func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
	if r.Method != http.MethodGet {
		return errMethod
	}
	_, dir, err := s.workdir(r)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(r.Context(), searchTimeout)
	defer cancel()
	list, err := s.git.ListFiles(ctx, dir)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, list)
	return nil
}

// handleSearch: GET /api/search?repo=&wt=&q=&regex=1&case=1&word=1&files=*.go,!vendor/
// — поиск текста по файлам вкладки.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
	if r.Method != http.MethodGet {
		return errMethod
	}
	_, dir, err := s.workdir(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	var globs []string
	for _, g := range strings.Split(q.Get("files"), ",") {
		if g = strings.TrimSpace(g); g != "" {
			globs = append(globs, g)
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), searchTimeout)
	defer cancel()
	res, err := s.git.Search(ctx, dir, gitx.SearchOptions{
		Query: q.Get("q"), Regex: q.Get("regex") == "1", Case: q.Get("case") == "1", Word: q.Get("word") == "1", Globs: globs,
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, res)
	return nil
}

// handleLog: GET /api/log?repo=&wt=&path=&skip=&limit= — страница истории ветки вкладки
// (или одного файла). На первой странице отмечаются коммиты, ещё не отправленные в upstream.
func (s *Server) handleLog(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
	if r.Method != http.MethodGet {
		return errMethod
	}
	_, dir, err := s.workdir(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	path, isDir := q.Get("path"), false
	if path != "" {
		clean, err := s.checkPaths(dir, []string{path})
		if err != nil {
			return err
		}
		path = clean[0]
		if full, err := s.sandbox.ResolveUnder(dir, path, false); err == nil {
			if fi, err := os.Stat(full); err == nil && fi.IsDir() {
				isDir = true
			}
		}
	}
	skip, _ := strconv.Atoi(q.Get("skip"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	ctx, cancel := context.WithTimeout(r.Context(), searchTimeout)
	defer cancel()
	page, err := s.git.Log(ctx, dir, gitx.LogOptions{Path: path, Dir: isDir, Skip: skip, Limit: limit})
	if err != nil {
		return err
	}
	if len(page.Commits) > 0 {
		unpushed := s.git.Unpushed(ctx, dir, 1000)
		for i := range page.Commits {
			page.Commits[i].Unpushed = unpushed[page.Commits[i].Hash]
		}
	}
	writeJSON(w, http.StatusOK, page)
	return nil
}

// handleCommit: GET /api/commit?repo=&wt=&hash= — описание коммита и изменённые файлы.
func (s *Server) handleCommit(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
	if r.Method != http.MethodGet {
		return errMethod
	}
	_, dir, err := s.workdir(r)
	if err != nil {
		return err
	}
	d, err := s.git.ShowCommit(r.Context(), dir, r.URL.Query().Get("hash"))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, d)
	return nil
}

// handleCommitDiff: GET /api/commit/diff?repo=&wt=&hash=&path=&old= — diff файла в коммите.
// Пути проверяются только по форме: файла в рабочей папке уже может не быть.
func (s *Server) handleCommitDiff(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
	if r.Method != http.MethodGet {
		return errMethod
	}
	_, dir, err := s.workdir(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	d, err := s.git.CommitDiff(r.Context(), dir, q.Get("hash"), q.Get("path"), q.Get("old"))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, d)
	return nil
}
