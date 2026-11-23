package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/Maxi-Boy/gitaway/internal/auth"
	"github.com/Maxi-Boy/gitaway/internal/session"
	"github.com/Maxi-Boy/gitaway/internal/store"
	"github.com/Maxi-Boy/gitaway/internal/tasks"
)

const (
	maxMyTasks    = 100 // моих скриптов всего
	maxTrustPerRe = 10  // запомненных версий .gitaway.json на репозиторий
)

// TrustDoc хранит доверие к .gitaway.json: id репозитория -> SHA-256 одобренных версий файла.
type TrustDoc = store.Doc[map[string][]string]

// TasksDoc хранит мои скрипты.
type TasksDoc = store.Doc[[]tasks.Task]

func (s *Server) trusted(repoID, hash string) bool {
	if hash == "" {
		return false
	}
	for _, h := range s.trust.Get()[repoID] {
		if h == hash {
			return true
		}
	}
	return false
}

// visibleMine возвращает мои скрипты, видимые в репозитории (общие и его собственные).
func (s *Server) visibleMine(repoID string) []tasks.Task {
	out := []tasks.Task{}
	for _, t := range s.tasks.Get() {
		if t.Global() || t.Scope == repoID {
			out = append(out, t)
		}
	}
	return out
}

// handleTasks: GET /api/tasks?repo=&wt= — задачи для вкладки из трёх источников.
func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
	if r.Method != http.MethodGet {
		return errMethod
	}
	q := r.URL.Query()
	resp := map[string]any{"mine": s.visibleMine(q.Get("repo")), "auto": []tasks.Task{}}
	if q.Get("repo") != "" {
		repo, dir, err := s.workdir(r)
		if err != nil {
			return err
		}
		rt := tasks.LoadRepoFile(dir)
		resp["repo"] = map[string]any{
			"tasks": rt.Tasks, "hash": rt.Hash, "error": rt.Error, "trusted": s.trusted(repo.ID, rt.Hash),
		}
		resp["auto"] = tasks.Detect(dir)
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

func newTaskID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "my:" + hex.EncodeToString(b)
}

// handleMyTasks: GET все мои скрипты, PUT заменяет список целиком (редактор скриптов).
func (s *Server) handleMyTasks(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.tasks.Get())
		return nil
	case http.MethodPut:
		var list []tasks.Task
		if err := decodeJSON(w, r, &list); err != nil {
			return err
		}
		if len(list) > maxMyTasks {
			return errBad("too many tasks (max %d)", maxMyTasks)
		}
		seen := map[string]bool{}
		for i := range list {
			t := &list[i]
			if err := t.Normalize(); err != nil {
				return errBad("%s: %v", t.Name, err)
			}
			if !strings.HasPrefix(t.ID, "my:") || seen[t.ID] {
				t.ID = newTaskID()
			}
			seen[t.ID] = true
			if !t.Global() {
				if _, ok := s.repos.Get(t.Scope); !ok {
					return errBad("%s: unknown repository in scope", t.Name)
				}
			} else {
				t.Scope = ""
			}
			if t.InWorktree() {
				t.Cwd = ""
				continue
			}
			dir, err := s.sandbox.Resolve(t.Cwd)
			if err != nil {
				return err
			}
			if err := requireDir(dir); err != nil {
				return err
			}
			t.Cwd = dir
		}
		if list == nil {
			list = []tasks.Task{}
		}
		if err := s.tasks.Update(func(v *[]tasks.Task) error { *v = list; return nil }); err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, list)
		return nil
	}
	return errMethod
}

// handleTrust: POST {repo, wt, hash} — доверять текущей версии .gitaway.json. Хеш должен
// совпасть с файлом на диске: пользователь одобряет именно то содержимое, которое видел.
func (s *Server) handleTrust(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
	if r.Method != http.MethodPost {
		return errMethod
	}
	var req struct {
		Repo string `json:"repo"`
		WT   string `json:"wt"`
		Hash string `json:"hash"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	repo, ok := s.repos.Get(req.Repo)
	if !ok {
		return errNotFound("repository")
	}
	dir, err := s.workdirFor(r.Context(), repo, req.WT)
	if err != nil {
		return err
	}
	rt := tasks.LoadRepoFile(dir)
	if rt.Hash == "" || rt.Hash != req.Hash {
		return &apiError{http.StatusConflict, "changed", "the task file changed; review it again"}
	}
	err = s.trust.Update(func(v *map[string][]string) error {
		if *v == nil {
			*v = map[string][]string{}
		}
		list := append((*v)[repo.ID], rt.Hash)
		if len(list) > maxTrustPerRe {
			list = list[len(list)-maxTrustPerRe:]
		}
		(*v)[repo.ID] = list
		return nil
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]bool{"trusted": true})
	return nil
}

// handleRunTask: POST {repo, wt, id} — запустить задачу как сеанс. Задачи из .gitaway.json
// запускаются только после доверия к текущей версии файла.
func (s *Server) handleRunTask(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
	if r.Method != http.MethodPost {
		return errMethod
	}
	var req struct {
		Repo string `json:"repo"`
		WT   string `json:"wt"`
		ID   string `json:"id"`
		Cols uint16 `json:"cols"`
		Rows uint16 `json:"rows"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	var repoID, repoName, wtDir string
	if req.Repo != "" {
		repo, ok := s.repos.Get(req.Repo)
		if !ok {
			return errNotFound("repository")
		}
		dir, err := s.workdirFor(r.Context(), repo, req.WT)
		if err != nil {
			return err
		}
		repoID, repoName, wtDir = repo.ID, filepath.Base(repo.Path), dir
	}
	task, err := s.findTask(repoID, wtDir, req.ID)
	if err != nil {
		return err
	}
	dir := wtDir
	if !task.InWorktree() {
		if dir, err = s.sandbox.Resolve(task.Cwd); err != nil {
			return err
		}
		if err := requireDir(dir); err != nil {
			return err
		}
	} else if dir == "" {
		return errBad("this task runs in a branch folder: open a repository first")
	}
	branch := ""
	if wtDir != "" {
		branch = s.currentBranch(r.Context(), wtDir)
	}
	script := tasks.Script(task, tasks.Vars{Worktree: wtDir, Repo: repoName, Branch: branch, Now: time.Now()})
	sess, err := s.terms.Start(session.Options{
		Kind: "task", Title: task.Name, Dir: dir, Repo: repoID, WT: req.WT,
		Args: append(session.Shell(), "-c", script), Cols: req.Cols, Rows: req.Rows, Timeout: task.Duration(),
	})
	if err != nil {
		return err
	}
	s.log.Info("task started", "task", task.Name, "id", sess.Info().ID, "dir", dir)
	writeJSON(w, http.StatusCreated, sess.Info())
	return nil
}

// findTask ищет задачу по id: "auto:" автоопределение, "repo:" файл репозитория
// (только с доверием), иначе мои скрипты (видимые в этом репозитории).
func (s *Server) findTask(repoID, wtDir, id string) (tasks.Task, error) {
	switch {
	case strings.HasPrefix(id, "auto:") && wtDir != "":
		for _, t := range tasks.Detect(wtDir) {
			if t.ID == id {
				return t, nil
			}
		}
	case strings.HasPrefix(id, "repo:") && wtDir != "":
		rt := tasks.LoadRepoFile(wtDir)
		if !s.trusted(repoID, rt.Hash) {
			return tasks.Task{}, &apiError{http.StatusForbidden, "not_trusted", "tasks from " + tasks.RepoFile + " need your approval first"}
		}
		for _, t := range rt.Tasks {
			if t.ID == id {
				return t, nil
			}
		}
	default:
		for _, t := range s.visibleMine(repoID) {
			if t.ID == id {
				return t, nil
			}
		}
	}
	return tasks.Task{}, errNotFound("task")
}

func (s *Server) currentBranch(ctx context.Context, dir string) string {
	st, err := s.git.Status(ctx, dir)
	if err != nil {
		return ""
	}
	return st.Branch
}
