package server

import (
	"context"
	"net/http"

	"github.com/Maxi-Boy/gitaway/internal/auth"
	"github.com/Maxi-Boy/gitaway/internal/gitx"
)

// changeRequest это тело запросов stage, unstage, discard и commit.
type changeRequest struct {
	Repo    string   `json:"repo"`
	WT      string   `json:"wt"`
	Paths   []string `json:"paths"`
	Message string   `json:"message"`
}

type changeOp func(ctx context.Context, dir string, q changeRequest) (map[string]any, error)

// checkPaths проверяет пути (формат в gitx, границы через Sandbox: symlink
// наружу не пройдёт) и возвращает их в очищенном виде.
func (s *Server) checkPaths(dir string, paths []string) ([]string, error) {
	clean, err := gitx.CleanPaths(paths)
	if err != nil {
		return nil, err
	}
	for _, p := range clean {
		if _, err := s.sandbox.ResolveUnder(dir, p, false); err != nil {
			return nil, err
		}
	}
	return clean, nil
}

// changeHandler выполняет изменение под блокировкой репозитория и отвечает
// результатом вместе со свежим статусом, чтобы интерфейсу не делать второй запрос.
func (s *Server) changeHandler(op changeOp) apiHandler {
	return func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		if r.Method != http.MethodPost {
			return errMethod
		}
		var q changeRequest
		if err := decodeJSON(w, r, &q); err != nil {
			return err
		}
		repo, ok := s.repos.Get(q.Repo)
		if !ok {
			return errNotFound("repository")
		}
		dir, err := s.workdirFor(r.Context(), repo, q.WT)
		if err != nil {
			return err
		}
		unlock, ok := s.locks.TryLock(repo.ID)
		if !ok {
			return errBusy
		}
		defer unlock()
		res, err := op(r.Context(), dir, q)
		if err != nil {
			return err
		}
		st, err := s.git.Status(r.Context(), dir)
		if err != nil {
			return err
		}
		res["status"] = st
		writeJSON(w, http.StatusOK, res)
		return nil
	}
}

func (s *Server) registerChanges(mux *http.ServeMux) {
	reg := func(path string, op changeOp) {
		mux.HandleFunc(path, s.api(s.changeHandler(op)))
	}
	reg("/api/git/stage", func(ctx context.Context, dir string, q changeRequest) (map[string]any, error) {
		paths, err := s.checkPaths(dir, q.Paths)
		if err != nil {
			return nil, err
		}
		return map[string]any{}, s.git.Stage(ctx, dir, paths)
	})
	reg("/api/git/unstage", func(ctx context.Context, dir string, q changeRequest) (map[string]any, error) {
		paths, err := s.checkPaths(dir, q.Paths)
		if err != nil {
			return nil, err
		}
		return map[string]any{}, s.git.Unstage(ctx, dir, paths)
	})
	reg("/api/git/discard", func(ctx context.Context, dir string, q changeRequest) (map[string]any, error) {
		paths, err := s.checkPaths(dir, q.Paths)
		if err != nil {
			return nil, err
		}
		touched, err := s.git.Discard(ctx, dir, paths)
		return map[string]any{"discarded": touched}, err
	})
	reg("/api/git/commit", func(ctx context.Context, dir string, q changeRequest) (map[string]any, error) {
		if len(q.Paths) > 0 {
			return nil, errBad("commit takes the index as is; stage files first")
		}
		hash, out, err := s.git.Commit(ctx, dir, q.Message)
		return map[string]any{"commit": hash, "output": out}, err
	})
}

// handleDiff отдаёт diff файла: GET /api/diff?repo=&wt=&path=&staged=1.
func (s *Server) handleDiff(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
	if r.Method != http.MethodGet {
		return errMethod
	}
	_, dir, err := s.workdir(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	paths, err := s.checkPaths(dir, []string{q.Get("path")})
	if err != nil {
		return err
	}
	d, err := s.git.FileDiff(r.Context(), dir, paths[0], q.Get("staged") == "1")
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, d)
	return nil
}
