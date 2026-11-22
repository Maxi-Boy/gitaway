package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Maxi-Boy/gitaway/internal/auth"
	"github.com/Maxi-Boy/gitaway/internal/copyop"
	"github.com/Maxi-Boy/gitaway/internal/store"
)

// location описывает одну панель: рабочую папку репозитория (repo, wt) или обычную
// папку на сервере (dir), плюс необязательный подкаталог path внутри неё.
type location struct {
	Repo string `json:"repo"`
	WT   string `json:"wt"`
	Dir  string `json:"dir"`
	Path string `json:"path"`
}

// copyRequest это тело запросов /api/copy/plan и /api/copy. Items это имена выбранных
// элементов относительно каталога src. Decisions: путь в назначении -> replace, skip
// или keep_both. Policy: replace_all, replace_newer, skip_all или keep_both_all.
type copyRequest struct {
	Src       location          `json:"src"`
	Items     []string          `json:"items"`
	Dst       location          `json:"dst"`
	Move      bool              `json:"move"`
	Decisions map[string]string `json:"decisions"`
	Policy    string            `json:"policy"`
}

type resolvedLoc struct {
	base string      // реальный путь каталога панели
	root string      // корень рабочей папки git ("" для обычной папки)
	repo *store.Repo // nil для обычной папки
}

// hasGitSegment сообщает, что в пути есть компонент ".git": туда копировать нельзя
// (хуки и конфигурация git исполняют код).
func hasGitSegment(p string) bool {
	for _, part := range strings.Split(filepath.ToSlash(p), "/") {
		if part == ".git" {
			return true
		}
	}
	return false
}

func (s *Server) resolveLocation(ctx context.Context, loc location) (resolvedLoc, error) {
	var out resolvedLoc
	switch {
	case loc.Repo != "":
		repo, ok := s.repos.Get(loc.Repo)
		if !ok {
			return out, errNotFound("repository")
		}
		dir, err := s.workdirFor(ctx, repo, loc.WT)
		if err != nil {
			return out, err
		}
		out.repo, out.root, out.base = &repo, dir, dir
	case loc.Dir != "":
		base, err := s.sandbox.Resolve(loc.Dir)
		if err != nil {
			return out, err
		}
		out.base = base
	default:
		return out, errBad("a location needs either repo or dir")
	}
	if loc.Path != "" {
		if hasGitSegment(loc.Path) {
			return out, errBad("copying to or from .git is not allowed")
		}
		base, err := s.sandbox.ResolveUnder(out.base, loc.Path, true)
		if err != nil {
			return out, err
		}
		out.base = base
	}
	if hasGitSegment(out.base) {
		return out, errBad("copying to or from .git is not allowed")
	}
	fi, err := os.Stat(out.base)
	if err != nil {
		return out, err
	}
	if !fi.IsDir() {
		return out, errBad("location is not a directory")
	}
	return out, nil
}

func (s *Server) copyLocations(ctx context.Context, req copyRequest) (src, dst resolvedLoc, err error) {
	for _, it := range req.Items {
		if hasGitSegment(it) {
			return src, dst, errBad("copying to or from .git is not allowed")
		}
	}
	if src, err = s.resolveLocation(ctx, req.Src); err != nil {
		return src, dst, err
	}
	dst, err = s.resolveLocation(ctx, req.Dst)
	return src, dst, err
}

// handleCopyPlan возвращает план: что копируется, какие есть конфликты, что пропущено.
// Для файлов из git к конфликтам добавляется время последнего коммита.
func (s *Server) handleCopyPlan(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
	if r.Method != http.MethodPost {
		return errMethod
	}
	var req copyRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	src, dst, err := s.copyLocations(r.Context(), req)
	if err != nil {
		return err
	}
	plan, err := copyop.BuildPlan(s.sandbox, copyop.Request{
		SrcBase: src.base, Items: req.Items, DstDir: dst.base, Move: req.Move,
	})
	if err != nil {
		return err
	}
	s.fillCommitTimes(r.Context(), src, plan)
	writeJSON(w, http.StatusOK, plan)
	return nil
}

// handleCopy выполняет копирование или перемещение с решениями пользователя.
// Если по какому-то конфликту решения нет, вернётся 409 unresolved_conflicts без изменений.
func (s *Server) handleCopy(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
	if r.Method != http.MethodPost {
		return errMethod
	}
	var req copyRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	src, dst, err := s.copyLocations(r.Context(), req)
	if err != nil {
		return err
	}
	unlock, ok := s.lockCopy(src, dst)
	if !ok {
		return errBusy
	}
	defer unlock()

	decisions := make(map[string]copyop.Action, len(req.Decisions))
	for rel, a := range req.Decisions {
		decisions[rel] = copyop.Action(a)
	}
	res, err := copyop.Execute(r.Context(), s.sandbox, copyop.ExecRequest{
		Request: copyop.Request{
			SrcBase: src.base, Items: req.Items, DstDir: dst.base, Move: req.Move,
		},
		Decisions: decisions,
		Policy:    copyop.Policy(req.Policy),
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, res)
	return nil
}

// lockCopy занимает каталог назначения и репозитории обеих панелей, чтобы две операции
// не писали в одно место одновременно и не пересекались с командами git.
func (s *Server) lockCopy(src, dst resolvedLoc) (func(), bool) {
	keys := []string{"dir:" + dst.base}
	if src.repo != nil {
		keys = append(keys, src.repo.ID)
	}
	if dst.repo != nil && (src.repo == nil || dst.repo.ID != src.repo.ID) {
		keys = append(keys, dst.repo.ID)
	}
	var unlocks []func()
	release := func() {
		for _, u := range unlocks {
			u()
		}
	}
	for _, k := range keys {
		u, ok := s.locks.TryLock(k)
		if !ok {
			release()
			return nil, false
		}
		unlocks = append(unlocks, u)
	}
	return release, true
}

// fillCommitTimes добавляет к конфликтам время последнего коммита файла в источнике.
// Это вспомогательные данные для диалога, поэтому ошибки молча игнорируются.
func (s *Server) fillCommitTimes(ctx context.Context, src resolvedLoc, plan *copyop.Plan) {
	if src.repo == nil || len(plan.Conflicts) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	for i := range plan.Conflicts {
		c := &plan.Conflicts[i]
		rel, err := filepath.Rel(src.root, filepath.Join(src.base, filepath.FromSlash(c.SrcRel)))
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		sub := filepath.ToSlash(filepath.Dir(rel))
		if sub == "." {
			sub = ""
		}
		found, err := s.commits.Get(ctx, s.git, src.root, sub)
		if err != nil {
			continue
		}
		if ci, ok := found[filepath.Base(rel)]; ok {
			c.SrcCommitTime = ci.Time
		}
	}
}
