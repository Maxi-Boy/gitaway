package server

import (
	"bytes"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"unicode/utf8"

	"github.com/Maxi-Boy/gitaway/internal/auth"
)

const maxFileSize = 5 << 20 // 5 МБ: больше файлы редактор не открывает

// fileVersion это метка состояния файла: время изменения в наносекундах и размер.
// Строка, а не число: в JavaScript целые больше 2^53 теряют точность.
// Размер помогает на файловых системах с грубым временем (секунды и хуже).
func fileVersion(info fs.FileInfo) string {
	return strconv.FormatInt(info.ModTime().UnixNano(), 10) + "-" + strconv.FormatInt(info.Size(), 10)
}

// handleFile: GET читает текстовый файл, PUT сохраняет его атомарно.
// В PUT поле version это метка, которую клиент получил при открытии: если файл
// на диске успел измениться (правка в терминале, git pull), вернётся 409.
// Пустая version означает запись без проверки (новый файл или «сохранить всё равно»).
func (s *Server) handleFile(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
	_, dir, err := s.workdir(r)
	if err != nil {
		return err
	}
	rel := r.URL.Query().Get("path")
	if rel == "" {
		return errBad("path is required")
	}

	switch r.Method {
	case http.MethodGet:
		full, err := s.sandbox.ResolveUnder(dir, rel, true)
		if err != nil {
			return err
		}
		info, err := os.Stat(full)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return errBad("path is a directory")
		}
		if info.Size() > maxFileSize {
			return &apiError{http.StatusRequestEntityTooLarge, "too_large", "file is larger than 5 MB"}
		}
		data, err := os.ReadFile(full)
		if err != nil {
			return err
		}
		head := data[:min(len(data), 8000)]
		if bytes.IndexByte(head, 0) >= 0 {
			return &apiError{http.StatusUnsupportedMediaType, "binary_file", "binary files cannot be edited"}
		}
		if !utf8.Valid(data) {
			return &apiError{http.StatusUnsupportedMediaType, "unsupported_encoding", "only UTF-8 files can be edited"}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"path": rel, "content": string(data), "size": info.Size(),
			"mtime": info.ModTime().Unix(), "version": fileVersion(info),
		})
		return nil

	case http.MethodPut:
		var req struct {
			Content string `json:"content"`
			Version string `json:"version"`
		}
		if err := decodeJSON(w, r, &req); err != nil {
			return err
		}
		if len(req.Content) > maxFileSize {
			return &apiError{http.StatusRequestEntityTooLarge, "too_large", "content is larger than 5 MB"}
		}
		full, err := s.sandbox.ResolveUnder(dir, rel, false)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if info, err := os.Stat(full); err == nil {
			if info.IsDir() {
				return errBad("path is a directory")
			}
			mode = info.Mode().Perm()
			if req.Version != "" && fileVersion(info) != req.Version {
				return &apiError{http.StatusConflict, "changed_on_disk", "file changed on disk since it was opened"}
			}
		}
		if err := writeFileAtomic(full, []byte(req.Content), mode); err != nil {
			return err
		}
		info, err := os.Stat(full)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"size": info.Size(), "mtime": info.ModTime().Unix(), "version": fileVersion(info),
		})
		return nil
	}
	return errMethod
}

// writeFileAtomic пишет во временный файл рядом и переименовывает его, чтобы сбой
// посреди записи не оставил наполовину записанный файл.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".gitaway-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // после успешного Rename файла уже нет, ошибка игнорируется
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
