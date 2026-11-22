package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Maxi-Boy/gitaway/internal/auth"
	"github.com/Maxi-Boy/gitaway/internal/gitx"
	"github.com/Maxi-Boy/gitaway/internal/store"
)

// repoBase находит репозиторий по ?repo= и возвращает его реальный путь внутри корней.
func (s *Server) repoBase(r *http.Request) (store.Repo, string, error) {
	repo, ok := s.repos.Get(r.URL.Query().Get("repo"))
	if !ok {
		return store.Repo{}, "", errNotFound("repository")
	}
	base, err := s.sandbox.Resolve(repo.Path)
	return repo, base, err
}

// workdir возвращает рабочую папку: основную или worktree из ?wt= (только из списка git).
func (s *Server) workdir(r *http.Request) (store.Repo, string, error) {
	repo, ok := s.repos.Get(r.URL.Query().Get("repo"))
	if !ok {
		return store.Repo{}, "", errNotFound("repository")
	}
	dir, err := s.workdirFor(r.Context(), repo, r.URL.Query().Get("wt"))
	return repo, dir, err
}

func (s *Server) workdirFor(ctx context.Context, repo store.Repo, wt string) (string, error) {
	base, err := s.sandbox.Resolve(repo.Path)
	if err != nil {
		return "", err
	}
	if wt == "" || filepath.Clean(wt) == base {
		return base, nil
	}
	list, err := s.git.Worktrees(ctx, base)
	if err != nil {
		return "", err
	}
	for _, w := range list {
		if filepath.Clean(w.Path) == filepath.Clean(wt) {
			return s.sandbox.Resolve(w.Path)
		}
	}
	return "", errNotFound("worktree")
}

func (s *Server) handleBranches(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
	if r.Method != http.MethodGet {
		return errMethod
	}
	_, dir, err := s.workdir(r)
	if err != nil {
		return err
	}
	list, err := s.git.Branches(r.Context(), dir)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, list)
	return nil
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
	if r.Method != http.MethodGet {
		return errMethod
	}
	_, dir, err := s.workdir(r)
	if err != nil {
		return err
	}
	st, err := s.git.Status(r.Context(), dir)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, st)
	return nil
}

// handleWorktrees: GET список, POST создать (вкладка-ветка), DELETE удалить.
func (s *Server) handleWorktrees(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
	switch r.Method {
	case http.MethodGet:
		_, base, err := s.repoBase(r)
		if err != nil {
			return err
		}
		list, err := s.git.Worktrees(r.Context(), base)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, list)
		return nil

	case http.MethodPost:
		var req struct {
			Repo   string `json:"repo"`
			Branch string `json:"branch"`
			Create bool   `json:"create"`
			Start  string `json:"start"`
		}
		if err := decodeJSON(w, r, &req); err != nil {
			return err
		}
		repo, ok := s.repos.Get(req.Repo)
		if !ok {
			return errNotFound("repository")
		}
		if err := gitx.ValidBranchName(req.Branch); err != nil {
			return errBad("%v", err)
		}
		base, err := s.sandbox.Resolve(repo.Path)
		if err != nil {
			return err
		}
		unlock, ok := s.locks.TryLock(repo.ID)
		if !ok {
			return errBusy
		}
		defer unlock()
		// Папки worktree лежат рядом с репозиторием: <репозиторий>.worktrees/<ветка>.
		parent, err := s.sandbox.ResolveNew(base + ".worktrees")
		if err != nil {
			return err
		}
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return err
		}
		dest, err := freeDir(parent, req.Branch)
		if err != nil {
			return err
		}
		out, err := s.git.AddWorktree(r.Context(), base, gitx.AddWorktreeOptions{
			Path: dest, Branch: req.Branch, CreateBranch: req.Create, StartPoint: req.Start,
		})
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusCreated, map[string]string{"path": dest, "output": out})
		return nil

	case http.MethodDelete:
		repo, base, err := s.repoBase(r)
		if err != nil {
			return err
		}
		unlock, ok := s.locks.TryLock(repo.ID)
		if !ok {
			return errBusy
		}
		defer unlock()
		out, err := s.git.RemoveWorktree(r.Context(), base, r.URL.Query().Get("wt"), r.URL.Query().Get("force") == "1")
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, map[string]string{"output": out})
		return nil
	}
	return errMethod
}

// freeDir подбирает свободное имя каталога для worktree (feature-login, feature-login-2, ...).
func freeDir(parent, branch string) (string, error) {
	name := gitx.SafeDirName(branch)
	for i := 1; i <= 50; i++ {
		cand := name
		if i > 1 {
			cand = fmt.Sprintf("%s-%d", name, i)
		}
		p := filepath.Join(parent, cand)
		if _, err := os.Lstat(p); errors.Is(err, fs.ErrNotExist) {
			return p, nil
		}
	}
	return "", errBad("cannot find a free directory name for the worktree")
}

type treeEntry struct {
	Name    string `json:"name"`
	IsDir   bool   `json:"is_dir"`
	Symlink bool   `json:"symlink,omitempty"`
	Size    int64  `json:"size"`
	MTime   int64  `json:"mtime"`            // время изменения на диске (unix)
	Status  string `json:"status,omitempty"` // метка git: M, ??, A, * (внутри каталога есть изменения)
}

// handleTree отдаёт элементы каталога с датой на диске и пометками git-статуса.
// Коммиты подгружаются отдельным запросом /api/commits, чтобы таблица открывалась сразу.
func (s *Server) handleTree(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
	if r.Method != http.MethodGet {
		return errMethod
	}
	_, dir, err := s.workdir(r)
	if err != nil {
		return err
	}
	rel := r.URL.Query().Get("path")
	full, err := s.sandbox.ResolveUnder(dir, rel, true)
	if err != nil {
		return err
	}
	entries, truncated, err := listDir(full, maxListEntries)
	if err != nil {
		return err
	}
	relSlash := cleanRel(rel)
	marks := s.statusMarks(r.Context(), dir, relSlash)
	for i := range entries {
		entries[i].Status = marks[entries[i].Name]
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": relSlash, "entries": entries, "truncated": truncated})
	return nil
}

// statusMarks сопоставляет имена элементов каталога rel отметки git status.
func (s *Server) statusMarks(ctx context.Context, dir, rel string) map[string]string {
	marks := map[string]string{}
	st, err := s.git.Status(ctx, dir)
	if err != nil {
		return marks
	}
	prefix := ""
	if rel != "" {
		prefix = rel + "/"
	}
	for _, c := range st.Changes {
		if !strings.HasPrefix(c.Path, prefix) {
			continue
		}
		name, _, nested := strings.Cut(c.Path[len(prefix):], "/")
		if name == "" {
			continue
		}
		flag := strings.TrimSpace(c.X + c.Y)
		if nested && c.X != "?" {
			flag = "*"
		}
		if _, exists := marks[name]; !exists {
			marks[name] = flag
		}
	}
	return marks
}

// handleCommits отдаёт последний коммит для каждого отслеживаемого элемента каталога.
func (s *Server) handleCommits(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
	if r.Method != http.MethodGet {
		return errMethod
	}
	_, dir, err := s.workdir(r)
	if err != nil {
		return err
	}
	rel := r.URL.Query().Get("path")
	if _, err := s.sandbox.ResolveUnder(dir, rel, true); err != nil {
		return err
	}
	sub := filepath.ToSlash(filepath.Clean(rel))
	if sub == "." {
		sub = ""
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	found, err := s.commits.Get(ctx, s.git, dir, sub)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, found)
	return nil
}

// opRequest это тело запросов fetch, pull, push, switch и update-branch.
type opRequest struct {
	Repo           string `json:"repo"`
	WT             string `json:"wt"`
	Remote         string `json:"remote"`
	Branch         string `json:"branch"`
	SetUpstream    bool   `json:"set_upstream"`
	ForceWithLease bool   `json:"force_with_lease"`
}

type gitOp func(ctx context.Context, dir string, q opRequest) (string, error)

// opHandler выполняет операцию git под блокировкой репозитория: две команды в одном
// репозитории одновременно не запускаются, вторая получает 409.
func (s *Server) opHandler(op gitOp) apiHandler {
	return func(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
		if r.Method != http.MethodPost {
			return errMethod
		}
		var q opRequest
		if err := decodeJSON(w, r, &q); err != nil {
			return err
		}
		if q.Remote != "" {
			if err := gitx.ValidRemoteName(q.Remote); err != nil {
				return errBad("%v", err)
			}
		}
		if q.Branch != "" {
			if err := gitx.ValidBranchName(q.Branch); err != nil {
				return errBad("%v", err)
			}
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
		out, err := op(r.Context(), dir, q)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, map[string]string{"output": out})
		return nil
	}
}

func (s *Server) registerGit(mux *http.ServeMux) {
	reg := func(path string, op gitOp) {
		mux.HandleFunc(path, s.api(s.opHandler(op)))
	}
	reg("/api/git/fetch", func(ctx context.Context, dir string, q opRequest) (string, error) {
		return s.git.Fetch(ctx, dir, q.Remote)
	})
	reg("/api/git/pull", func(ctx context.Context, dir string, q opRequest) (string, error) {
		return s.git.Pull(ctx, dir)
	})
	reg("/api/git/push", func(ctx context.Context, dir string, q opRequest) (string, error) {
		return s.git.Push(ctx, dir, gitx.PushOptions{
			Remote: q.Remote, Branch: q.Branch, SetUpstream: q.SetUpstream, ForceWithLease: q.ForceWithLease,
		})
	})
	reg("/api/git/switch", func(ctx context.Context, dir string, q opRequest) (string, error) {
		return s.git.Switch(ctx, dir, q.Branch)
	})
	reg("/api/git/update-branch", func(ctx context.Context, dir string, q opRequest) (string, error) {
		return s.git.UpdateBranch(ctx, dir, q.Branch)
	})
}
