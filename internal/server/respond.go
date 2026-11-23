package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"

	"github.com/Maxi-Boy/gitaway/internal/copyop"
	"github.com/Maxi-Boy/gitaway/internal/fsx"
	"github.com/Maxi-Boy/gitaway/internal/gitx"
	"github.com/Maxi-Boy/gitaway/internal/session"
	"github.com/Maxi-Boy/gitaway/internal/tasks"
)

const maxBody = 8 << 20 // 8 МБ: лимит тела запроса

// apiError это ошибка с готовым HTTP-статусом и кодом для клиента.
type apiError struct {
	Status int
	Code   string
	Msg    string
}

func (e *apiError) Error() string { return e.Msg }

func errBad(format string, a ...any) error {
	return &apiError{http.StatusBadRequest, "bad_request", fmt.Sprintf(format, a...)}
}

func errNotFound(what string) error {
	return &apiError{http.StatusNotFound, "not_found", what + " not found"}
}

var (
	errBusy   error = &apiError{http.StatusConflict, "busy", "another operation is running in this repository"}
	errMethod error = &apiError{http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed"}
)

// fail превращает ошибку в JSON-ответ единого формата.
func (s *Server) fail(w http.ResponseWriter, err error) {
	var (
		ae *apiError
		ie *gitx.InputError
		ge *gitx.Error
		ue *copyop.UnresolvedError
	)
	switch {
	case errors.As(err, &ae):
		writeError(w, ae.Status, ae.Code, ae.Msg)
	case errors.As(err, &ie):
		writeError(w, http.StatusBadRequest, "bad_request", ie.Msg)
	case errors.As(err, &ue):
		writeJSON(w, http.StatusConflict, map[string]any{"error": map[string]any{
			"code": "unresolved_conflicts", "message": ue.Error(), "rels": ue.Rels,
		}})
	case errors.Is(err, copyop.ErrIntoItself):
		writeError(w, http.StatusBadRequest, "into_itself", "cannot copy a directory into itself")
	case errors.Is(err, copyop.ErrInvalid):
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
	case errors.Is(err, tasks.ErrInvalid):
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
	case errors.Is(err, gitx.ErrNotMerged):
		writeError(w, http.StatusConflict, "not_merged", err.Error())
	case errors.Is(err, session.ErrLimit):
		writeError(w, http.StatusTooManyRequests, "too_many_sessions", "too many terminals are open; close one first")
	case errors.Is(err, session.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "session not found")
	case errors.Is(err, context.Canceled):
		// клиент оборвал запрос: отвечать некому
	case errors.Is(err, fsx.ErrOutside):
		writeError(w, http.StatusForbidden, "outside_roots", "path is outside the allowed area")
	case errors.Is(err, fsx.ErrNotAbsolute), errors.Is(err, fsx.ErrNotRelative):
		writeError(w, http.StatusBadRequest, "bad_path", err.Error())
	case errors.Is(err, gitx.ErrUnknownWorktree), errors.Is(err, fs.ErrNotExist):
		writeError(w, http.StatusNotFound, "not_found", "not found")
	case errors.As(err, &ge) && gitx.NeedsLogin(err):
		writeError(w, http.StatusUnprocessableEntity, "auth_required", ge.Error())
	case errors.As(err, &ge):
		writeError(w, http.StatusUnprocessableEntity, "git_failed", ge.Error())
	default:
		s.log.Error("internal error", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "internal error")
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError пишет ответ вида {"error":{"code":"...","message":"..."}}.
func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": msg}})
}

// decodeJSON читает тело запроса строго: лимит размера, неизвестные поля запрещены.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return errBad("invalid JSON: %v", err)
	}
	return nil
}
