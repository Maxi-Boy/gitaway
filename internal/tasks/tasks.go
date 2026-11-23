// Package tasks описывает задачи (сборка, тесты, свои скрипты): проверку, сборку
// сценария оболочки из шагов, автоопределение по файлам проекта и чтение .gitaway.json.
// Запуск делает сервер через сеансы (internal/session).
package tasks

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Ограничения задачи.
const (
	MaxName     = 80
	MaxSteps    = 20
	MaxStep     = 4000
	MaxTimeout  = 24 * time.Hour
	MaxRepoFile = 64 << 10
	RepoFile    = ".gitaway.json"
)

// Task это именованная цепочка команд.
type Task struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Steps           []string `json:"steps"`
	Cwd             string   `json:"cwd,omitempty"`   // "" или "worktree": папка вкладки; иначе абсолютный путь
	Scope           string   `json:"scope,omitempty"` // "" или "global": все репозитории; иначе id репозитория
	Confirm         bool     `json:"confirm,omitempty"`
	ContinueOnError bool     `json:"continue_on_error,omitempty"`
	Timeout         string   `json:"timeout,omitempty"` // например "30m"; пусто: без ограничения
}

// InWorktree сообщает, что задача выполняется в папке вкладки-ветки.
func (t Task) InWorktree() bool {
	return t.Cwd == "" || t.Cwd == "worktree"
}

// Global сообщает, что задача видна во всех репозиториях.
func (t Task) Global() bool {
	return t.Scope == "" || t.Scope == "global"
}

// Duration возвращает таймаут задачи (0: без ограничения).
func (t Task) Duration() time.Duration {
	d, _ := time.ParseDuration(t.Timeout)
	return d
}

// ErrInvalid оборачивает ошибки проверки задачи.
var ErrInvalid = errors.New("invalid task")

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// Normalize убирает пробелы по краям имени и пустые шаги, затем проверяет задачу.
// Путь Cwd здесь проверяется только на форму; границы проверяет сервер через Sandbox.
func (t *Task) Normalize() error {
	t.Name = strings.TrimSpace(t.Name)
	steps := t.Steps[:0:0]
	for _, s := range t.Steps {
		if s = strings.TrimSpace(s); s != "" {
			steps = append(steps, s)
		}
	}
	t.Steps = steps
	switch {
	case t.Name == "" || utf8.RuneCountInString(t.Name) > MaxName:
		return invalid("name must be 1..%d characters", MaxName)
	case strings.ContainsAny(t.Name, "\x00\n\r"):
		return invalid("name must be a single line")
	case len(t.Steps) == 0 || len(t.Steps) > MaxSteps:
		return invalid("a task needs 1..%d steps", MaxSteps)
	}
	for i, s := range t.Steps {
		if len(s) > MaxStep || strings.ContainsRune(s, 0) {
			return invalid("step %d is too long or contains NUL", i+1)
		}
	}
	if !t.InWorktree() && !filepath.IsAbs(t.Cwd) {
		return invalid("cwd must be \"worktree\" or an absolute path")
	}
	if t.Timeout != "" {
		d, err := time.ParseDuration(t.Timeout)
		if err != nil || d <= 0 || d > MaxTimeout {
			return invalid("timeout must be a duration up to %s, like 30m", MaxTimeout)
		}
	}
	return nil
}

// Vars это значения подстановок в шагах.
type Vars struct {
	Worktree string
	Repo     string
	Branch   string
	Now      time.Time
	Lang     string // язык сообщений сценария: "en" или русский по умолчанию
}

// messages это служебные строки сценария на языке интерфейса.
type messages struct{ step, done string }

func messagesFor(lang string) messages {
	if lang == "en" {
		return messages{step: "✗ step %d failed with code", done: "✓ done"}
	}
	return messages{step: "✗ шаг %d завершился с кодом", done: "✓ готово"}
}

// TimeoutNote это строка, которую терминал показывает при остановке по таймауту.
func TimeoutNote(lang string, d time.Duration) string {
	if lang == "en" {
		return "[stopped: timeout " + d.String() + "]"
	}
	return "[остановлено по таймауту " + d.String() + "]"
}

// Quote заключает строку в одинарные кавычки оболочки: внутри ничего не раскрывается.
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Expand подставляет {worktree}, {repo}, {branch}, {date}, {time}. Каждое значение уже
// в кавычках оболочки, поэтому имя ветки вида $(rm -rf ~) остаётся просто текстом
// (оборачивать подстановку в свои кавычки не нужно). ${...} и прочие скобки не трогаются.
func Expand(step string, v Vars) string {
	vals := map[string]string{
		"worktree": v.Worktree, "repo": v.Repo, "branch": v.Branch,
		"date": v.Now.Format("2006-01-02"), "time": v.Now.Format("150405"),
	}
	var b strings.Builder
	for i := 0; i < len(step); i++ {
		if step[i] == '{' && (i == 0 || step[i-1] != '$') { // ${worktree} это переменная оболочки
			if end := strings.IndexByte(step[i:], '}'); end > 0 {
				if val, ok := vals[step[i+1:i+end]]; ok {
					b.WriteString(Quote(val))
					i += end
					continue
				}
			}
		}
		b.WriteByte(step[i])
	}
	return b.String()
}

// Script собирает сценарий для `sh -c`: шаги по очереди в одной оболочке (cd и переменные
// сохраняются), перед каждым заголовок «▶ 2/3: команда», остановка на первой ошибке
// (или продолжение с итоговым кодом первой ошибки при ContinueOnError).
func Script(t Task, v Vars) string {
	var b strings.Builder
	msg := messagesFor(v.Lang)
	b.WriteString("__gitaway_fail=0\n")
	n := len(t.Steps)
	for i, raw := range t.Steps {
		step := Expand(raw, v)
		fmt.Fprintf(&b, "printf '\\033[1;36m%%s\\033[0m\\n' %s\n", Quote(fmt.Sprintf("▶ %d/%d: %s", i+1, n, raw)))
		b.WriteString(step)
		b.WriteString("\n__gitaway_c=$?\n")
		fmt.Fprintf(&b, "if [ \"$__gitaway_c\" -ne 0 ]; then printf '\\033[31m%%s %%s\\033[0m\\n' %s \"$__gitaway_c\"; ",
			Quote(fmt.Sprintf(msg.step, i+1)))
		if t.ContinueOnError {
			b.WriteString("[ \"$__gitaway_fail\" -eq 0 ] && __gitaway_fail=$__gitaway_c; fi\n")
		} else {
			b.WriteString("exit \"$__gitaway_c\"; fi\n")
		}
	}
	fmt.Fprintf(&b, "if [ \"$__gitaway_fail\" -eq 0 ]; then printf '\\033[32m%%s\\033[0m\\n' %s; fi\n", Quote(msg.done))
	b.WriteString("exit \"$__gitaway_fail\"\n")
	return b.String()
}

// RepoTasks это задачи из .gitaway.json рабочей папки.
type RepoTasks struct {
	Tasks []Task `json:"tasks"`
	Hash  string `json:"hash"`            // SHA-256 файла: к нему привязано доверие
	Error string `json:"error,omitempty"` // файл есть, но прочитать или разобрать не удалось
}

// LoadRepoFile читает .gitaway.json из dir. Нет файла: пустой результат без ошибки.
// В задачах из репозитория разрешена только папка вкладки (Cwd), Scope не учитывается.
func LoadRepoFile(dir string) RepoTasks {
	f, err := os.Open(filepath.Join(dir, RepoFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return RepoTasks{Tasks: []Task{}}
		}
		return RepoTasks{Tasks: []Task{}, Error: err.Error()}
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxRepoFile+1))
	if err != nil {
		return RepoTasks{Tasks: []Task{}, Error: err.Error()}
	}
	sum := sha256.Sum256(data)
	rt := RepoTasks{Tasks: []Task{}, Hash: hex.EncodeToString(sum[:])}
	if len(data) > MaxRepoFile {
		rt.Error = fmt.Sprintf("%s is larger than %d bytes", RepoFile, MaxRepoFile)
		return rt
	}
	var doc struct {
		Tasks []Task `json:"tasks"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		rt.Error = err.Error()
		return rt
	}
	for i, t := range doc.Tasks {
		if !t.InWorktree() {
			rt.Error = fmt.Sprintf("task %d: tasks from the repository run only in the branch folder", i+1)
			return rt
		}
		t.Cwd, t.Scope = "", ""
		if err := t.Normalize(); err != nil {
			rt.Error = fmt.Sprintf("task %d: %v", i+1, err)
			return rt
		}
		t.ID = fmt.Sprintf("repo:%d", i)
		rt.Tasks = append(rt.Tasks, t)
	}
	return rt
}

// makeTarget: явная цель Makefile «имя:», но не присваивание «имя := значение».
var makeTarget = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9_./-]*)\s*:([^=]|$)`)

// Detect предлагает задачи по файлам проекта. Сами ничего не запускают.
func Detect(dir string) []Task {
	var out []Task
	add := func(name string, steps ...string) {
		out = append(out, Task{ID: "auto:" + name, Name: name, Steps: steps})
	}
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(dir, name))
		return err == nil
	}
	read := func(name string) []byte {
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			return nil
		}
		defer f.Close()
		b, _ := io.ReadAll(io.LimitReader(f, 256<<10))
		return b
	}

	for _, mk := range []string{"GNUmakefile", "makefile", "Makefile"} {
		data := read(mk)
		if data == nil {
			continue
		}
		seen := map[string]bool{}
		for _, line := range strings.Split(string(data), "\n") {
			m := makeTarget.FindStringSubmatch(line)
			if m == nil || seen[m[1]] || strings.Contains(m[1], "%") || len(seen) >= 30 {
				continue
			}
			seen[m[1]] = true
			add("make "+m[1], "make "+m[1])
		}
		break
	}
	if exists("go.mod") {
		add("go build", "go build ./...")
		add("go test", "go test ./...")
		add("go vet", "go vet ./...")
	}
	if data := read("package.json"); data != nil {
		var pkg struct {
			Scripts map[string]string `json:"scripts"`
		}
		if json.Unmarshal(data, &pkg) == nil {
			names := make([]string, 0, len(pkg.Scripts))
			for name := range pkg.Scripts {
				names = append(names, name)
			}
			sort.Strings(names)
			for i, name := range names {
				if i >= 30 {
					break
				}
				add("npm run "+name, "npm run "+Quote(name))
			}
		}
	}
	if exists("Cargo.toml") {
		add("cargo build", "cargo build")
		add("cargo test", "cargo test")
	}
	if exists("pytest.ini") || exists("conftest.py") || strings.Contains(string(read("pyproject.toml")), "[tool.pytest") {
		add("pytest", "python3 -m pytest")
	}
	if out == nil {
		out = []Task{}
	}
	return out
}
