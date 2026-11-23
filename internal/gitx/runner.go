// Package gitx это тонкая обёртка над системным git (os/exec).
//
// Принципы: аргументы передаются списком (никаких строк для оболочки), все
// значения из запросов проверяются в validate.go, у каждой команды есть
// таймаут, git никогда не ждёт ввода (GIT_TERMINAL_PROMPT=0).
package gitx

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Error это ошибка выполнения git: код выхода и вывод в stderr (без учётных данных в URL).
type Error struct {
	Args   []string
	Code   int
	Output string
}

func (e *Error) Error() string {
	out := strings.TrimSpace(e.Output)
	if out == "" {
		out = "no output"
	}
	return fmt.Sprintf("git %s: exit %d: %s", strings.Join(e.Args, " "), e.Code, out)
}

// InputError это ошибка неверных входных данных (на уровне API превращается в HTTP 400).
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

func invalid(msg string) error { return &InputError{Msg: msg} }

func invalidf(format string, a ...any) error { return &InputError{Msg: fmt.Sprintf(format, a...)} }

var credRe = regexp.MustCompile(`://[^/\s@]+@`)

// redact скрывает логин и пароль в URL вида scheme://user:pass@host.
func redact(s string) string { return credRe.ReplaceAllString(s, "://***@") }

func redactAll(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = redact(a)
	}
	return out
}

// NeedsLogin сообщает, что git не смог войти на сервер репозитория: по HTTPS нужен логин
// (спросить его некому, диалоги выключены) или ключ SSH не подошёл.
func NeedsLogin(err error) bool {
	var ge *Error
	if !errors.As(err, &ge) {
		return false
	}
	o := strings.ToLower(ge.Output)
	for _, s := range []string{"could not read username", "could not read password", "authentication failed",
		"terminal prompts disabled", "permission denied (publickey)", "invalid username or password"} {
		if strings.Contains(o, s) {
			return true
		}
	}
	return false
}

// Runner запускает git. Нулевое значение пригодно к использованию.
type Runner struct {
	GitBin  string        // по умолчанию "git"
	Timeout time.Duration // применяется, если у контекста нет своего срока
	Env     []string      // дополнительные переменные окружения
}

// NewRunner возвращает Runner с таймаутом 60 секунд.
func NewRunner() *Runner { return &Runner{GitBin: "git", Timeout: 60 * time.Second} }

// readOnlyEnv не даёт git брать необязательные блокировки (нужно для status и подобных).
var readOnlyEnv = []string{"GIT_OPTIONAL_LOCKS=0"}

func (r *Runner) run(ctx context.Context, dir string, extraEnv []string, args []string) (string, string, error) {
	if _, ok := ctx.Deadline(); !ok && r.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.Timeout)
		defer cancel()
	}
	bin := r.GitBin
	if bin == "" {
		bin = "git"
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.WaitDelay = 5 * time.Second // не ждать вечно дочерние процессы (например, ssh)
	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	env = append(env, r.Env...)
	env = append(env, extraEnv...)
	cmd.Env = env

	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		e := &Error{Args: redactAll(args), Code: -1, Output: redact(strings.TrimSpace(stderr.String()))}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			e.Code = ee.ExitCode()
		} else if e.Output == "" {
			e.Output = err.Error()
		}
		if ctx.Err() != nil {
			e.Output = ctx.Err().Error()
		}
		return stdout.String(), stderr.String(), e
	}
	return stdout.String(), stderr.String(), nil
}

// Run выполняет git в каталоге dir и возвращает stdout.
func (r *Runner) Run(ctx context.Context, dir string, args ...string) (string, error) {
	out, _, err := r.run(ctx, dir, nil, args)
	return out, err
}

// RunEnv как Run, с дополнительными переменными окружения.
func (r *Runner) RunEnv(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	out, _, err := r.run(ctx, dir, env, args)
	return out, err
}

// RunCombined возвращает stdout и stderr вместе (удобно показывать пользователю
// вывод fetch, pull, push, где git пишет прогресс в stderr).
func (r *Runner) RunCombined(ctx context.Context, dir string, args ...string) (string, error) {
	so, se, err := r.run(ctx, dir, nil, args)
	out := strings.TrimSpace(redact(so + se))
	// часть команд (git merge: CONFLICT ...) пишет причину в stdout: она нужна и в ошибке
	var ge *Error
	if errors.As(err, &ge) && out != "" && ge.Code >= 0 {
		ge.Output = out
	}
	return out, err
}

// Stream запускает git и передаёт fn вывод по записям, разделённым sep. Если fn вернула
// false, git останавливается (так поиск и история не читают лишнего). Ошибкой считается
// только сбой запуска или код выхода, отличный от allow, когда вывод дочитан до конца.
func (r *Runner) Stream(ctx context.Context, dir string, env []string, sep byte, allow []int, fn func(rec []byte) bool, args ...string) error {
	if _, ok := ctx.Deadline(); !ok && r.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.Timeout)
		defer cancel()
	}
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	bin := r.GitBin
	if bin == "" {
		bin = "git"
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.WaitDelay = 5 * time.Second
	cmd.Env = append(append(append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C"), r.Env...), env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return &Error{Args: redactAll(args), Code: -1, Output: err.Error()}
	}
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	sc.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		if i := bytes.IndexByte(data, sep); i >= 0 {
			return i + 1, data[:i], nil
		}
		if atEOF && len(data) > 0 {
			return len(data), data, nil
		}
		return 0, nil, nil
	})
	stopped := false
	for sc.Scan() {
		if !fn(sc.Bytes()) {
			stopped = true
			break
		}
	}
	scanErr := sc.Err()
	if stopped || scanErr != nil {
		stop() // остальное не нужно: git получает сигнал и выходит
	}
	err = cmd.Wait()
	if stopped {
		return nil
	}
	if scanErr != nil {
		return &Error{Args: redactAll(args), Code: -1, Output: scanErr.Error()}
	}
	if err != nil {
		e := &Error{Args: redactAll(args), Code: -1, Output: redact(strings.TrimSpace(stderr.String()))}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			e.Code = ee.ExitCode()
			for _, c := range allow {
				if c == e.Code {
					return nil
				}
			}
		}
		if ctx.Err() != nil {
			e.Output = ctx.Err().Error()
		}
		return e
	}
	return nil
}
