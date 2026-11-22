package server

import (
	"net/http"
	"os"

	"github.com/Maxi-Boy/gitaway/internal/auth"
)

const cookieName = "gitaway_sid"

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "bad_origin", "cross-origin request rejected")
		return
	}
	ip := s.clientIP(r)
	if !s.limiter.Allow(ip) {
		writeError(w, http.StatusTooManyRequests, "too_many_attempts", "too many failed attempts, try again later")
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		s.fail(w, err)
		return
	}
	user, err := s.auth.Authenticate(req.Username, req.Password)
	if err != nil {
		s.limiter.Fail(ip)
		s.log.Warn("login failed", "ip", ip) // пароль и токен в лог не пишем
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid credentials")
		return
	}
	s.limiter.Reset(ip)
	id, sess, err := s.sessions.Create(user)
	if err != nil {
		s.fail(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    id,
		Path:     "/",
		MaxAge:   int(s.cfg.SessionTTL.Duration.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   s.isHTTPS(r),
	})
	writeJSON(w, http.StatusOK, map[string]string{"user": sess.User, "csrf": sess.CSRF})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request, _ auth.Session) error {
	if r.Method != http.MethodPost {
		return errMethod
	}
	if c, err := r.Cookie(cookieName); err == nil {
		s.sessions.Delete(c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   s.isHTTPS(r),
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	return nil
}

// handleMe отдаёт данные текущей сессии; страница после перезагрузки берёт отсюда CSRF-токен.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, sess auth.Session) error {
	if r.Method != http.MethodGet {
		return errMethod
	}
	host, _ := os.Hostname()
	writeJSON(w, http.StatusOK, map[string]any{
		"host":           host,
		"user":           sess.User,
		"csrf":           sess.CSRF,
		"roots":          s.sandbox.Roots(),
		"default_folder": s.settings.Get().DefaultFolder,
	})
	return nil
}
