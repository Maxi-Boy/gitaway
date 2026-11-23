package server

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"

	"github.com/coder/websocket"

	"github.com/Maxi-Boy/gitaway/internal/auth"
	"github.com/Maxi-Boy/gitaway/internal/session"
)

// handleSessions: GET список сеансов, POST новый терминал, DELETE остановить сеанс.
// Терминал открывается в рабочей папке вкладки (repo, wt) или в папке сервера (dir).
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.terms.List())
		return nil
	case http.MethodDelete:
		if err := s.terms.Close(r.URL.Query().Get("id")); err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return nil
	case http.MethodPost:
		var req struct {
			Repo string `json:"repo"`
			WT   string `json:"wt"`
			Dir  string `json:"dir"`
			Cols uint16 `json:"cols"`
			Rows uint16 `json:"rows"`
		}
		if err := decodeJSON(w, r, &req); err != nil {
			return err
		}
		o := session.Options{Cols: req.Cols, Rows: req.Rows}
		if req.Repo != "" {
			repo, ok := s.repos.Get(req.Repo)
			if !ok {
				return errNotFound("repository")
			}
			dir, err := s.workdirFor(r.Context(), repo, req.WT)
			if err != nil {
				return err
			}
			o.Dir, o.Repo, o.WT = dir, repo.ID, req.WT
			o.Title = filepath.Base(dir)
		} else {
			dir, err := s.serverDir(req.Dir)
			if err != nil {
				return err
			}
			if err := requireDir(dir); err != nil {
				return err
			}
			o.Dir, o.Title = dir, filepath.Base(dir)
		}
		sess, err := s.terms.Start(o)
		if err != nil {
			return err
		}
		s.log.Info("session started", "id", sess.Info().ID, "dir", o.Dir)
		writeJSON(w, http.StatusCreated, sess.Info())
		return nil
	}
	return errMethod
}

// control это служебное сообщение WebSocket (текстовый кадр). Ввод и вывод терминала
// идут двоичными кадрами.
type control struct {
	Type string `json:"type"`           // resize от клиента; exit от сервера
	Cols uint16 `json:"cols,omitempty"` // resize
	Rows uint16 `json:"rows,omitempty"` // resize
	Code int    `json:"code"`           // exit: код выхода процесса
}

// handleSessionWS подключает браузер к сеансу: GET /api/sessions/ws?id=.
// Сначала уходит накопленный вывод, затем живой поток; закрытие вкладки не останавливает процесс.
func (s *Server) handleSessionWS(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
	// WebSocket не несёт CSRF-заголовок, поэтому Origin обязателен и должен совпадать с хостом
	// (защита от подключения со стороннего сайта с cookie пользователя).
	if r.Header.Get("Origin") == "" || !sameOrigin(r) {
		return &apiError{http.StatusForbidden, "bad_origin", "cross-origin request rejected"}
	}
	sess, err := s.terms.Get(r.URL.Query().Get("id"))
	if err != nil {
		return err
	}
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return nil // Accept уже ответил клиенту
	}
	c.SetReadLimit(64 << 10)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	snapshot, out, detach := sess.Attach()
	defer detach()

	// ввод и изменение размера от клиента
	go func() {
		defer cancel()
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageBinary {
				if sess.Write(data) != nil {
					return
				}
				continue
			}
			var m control
			if json.Unmarshal(data, &m) == nil && m.Type == "resize" {
				_ = sess.Resize(m.Cols, m.Rows)
			}
		}
	}()

	if len(snapshot) > 0 {
		if err := c.Write(ctx, websocket.MessageBinary, snapshot); err != nil {
			return nil
		}
	}
	for {
		select {
		case <-ctx.Done():
			c.Close(websocket.StatusNormalClosure, "")
			return nil
		case p, ok := <-out:
			if !ok {
				// Running сбрасывается до закрытия каналов (под тем же замком), а Done
				// закрывается чуть позже: смотрим на Running, иначе при выходе процесса
				// клиент изредка получал бы «slow client» вместо кода выхода.
				if !sess.Info().Running {
					<-sess.Done()
					msg, _ := json.Marshal(control{Type: "exit", Code: sess.Info().ExitCode})
					_ = c.Write(ctx, websocket.MessageText, msg)
					c.Close(websocket.StatusNormalClosure, "exited")
					return nil
				}
				// клиент не успевал читать: пусть переподключится и получит буфер заново
				c.Close(websocket.StatusTryAgainLater, "slow client")
				return nil
			}
			if err := c.Write(ctx, websocket.MessageBinary, p); err != nil {
				return nil
			}
		}
	}
}

// Close останавливает процессы сеансов (при остановке gitaway).
func (s *Server) Close() {
	s.terms.Shutdown()
}
