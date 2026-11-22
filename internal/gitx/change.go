package gitx

import (
	"context"
	"errors"
	"path"
	"strings"
)

// MaxPaths ограничивает число путей в одной операции (длина командной строки).
const MaxPaths = 1000

// MaxDiff это предел размера diff в ответе; длиннее обрезается.
const MaxDiff = 1 << 20

// literalEnv выключает шаблоны в путях: "*.go" и ":(top)" это просто имена файлов.
var literalEnv = []string{"GIT_LITERAL_PATHSPECS=1"}

// CleanPaths проверяет пути из запроса и приводит их к виду "a/b". Путь относительный,
// без выхода наверх и без .git. "." означает всю рабочую папку.
func CleanPaths(paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, invalid("no paths given")
	}
	if len(paths) > MaxPaths {
		return nil, invalidf("too many paths (max %d)", MaxPaths)
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		c, err := cleanPath(p)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func cleanPath(p string) (string, error) {
	if p == "" || len(p) > 4096 || strings.ContainsAny(p, "\x00\\") {
		return "", invalidf("invalid path %q", p)
	}
	if strings.HasPrefix(p, "/") {
		return "", invalidf("path must be relative: %q", p)
	}
	c := path.Clean(p)
	if c == ".." || strings.HasPrefix(c, "../") {
		return "", invalidf("path leaves the working tree: %q", p)
	}
	for _, seg := range strings.Split(c, "/") {
		if seg == ".git" {
			return "", invalidf("paths inside .git are not allowed: %q", p)
		}
	}
	return c, nil
}

func withPaths(args []string, paths []string) []string {
	args = append(args, "--")
	return append(args, paths...)
}

// hasHead сообщает, есть ли в репозитории хотя бы один коммит.
func (r *Runner) hasHead(ctx context.Context, dir string) bool {
	_, err := r.RunEnv(ctx, dir, readOnlyEnv, "rev-parse", "--verify", "-q", "HEAD")
	return err == nil
}

// Stage добавляет изменения в индекс, включая удаления и новые файлы.
func (r *Runner) Stage(ctx context.Context, dir string, paths []string) error {
	paths, err := CleanPaths(paths)
	if err != nil {
		return err
	}
	_, err = r.RunEnv(ctx, dir, literalEnv, withPaths([]string{"add", "-A"}, paths)...)
	return err
}

// Unstage убирает изменения из индекса, рабочая папка не меняется.
func (r *Runner) Unstage(ctx context.Context, dir string, paths []string) error {
	paths, err := CleanPaths(paths)
	if err != nil {
		return err
	}
	args := []string{"restore", "--staged"}
	if !r.hasHead(ctx, dir) {
		// до первого коммита сравнивать не с чем: просто убрать файлы из индекса
		args = []string{"rm", "--cached", "-r", "-q", "--ignore-unmatch"}
	}
	_, err = r.RunEnv(ctx, dir, literalEnv, withPaths(args, paths)...)
	return err
}

// changesFor возвращает изменения только по указанным путям, по одному файлу
// на запись и без склейки переименований (удаление и добавление отдельно).
func (r *Runner) changesFor(ctx context.Context, dir string, paths []string) ([]Change, error) {
	env := append([]string{}, readOnlyEnv...)
	env = append(env, literalEnv...)
	out, err := r.RunEnv(ctx, dir, env, withPaths(
		[]string{"status", "--porcelain=v1", "-z", "--no-renames", "--untracked-files=all"}, paths)...)
	if err != nil {
		return nil, err
	}
	return parseStatus(out).Changes, nil
}

// Discard возвращает файлы к состоянию последнего коммита: правки и индекс
// сбрасываются, новые (неотслеживаемые или только добавленные) файлы удаляются.
// Действует только на то, что git status показывает для этих путей; игнорируемые
// файлы не трогает. Возвращает список затронутых файлов. Подтверждение спрашивает интерфейс.
func (r *Runner) Discard(ctx context.Context, dir string, paths []string) ([]string, error) {
	paths, err := CleanPaths(paths)
	if err != nil {
		return nil, err
	}
	changes, err := r.changesFor(ctx, dir, paths)
	if err != nil {
		return nil, err
	}
	var tracked, added, created, touched []string
	for _, c := range changes {
		touched = append(touched, c.Path)
		switch {
		case c.X == "?":
			created = append(created, c.Path)
		case c.X == "A": // до первого коммита все файлы индекса тоже "A"
			added = append(added, c.Path)
			created = append(created, c.Path)
		default:
			tracked = append(tracked, c.Path)
		}
	}
	if len(tracked) > 0 {
		args := withPaths([]string{"restore", "--source=HEAD", "--staged", "--worktree"}, tracked)
		if _, err := r.RunEnv(ctx, dir, literalEnv, args...); err != nil {
			return nil, err
		}
	}
	if len(added) > 0 {
		args := withPaths([]string{"rm", "--cached", "-q", "--ignore-unmatch"}, added)
		if _, err := r.RunEnv(ctx, dir, literalEnv, args...); err != nil {
			return nil, err
		}
	}
	if len(created) > 0 {
		if _, err := r.RunEnv(ctx, dir, literalEnv, withPaths([]string{"clean", "-f", "-q"}, created)...); err != nil {
			return nil, err
		}
	}
	if touched == nil {
		touched = []string{}
	}
	return touched, nil
}

// Commit создаёт коммит из индекса. Хуки репозитория выполняются как обычно.
// Возвращает хеш нового коммита и вывод git.
func (r *Runner) Commit(ctx context.Context, dir, message string) (hash, output string, err error) {
	msg := strings.TrimSpace(message)
	switch {
	case msg == "":
		return "", "", invalid("empty commit message")
	case len(msg) > 64<<10:
		return "", "", invalid("commit message is too long")
	case strings.ContainsRune(msg, 0):
		return "", "", invalid("invalid character in commit message")
	}
	st, err := r.Status(ctx, dir)
	if err != nil {
		return "", "", err
	}
	staged := false
	for _, c := range st.Changes {
		if c.X != " " && c.X != "?" {
			staged = true
			break
		}
	}
	if !staged {
		return "", "", invalid("nothing is staged for commit")
	}
	// "--message=" одним аргументом: текст не может стать опцией git.
	output, err = r.RunCombined(ctx, dir, "commit", "--message="+msg)
	if err != nil {
		return "", output, err
	}
	out, err := r.Run(ctx, dir, "rev-parse", "HEAD")
	return strings.TrimSpace(out), output, err
}

// Diff это разница по одному файлу.
type Diff struct {
	Path      string `json:"path"`
	Staged    bool   `json:"staged"`
	Untracked bool   `json:"untracked,omitempty"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated,omitempty"`
}

// FileDiff возвращает diff файла: рабочая папка против индекса или (staged) индекс
// против HEAD. Для неотслеживаемого файла показывается весь файл как добавленный.
func (r *Runner) FileDiff(ctx context.Context, dir, p string, staged bool) (*Diff, error) {
	p, err := cleanPath(p)
	if err != nil {
		return nil, err
	}
	if p == "." {
		return nil, invalid("diff needs a file path")
	}
	d := &Diff{Path: p, Staged: staged}
	changes, err := r.changesFor(ctx, dir, []string{p})
	if err != nil {
		return nil, err
	}
	for _, c := range changes {
		if c.X == "?" && c.Path == p {
			d.Untracked = true
		}
	}
	env := append([]string{}, readOnlyEnv...)
	env = append(env, literalEnv...)
	var text string
	switch {
	case d.Untracked && staged:
		// в индексе такого файла нет: разницы нет
	case d.Untracked:
		text, err = r.RunEnv(ctx, dir, env, "diff", "--no-index", "--no-color", "--no-ext-diff", "--", "/dev/null", p)
		var ge *Error
		if errors.As(err, &ge) && ge.Code == 1 {
			err = nil // для --no-index код 1 означает "файлы различаются"
		}
	default:
		args := []string{"diff", "--no-color", "--no-ext-diff"}
		if staged {
			args = append(args, "--cached")
		}
		text, err = r.RunEnv(ctx, dir, env, withPaths(args, []string{p})...)
	}
	if err != nil {
		return nil, err
	}
	if len(text) > MaxDiff {
		text, d.Truncated = text[:MaxDiff], true
	}
	d.Text = text
	return d, nil
}
