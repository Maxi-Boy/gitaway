// Package session запускает процессы в pty и держит их вывод: терминал сейчас,
// задачи и долгие операции git позже. Процесс живёт на сервере независимо от браузера;
// при подключении клиент получает накопленный вывод, затем живой поток.
package session

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// Ограничения по умолчанию: память и число процессов не растут без предела.
const (
	DefaultBuffer  = 2 << 20          // вывод одного сеанса в памяти
	DefaultMax     = 16               // одновременно существующих сеансов
	DefaultKeep    = 30 * time.Minute // сколько хранить завершённый сеанс
	subscriberSize = 256              // сообщений в очереди одного подключения
)

// ErrLimit возвращается, когда сеансов уже максимум.
var ErrLimit = errors.New("too many sessions")

// ErrNotFound возвращается для неизвестного id.
var ErrNotFound = errors.New("session not found")

// Info это описание сеанса для списка в интерфейсе.
type Info struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"` // shell или task
	Title    string    `json:"title"`
	Dir      string    `json:"dir"`
	Repo     string    `json:"repo,omitempty"`
	WT       string    `json:"wt,omitempty"`
	Started  time.Time `json:"started"`
	Running  bool      `json:"running"`
	ExitCode int       `json:"exit_code"`
	TimedOut bool      `json:"timed_out,omitempty"`
}

// Options описывает новый сеанс.
type Options struct {
	Title string
	Dir   string
	Repo  string
	WT    string
	Args  []string // команда; пусто: оболочка входа пользователя
	Cols  uint16
	Rows  uint16

	Kind    string        // shell (по умолчанию), task или clone
	Timeout time.Duration // 0: без ограничения; по истечении процесс останавливается
	Env     []string      // дополнительные переменные окружения процесса
}

// Manager хранит сеансы этого процесса gitaway.
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*Session
	Max      int
	Buffer   int
	Keep     time.Duration
	Env      []string // окружение процессов; nil: окружение gitaway
}

// NewManager создаёт менеджер с ограничениями по умолчанию.
func NewManager() *Manager {
	return &Manager{sessions: map[string]*Session{}, Max: DefaultMax, Buffer: DefaultBuffer, Keep: DefaultKeep}
}

// Session это один процесс в pty.
type Session struct {
	info Info
	cmd  *exec.Cmd
	pty  *os.File

	mu     sync.Mutex
	timer  *time.Timer
	buf    []byte
	limit  int
	subs   map[chan []byte]struct{}
	done   chan struct{}
	ended  time.Time
	closed bool
}

// Shell возвращает оболочку входа: $SHELL или /bin/bash, с ключом -l, чтобы PATH
// и переменные были как при входе по SSH (у systemd-сервиса окружение почти пустое).
func Shell() []string {
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "/bin/bash"
	}
	return []string{sh, "-l"}
}

func newID() string {
	b := make([]byte, 9)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Start запускает процесс. Вызывающий проверяет Dir через Sandbox.
func (m *Manager) Start(o Options) (*Session, error) {
	m.mu.Lock()
	m.cleanupLocked(time.Now())
	if len(m.sessions) >= m.Max {
		m.mu.Unlock()
		return nil, ErrLimit
	}
	m.mu.Unlock()

	args := o.Args
	if len(args) == 0 {
		args = Shell()
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = o.Dir
	env := m.Env
	if env == nil {
		env = os.Environ()
	}
	cmd.Env = append(append([]string{}, env...), "TERM=xterm-256color", "COLORTERM=truecolor")
	cmd.Env = append(cmd.Env, o.Env...)
	size := &pty.Winsize{Cols: o.Cols, Rows: o.Rows}
	if size.Cols == 0 || size.Rows == 0 {
		size = &pty.Winsize{Cols: 120, Rows: 32}
	}
	f, err := pty.StartWithSize(cmd, size) // новая группа процессов и управляющий терминал
	if err != nil {
		return nil, err
	}
	kind := o.Kind
	if kind == "" {
		kind = "shell"
	}
	s := &Session{
		info:  Info{ID: newID(), Kind: kind, Title: o.Title, Dir: o.Dir, Repo: o.Repo, WT: o.WT, Started: time.Now(), Running: true},
		cmd:   cmd,
		pty:   f,
		limit: m.Buffer,
		subs:  map[chan []byte]struct{}{},
		done:  make(chan struct{}),
	}
	if s.limit <= 0 {
		s.limit = DefaultBuffer
	}
	m.mu.Lock()
	m.sessions[s.info.ID] = s
	m.mu.Unlock()
	if o.Timeout > 0 {
		s.mu.Lock()
		s.timer = time.AfterFunc(o.Timeout, func() {
			s.mu.Lock()
			s.info.TimedOut = true
			s.mu.Unlock()
			s.publish([]byte("\r\n\x1b[31m[остановлено по таймауту " + o.Timeout.String() + "]\x1b[0m\r\n"))
			s.stop(5 * time.Second)
		})
		s.mu.Unlock()
	}
	go s.readLoop()
	return s, nil
}

func (s *Session) readLoop() {
	buf := make([]byte, 32<<10)
	for {
		n, err := s.pty.Read(buf)
		if n > 0 {
			s.publish(buf[:n])
		}
		if err != nil {
			break // EIO после выхода процесса: это нормальный конец
		}
	}
	err := s.cmd.Wait()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		code = -1
	}
	_ = s.pty.Close()
	s.mu.Lock()
	if s.timer != nil {
		s.timer.Stop()
	}
	s.info.Running = false
	s.info.ExitCode = code
	s.ended = time.Now()
	s.closed = true
	for ch := range s.subs {
		close(ch)
	}
	s.subs = nil
	s.mu.Unlock()
	close(s.done)
}

// publish добавляет вывод в буфер и раздаёт подключённым клиентам. Отстающий клиент
// отключается (его канал закрывается): при переподключении он получит буфер целиком.
func (s *Session) publish(p []byte) {
	chunk := append([]byte(nil), p...)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buf = append(s.buf, chunk...)
	if over := len(s.buf) - s.limit; over > 0 {
		s.buf = append(s.buf[:0:0], s.buf[over:]...)
	}
	for ch := range s.subs {
		select {
		case ch <- chunk:
		default:
			delete(s.subs, ch)
			close(ch)
		}
	}
}

// Attach возвращает накопленный вывод и канал живого вывода. Канал закрывается,
// когда процесс завершился или клиент не успевает читать. detach отписывает клиента.
func (s *Session) Attach() (snapshot []byte, ch <-chan []byte, detach func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot = append([]byte(nil), s.buf...)
	c := make(chan []byte, subscriberSize)
	if s.closed {
		close(c)
		return snapshot, c, func() {}
	}
	s.subs[c] = struct{}{}
	return snapshot, c, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, ok := s.subs[c]; ok {
			delete(s.subs, c)
			close(c)
		}
	}
}

// Write передаёт ввод процессу.
func (s *Session) Write(p []byte) error {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return io.ErrClosedPipe
	}
	_, err := s.pty.Write(p)
	return err
}

// Resize меняет размер терминала.
func (s *Session) Resize(cols, rows uint16) error {
	if cols == 0 || rows == 0 || cols > 1000 || rows > 1000 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	return pty.Setsize(s.pty, &pty.Winsize{Cols: cols, Rows: rows})
}

// Info возвращает текущее состояние сеанса.
func (s *Session) Info() Info {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.info
}

// Done закрывается, когда процесс завершился.
func (s *Session) Done() <-chan struct{} {
	return s.done
}

// stop посылает группе процессов SIGHUP (как при закрытии окна терминала), через grace SIGKILL.
func (s *Session) stop(grace time.Duration) {
	if s.cmd.Process == nil {
		return
	}
	pgid := s.cmd.Process.Pid
	_ = syscall.Kill(-pgid, syscall.SIGHUP)
	select {
	case <-s.done:
	case <-time.After(grace):
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		<-s.done
	}
}

// Get находит сеанс.
func (m *Manager) Get(id string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return nil, ErrNotFound
	}
	return s, nil
}

// List возвращает сеансы, старые первыми.
func (m *Manager) List() []Info {
	m.mu.Lock()
	m.cleanupLocked(time.Now())
	list := make([]Info, 0, len(m.sessions))
	for _, s := range m.sessions {
		list = append(list, s.Info())
	}
	m.mu.Unlock()
	sort.Slice(list, func(i, j int) bool { return list[i].Started.Before(list[j].Started) })
	return list
}

// Close останавливает процесс (если он ещё идёт) и убирает сеанс из списка.
func (m *Manager) Close(id string) error {
	s, err := m.Get(id)
	if err != nil {
		return err
	}
	s.stop(3 * time.Second)
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
	return nil
}

// Shutdown останавливает все сеансы (при остановке gitaway).
func (m *Manager) Shutdown() {
	m.mu.Lock()
	all := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		all = append(all, s)
	}
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, s := range all {
		wg.Add(1)
		go func(s *Session) {
			defer wg.Done()
			s.stop(2 * time.Second)
		}(s)
	}
	wg.Wait()
}

// cleanupLocked убирает завершённые сеансы старше Keep.
func (m *Manager) cleanupLocked(now time.Time) {
	for id, s := range m.sessions {
		s.mu.Lock()
		old := s.closed && now.Sub(s.ended) > m.Keep
		s.mu.Unlock()
		if old {
			delete(m.sessions, id)
		}
	}
}
