package server

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Maxi-Boy/gitaway/internal/auth"
)

// maxListEntries ограничивает размер ответа для очень больших каталогов.
const maxListEntries = 10000

// listDir читает каталог: сначала каталоги, затем файлы, имена без учёта регистра.
// Каталог .git не показывается. Если элементов больше limit, возвращаются первые
// limit и true.
func listDir(full string, limit int) ([]treeEntry, bool, error) {
	items, err := os.ReadDir(full)
	if err != nil {
		return nil, false, err
	}
	entries := make([]treeEntry, 0, len(items))
	for _, it := range items {
		if it.Name() == ".git" {
			continue
		}
		info, err := it.Info()
		if err != nil {
			continue // файл исчез между чтением каталога и stat
		}
		entries = append(entries, treeEntry{
			Name:    it.Name(),
			IsDir:   it.IsDir(),
			Symlink: it.Type()&fs.ModeSymlink != 0,
			Size:    info.Size(),
			MTime:   info.ModTime().Unix(),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	if len(entries) > limit {
		return entries[:limit], true, nil
	}
	return entries, false, nil
}

// cleanRel приводит относительный путь к виду для ответа: "/" как разделитель, "" для корня.
func cleanRel(rel string) string {
	s := filepath.ToSlash(filepath.Clean(rel))
	if s == "." {
		return ""
	}
	return s
}

// serverDir возвращает реальный путь каталога серверной панели: dir из запроса,
// иначе папку по умолчанию из настроек, иначе первый разрешённый корень.
// Папка по умолчанию могла исчезнуть; тогда панель открывает корень, а не ошибку.
func (s *Server) serverDir(dir string) (string, error) {
	if dir != "" {
		return s.sandbox.Resolve(dir)
	}
	if def := s.settings.Get().DefaultFolder; def != "" {
		if base, err := s.sandbox.Resolve(def); err == nil {
			return base, nil
		}
	}
	return s.sandbox.Roots()[0], nil
}

// requireDir проверяет, что путь существует и это каталог.
func requireDir(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return errBad("path is not a directory")
	}
	return nil
}

// handleFS отдаёт содержимое папки на сервере: GET /api/fs?dir=<абсолютный путь>&path=<подкаталог>.
// Без dir открывается папка по умолчанию. В ответе dir это реальный путь (для хлебных крошек).
func (s *Server) handleFS(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
	if r.Method != http.MethodGet {
		return errMethod
	}
	q := r.URL.Query()
	base, err := s.serverDir(q.Get("dir"))
	if err != nil {
		return err
	}
	rel := q.Get("path")
	if hasGitSegment(base) || hasGitSegment(rel) {
		return errBad("browsing .git is not allowed")
	}
	full, err := s.sandbox.ResolveUnder(base, rel, true)
	if err != nil {
		return err
	}
	if err := requireDir(full); err != nil {
		return err
	}
	entries, truncated, err := listDir(full, maxListEntries)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"dir": base, "path": cleanRel(rel), "entries": entries, "truncated": truncated,
	})
	return nil
}

// validDirName проверяет имя новой папки: одно имя, без разделителей и служебных значений.
func validDirName(name string) error {
	switch {
	case name == "", name == ".", name == "..", name == ".git", len(name) > 255:
		return errBad("invalid folder name")
	case strings.ContainsAny(name, "/\\\x00"):
		return errBad("folder name must not contain slashes")
	}
	return nil
}

// handleMkdir создаёт папку: POST /api/fs/mkdir {"dir", "path", "name"}.
func (s *Server) handleMkdir(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
	if r.Method != http.MethodPost {
		return errMethod
	}
	var req struct {
		Dir  string `json:"dir"`
		Path string `json:"path"`
		Name string `json:"name"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	if err := validDirName(req.Name); err != nil {
		return err
	}
	base, err := s.serverDir(req.Dir)
	if err != nil {
		return err
	}
	if hasGitSegment(base) || hasGitSegment(req.Path) {
		return errBad("creating folders in .git is not allowed")
	}
	parent, err := s.sandbox.ResolveUnder(base, req.Path, true)
	if err != nil {
		return err
	}
	if err := requireDir(parent); err != nil {
		return err
	}
	full, err := s.sandbox.ResolveUnder(parent, req.Name, false)
	if err != nil {
		return err
	}
	if err := os.Mkdir(full, 0o755); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return &apiError{http.StatusConflict, "exists", "a file or folder with this name already exists"}
		}
		return err
	}
	rel, err := filepath.Rel(base, full)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, map[string]string{"dir": base, "path": cleanRel(rel)})
	return nil
}

// handleSettings: GET отдаёт настройки, PUT меняет их. Поля, которых нет в теле PUT,
// сохраняют текущие значения. Папка по умолчанию должна быть каталогом внутри корней.
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.settings.Get())
		return nil
	case http.MethodPut:
		next := s.settings.Get()
		if err := decodeJSON(w, r, &next); err != nil {
			return err
		}
		if next.DefaultFolder != "" {
			dir, err := s.sandbox.Resolve(next.DefaultFolder)
			if err != nil {
				return err
			}
			if hasGitSegment(dir) {
				return errBad("the default folder must not be inside .git")
			}
			if err := requireDir(dir); err != nil {
				return err
			}
			next.DefaultFolder = dir
		}
		if err := s.settings.Set(next); err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, next)
		return nil
	}
	return errMethod
}
