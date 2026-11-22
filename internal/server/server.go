// Package server содержит HTTP-сервер: маршруты, middleware и обработчики API.
package server

import (
	"bufio"
	"crypto/subtle"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/Maxi-Boy/gitaway/internal/auth"
	"github.com/Maxi-Boy/gitaway/internal/config"
	"github.com/Maxi-Boy/gitaway/internal/fsx"
	"github.com/Maxi-Boy/gitaway/internal/gitx"
	"github.com/Maxi-Boy/gitaway/internal/jobs"
	"github.com/Maxi-Boy/gitaway/internal/store"
)

// Deps это зависимости сервера.
type Deps struct {
	Cfg     config.Config
	Auth    auth.Authenticator
	Sandbox *fsx.Sandbox
	Repos   *store.Repos
	Git     *gitx.Runner
	Log     *slog.Logger
	Web     fs.FS // статический интерфейс для "/" (может быть nil)
}

// Server обслуживает API и статические файлы.
type Server struct {
	cfg      config.Config
	auth     auth.Authenticator
	sandbox  *fsx.Sandbox
	repos    *store.Repos
	git      *gitx.Runner
	log      *slog.Logger
	web      fs.FS
	sessions *auth.Store
	limiter  *auth.Limiter
	locks    *jobs.KeyedLock
	commits  *gitx.CommitCache
}

func New(d Deps) *Server {
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	return &Server{
		cfg:      d.Cfg,
		auth:     d.Auth,
		sandbox:  d.Sandbox,
		repos:    d.Repos,
		git:      d.Git,
		log:      log,
		web:      d.Web,
		sessions: auth.NewStore(d.Cfg.SessionTTL.Duration),
		limiter:  auth.NewLimiter(5, 5*time.Minute),
		locks:    &jobs.KeyedLock{},
		commits:  gitx.NewCommitCache(),
	}
}

// Handler собирает маршруты и middleware.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/api/login", s.handleLogin)
	mux.HandleFunc("/api/logout", s.api(s.handleLogout))
	mux.HandleFunc("/api/me", s.api(s.handleMe))
	mux.HandleFunc("/api/repos", s.api(s.handleRepos))
	mux.HandleFunc("/api/branches", s.api(s.handleBranches))
	mux.HandleFunc("/api/status", s.api(s.handleStatus))
	mux.HandleFunc("/api/worktrees", s.api(s.handleWorktrees))
	mux.HandleFunc("/api/tree", s.api(s.handleTree))
	mux.HandleFunc("/api/commits", s.api(s.handleCommits))
	mux.HandleFunc("/api/file", s.api(s.handleFile))
	mux.HandleFunc("/api/copy/plan", s.api(s.handleCopyPlan))
	mux.HandleFunc("/api/copy", s.api(s.handleCopy))
	s.registerGit(mux)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "unknown API endpoint")
	})
	if s.web != nil {
		mux.Handle("/", http.FileServer(http.FS(s.web)))
	}
	return s.withHeaders(s.withLog(mux))
}

// apiHandler это обработчик, которому уже известна сессия; ошибка превращается в JSON-ответ.
type apiHandler func(w http.ResponseWriter, r *http.Request, sess auth.Session) error

// api требует вход, а для изменяющих запросов ещё и проверяет Origin и CSRF-токен.
func (s *Server) api(h apiHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, ok := s.sessionFrom(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "login required")
			return
		}
		if !safeMethod(r.Method) {
			if !sameOrigin(r) {
				writeError(w, http.StatusForbidden, "bad_origin", "cross-origin request rejected")
				return
			}
			got := []byte(r.Header.Get("X-CSRF-Token"))
			if subtle.ConstantTimeCompare(got, []byte(sess.CSRF)) != 1 {
				writeError(w, http.StatusForbidden, "bad_csrf", "missing or invalid CSRF token")
				return
			}
		}
		if err := h(w, r, sess); err != nil {
			s.fail(w, err)
		}
	}
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// sameOrigin отклоняет запросы, у которых Origin не совпадает с Host.
// Запросы без Origin (curl, тесты) проходят, но всё равно требуют CSRF-токен.
func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	return err == nil && u.Host == r.Host
}

func (s *Server) sessionFrom(r *http.Request) (auth.Session, bool) {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return auth.Session{}, false
	}
	return s.sessions.Get(c.Value)
}

func (s *Server) isHTTPS(r *http.Request) bool {
	return r.TLS != nil || (s.cfg.TrustProxy && r.Header.Get("X-Forwarded-Proto") == "https")
}

func (s *Server) clientIP(r *http.Request) string {
	if s.cfg.TrustProxy {
		if ip := r.Header.Get("X-Real-IP"); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) withHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy",
			"default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

// statusWriter запоминает статус ответа для лога и не мешает WebSocket (Hijack) и потоковому выводу (Flush).
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("hijacking is not supported")
	}
	return h.Hijack()
}

func (s *Server) withLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		// Query-строка в лог не попадает намеренно.
		s.log.Info("request", "method", r.Method, "path", r.URL.Path,
			"status", sw.status, "ms", time.Since(start).Milliseconds())
	})
}
