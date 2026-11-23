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
	"net/netip"
	"net/url"
	"time"

	"github.com/Maxi-Boy/gitaway/internal/auth"
	"github.com/Maxi-Boy/gitaway/internal/config"
	"github.com/Maxi-Boy/gitaway/internal/fsx"
	"github.com/Maxi-Boy/gitaway/internal/github"
	"github.com/Maxi-Boy/gitaway/internal/gitx"
	"github.com/Maxi-Boy/gitaway/internal/jobs"
	"github.com/Maxi-Boy/gitaway/internal/session"
	"github.com/Maxi-Boy/gitaway/internal/store"
	"github.com/Maxi-Boy/gitaway/internal/tasks"
)

// Deps это зависимости сервера.
type Deps struct {
	Cfg      config.Config
	Auth     auth.Authenticator
	Sandbox  *fsx.Sandbox
	Repos    *store.Repos
	Settings *store.SettingsStore
	Git      *gitx.Runner
	Log      *slog.Logger
	Web      fs.FS            // статический интерфейс для "/" (может быть nil)
	Terms    *session.Manager // терминалы и другие процессы в pty (nil: новый менеджер)
	Tasks    *TasksDoc        // мои скрипты (nil: только в памяти)
	Trust    *TrustDoc        // доверие к .gitaway.json (nil: только в памяти)
	Keys     *KeysDoc         // deploy-ключи GitHub (nil: только в памяти)
}

// Server обслуживает API и статические файлы.
type Server struct {
	cfg      config.Config
	auth     auth.Authenticator
	sandbox  *fsx.Sandbox
	repos    *store.Repos
	settings *store.SettingsStore
	git      *gitx.Runner
	log      *slog.Logger
	web      fs.FS
	sessions *auth.Store
	limiter  *auth.Limiter
	locks    *jobs.KeyedLock
	commits  *gitx.CommitCache
	allow    []netip.Prefix
	allowAll bool
	terms    *session.Manager
	tasks    *TasksDoc
	trust    *TrustDoc
	keys     *KeysDoc
	ghURL    func(ref github.Ref, transport string) string // для тестов: адрес вместо GitHub
}

func New(d Deps) *Server {
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	allow, err := config.ParseCIDRs(d.Cfg.AllowCIDRs)
	if err != nil {
		// Normalize уже проверил список; если всё же ошибка, пускаем только loopback
		log.Error("allow_cidrs", "err", err)
		allow = nil
	}
	terms := d.Terms
	if terms == nil {
		terms = session.NewManager()
	}
	tasksDoc, trustDoc := d.Tasks, d.Trust
	if tasksDoc == nil {
		tasksDoc, _ = store.OpenDoc("", []tasks.Task{})
	}
	if trustDoc == nil {
		trustDoc, _ = store.OpenDoc("", map[string][]string{})
	}
	keysDoc := d.Keys
	if keysDoc == nil {
		keysDoc, _ = store.OpenDoc("", []github.Key{})
	}
	return &Server{
		cfg:      d.Cfg,
		auth:     d.Auth,
		sandbox:  d.Sandbox,
		repos:    d.Repos,
		settings: d.Settings,
		git:      d.Git,
		log:      log,
		web:      d.Web,
		sessions: auth.NewStore(d.Cfg.SessionTTL.Duration),
		limiter:  auth.NewLimiter(5, 5*time.Minute),
		locks:    &jobs.KeyedLock{},
		commits:  gitx.NewCommitCache(),
		allow:    allow,
		allowAll: len(d.Cfg.AllowCIDRs) == 0,
		terms:    terms,
		tasks:    tasksDoc,
		trust:    trustDoc,
		keys:     keysDoc,
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
	mux.HandleFunc("/api/fs", s.api(s.handleFS))
	mux.HandleFunc("/api/fs/mkdir", s.api(s.handleMkdir))
	mux.HandleFunc("/api/settings", s.api(s.handleSettings))
	mux.HandleFunc("/api/diff", s.api(s.handleDiff))
	mux.HandleFunc("/api/sessions", s.api(s.handleSessions))
	mux.HandleFunc("/api/sessions/ws", s.api(s.handleSessionWS))
	mux.HandleFunc("/api/tasks", s.api(s.handleTasks))
	mux.HandleFunc("/api/tasks/mine", s.api(s.handleMyTasks))
	mux.HandleFunc("/api/tasks/trust", s.api(s.handleTrust))
	mux.HandleFunc("/api/tasks/run", s.api(s.handleRunTask))
	mux.HandleFunc("/api/github/key", s.api(s.handleGitHubKey))
	mux.HandleFunc("/api/github/check", s.api(s.handleGitHubCheck))
	mux.HandleFunc("/api/github/clone", s.api(s.handleGitHubClone))
	mux.HandleFunc("/api/github/attach", s.api(s.handleGitHubAttach))
	mux.HandleFunc("/api/files", s.api(s.handleFiles))
	mux.HandleFunc("/api/search", s.api(s.handleSearch))
	mux.HandleFunc("/api/log", s.api(s.handleLog))
	mux.HandleFunc("/api/commit", s.api(s.handleCommit))
	mux.HandleFunc("/api/commit/diff", s.api(s.handleCommitDiff))
	s.registerGit(mux)
	s.registerChanges(mux)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "unknown API endpoint")
	})
	if s.web != nil {
		mux.Handle("/", newStatic(s.web))
	}
	return s.withHeaders(s.withLog(s.withAllow(mux)))
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

// clientIP возвращает адрес клиента. X-Real-IP учитывается, только если включён
// trust_proxy и запрос пришёл с loopback (от nginx на той же машине): иначе
// заголовок мог бы подделать кто угодно.
func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if s.cfg.TrustProxy && isLoopback(host) {
		if ip := r.Header.Get("X-Real-IP"); ip != "" {
			return ip
		}
	}
	return host
}

func isLoopback(ip string) bool {
	a, err := netip.ParseAddr(ip)
	return err == nil && a.Unmap().IsLoopback()
}

// allowed проверяет адрес клиента по allow_cidrs. Loopback разрешён всегда:
// с той же машины и так есть доступ к файлам.
func (s *Server) allowed(ip string) bool {
	if s.allowAll {
		return true
	}
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	a = a.Unmap() // ::ffff:10.0.0.1 это 10.0.0.1
	if a.IsLoopback() {
		return true
	}
	for _, p := range s.allow {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// withAllow отклоняет запросы с адресов вне allow_cidrs до любой другой обработки,
// включая страницу входа.
func (s *Server) withAllow(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ip := s.clientIP(r); !s.allowed(ip) {
			s.log.Warn("address not allowed", "ip", ip)
			writeError(w, http.StatusForbidden, "address_not_allowed", "access from this address is not allowed")
			return
		}
		next.ServeHTTP(w, r)
	})
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
