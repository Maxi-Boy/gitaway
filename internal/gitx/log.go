package gitx

import (
	"context"
	"regexp"
	"strconv"
	"strings"
)

// Ограничения истории.
const (
	MaxLogPage     = 200  // коммитов за один запрос
	MaxCommitFiles = 3000 // файлов в описании коммита
)

// LogEntry это строка истории ветки.
type LogEntry struct {
	Hash     string   `json:"hash"`
	Parents  []string `json:"parents"`
	Author   string   `json:"author"`
	Email    string   `json:"email"`
	Time     int64    `json:"time"`
	Subject  string   `json:"subject"`
	Refs     []string `json:"refs,omitempty"`     // ветки и теги, указывающие на коммит
	Unpushed bool     `json:"unpushed,omitempty"` // есть в ветке, но ещё не в upstream
}

// LogPage это страница истории.
type LogPage struct {
	Commits []LogEntry `json:"commits"`
	More    bool       `json:"more"`
}

// LogOptions выбирает страницу истории HEAD рабочей папки.
type LogOptions struct {
	Path  string // только коммиты, менявшие этот путь ("" вся история)
	Dir   bool   // путь это папка: переименования не отслеживаются (--follow только для файла)
	Skip  int
	Limit int
}

const logFormat = "%H%x1f%P%x1f%an%x1f%ae%x1f%at%x1f%D%x1f%s"

func parseLogEntry(rec string) (LogEntry, bool) {
	p := strings.SplitN(strings.TrimLeft(rec, "\n"), "\x1f", 7)
	if len(p) != 7 {
		return LogEntry{}, false
	}
	ts, _ := strconv.ParseInt(p[4], 10, 64)
	e := LogEntry{Hash: p[0], Parents: strings.Fields(p[1]), Author: p[2], Email: p[3], Time: ts, Subject: p[6]}
	if e.Parents == nil {
		e.Parents = []string{}
	}
	for _, ref := range strings.Split(p[5], ", ") {
		ref = strings.TrimPrefix(ref, "HEAD -> ")
		if ref != "" && ref != "HEAD" {
			e.Refs = append(e.Refs, ref)
		}
	}
	return e, true
}

// Log возвращает страницу истории текущей ветки рабочей папки, новые коммиты первыми.
// Для истории файла учитываются переименования (--follow).
func (r *Runner) Log(ctx context.Context, dir string, o LogOptions) (*LogPage, error) {
	if o.Limit <= 0 || o.Limit > MaxLogPage {
		o.Limit = MaxLogPage
	}
	if o.Skip < 0 {
		o.Skip = 0
	}
	page := &LogPage{Commits: []LogEntry{}}
	if !r.hasHead(ctx, dir) {
		return page, nil // ни одного коммита
	}
	args := []string{"-c", "core.quotepath=false", "-c", "log.showSignature=false", "log", "-z",
		"--format=" + logFormat, "--skip=" + strconv.Itoa(o.Skip), "-n", strconv.Itoa(o.Limit + 1), "HEAD"}
	env := readOnlyEnv
	if o.Path != "" {
		p, err := cleanPath(o.Path)
		if err != nil {
			return nil, err
		}
		if p != "." {
			if !o.Dir {
				args = append(args, "--follow")
			}
			args = append(args, "--", p)
			env = append(append([]string{}, readOnlyEnv...), literalEnv...)
		}
	}
	err := r.Stream(ctx, dir, env, 0, nil, func(rec []byte) bool {
		e, ok := parseLogEntry(string(rec))
		if !ok {
			return true
		}
		if len(page.Commits) == o.Limit {
			page.More = true
			return false
		}
		page.Commits = append(page.Commits, e)
		return true
	}, args...)
	if err != nil {
		return nil, err
	}
	return page, nil
}

// Unpushed возвращает коммиты ветки, которых ещё нет в upstream (не больше limit).
// Без upstream пустой результат: отмечать нечего.
func (r *Runner) Unpushed(ctx context.Context, dir string, limit int) map[string]bool {
	out, err := r.RunEnv(ctx, dir, readOnlyEnv, "rev-list", "--max-count="+strconv.Itoa(limit), "@{upstream}..HEAD")
	set := map[string]bool{}
	if err != nil {
		return set
	}
	for _, h := range strings.Fields(out) {
		set[h] = true
	}
	return set
}

var hashRe = regexp.MustCompile(`^[0-9a-f]{4,64}$`)

// ValidHash проверяет сокращённый или полный хеш коммита (только шестнадцатеричные цифры).
func ValidHash(h string) error {
	if !hashRe.MatchString(h) {
		return invalidf("invalid commit hash %q", h)
	}
	return nil
}

// CommitFile это файл, изменённый коммитом (относительно первого родителя).
type CommitFile struct {
	Path    string `json:"path"`
	OldPath string `json:"old_path,omitempty"` // при переименовании и копировании
	Status  string `json:"status"`             // A, M, D, R, C, T
	Added   int    `json:"added"`              // строк; -1 для двоичного файла
	Deleted int    `json:"deleted"`
}

// CommitDetail это описание коммита для просмотра.
type CommitDetail struct {
	LogEntry
	Body           string       `json:"body"`
	Committer      string       `json:"committer"`
	CommitTime     int64        `json:"commit_time"`
	Files          []CommitFile `json:"files"`
	FilesTruncated bool         `json:"files_truncated,omitempty"`
}

// resolveCommit превращает хеш (возможно сокращённый) в полный хеш коммита.
func (r *Runner) resolveCommit(ctx context.Context, dir, hash string) (string, error) {
	if err := ValidHash(hash); err != nil {
		return "", err
	}
	out, err := r.RunEnv(ctx, dir, readOnlyEnv, "rev-parse", "--verify", "-q", hash+"^{commit}")
	if err != nil {
		return "", &InputError{Msg: "commit not found: " + hash}
	}
	return strings.TrimSpace(out), nil
}

// diffBase возвращает аргументы diff-tree, сравнивающие коммит с первым родителем.
// У первого коммита родителя нет: --root сравнивает с пустым деревом.
func diffBase(c *LogEntry) []string {
	if len(c.Parents) == 0 {
		return []string{"--root", c.Hash}
	}
	return []string{c.Parents[0], c.Hash}
}

// ShowCommit возвращает описание коммита и список изменённых файлов со счётчиками строк.
func (r *Runner) ShowCommit(ctx context.Context, dir, hash string) (*CommitDetail, error) {
	full, err := r.resolveCommit(ctx, dir, hash)
	if err != nil {
		return nil, err
	}
	out, err := r.RunEnv(ctx, dir, readOnlyEnv, "-c", "log.showSignature=false", "show", "-s", "-z",
		"--format="+logFormat+"%x1f%cn%x1f%ct%x1f%b", full)
	if err != nil {
		return nil, err
	}
	rec := strings.TrimRight(out, "\x00")
	p := strings.SplitN(rec, "\x1f", 10)
	if len(p) != 10 {
		return nil, &Error{Args: []string{"show"}, Code: -1, Output: "unexpected output"}
	}
	e, _ := parseLogEntry(strings.Join(p[:7], "\x1f"))
	ct, _ := strconv.ParseInt(p[8], 10, 64)
	d := &CommitDetail{LogEntry: e, Committer: p[7], CommitTime: ct, Body: strings.TrimSpace(p[9]), Files: []CommitFile{}}

	base := append([]string{"-c", "core.quotepath=false", "diff-tree", "-r", "-z", "-M", "--no-commit-id"}, diffBase(&e)...)
	// имена и состояние: "R100\0старый\0новый\0" или "M\0путь\0"
	names, err := r.RunEnv(ctx, dir, readOnlyEnv, append(append([]string{}, base...), "--name-status")...)
	if err != nil {
		return nil, err
	}
	f := strings.Split(strings.TrimRight(names, "\x00"), "\x00")
	index := map[string]int{}
	for i := 0; i < len(f); {
		st := f[i]
		if st == "" {
			break
		}
		cf := CommitFile{Status: st[:1]}
		if (cf.Status == "R" || cf.Status == "C") && i+2 < len(f) {
			cf.OldPath, cf.Path = f[i+1], f[i+2]
			i += 3
		} else if i+1 < len(f) {
			cf.Path = f[i+1]
			i += 2
		} else {
			break
		}
		if len(d.Files) >= MaxCommitFiles {
			d.FilesTruncated = true
			break
		}
		index[cf.Path] = len(d.Files)
		d.Files = append(d.Files, cf)
	}
	// счётчики строк: "доб\tуд\tпуть\0" или "доб\tуд\t\0старый\0новый\0"; двоичный файл "-\t-"
	nums, err := r.RunEnv(ctx, dir, readOnlyEnv, append(append([]string{}, base...), "--numstat")...)
	if err != nil {
		return nil, err
	}
	n := strings.Split(strings.TrimRight(nums, "\x00"), "\x00")
	for i := 0; i < len(n); i++ {
		parts := strings.SplitN(n[i], "\t", 3)
		if len(parts) != 3 {
			continue
		}
		path := parts[2]
		if path == "" && i+2 < len(n) {
			path = n[i+2]
			i += 2
		}
		k, ok := index[path]
		if !ok {
			continue
		}
		a, errA := strconv.Atoi(parts[0])
		del, errD := strconv.Atoi(parts[1])
		if errA != nil || errD != nil {
			a, del = -1, -1
		}
		d.Files[k].Added, d.Files[k].Deleted = a, del
	}
	return d, nil
}

// CommitDiff возвращает diff одного файла в коммите (относительно первого родителя).
// oldPath нужен для переименованного файла, чтобы git сопоставил старое и новое имя.
func (r *Runner) CommitDiff(ctx context.Context, dir, hash, path, oldPath string) (*Diff, error) {
	full, err := r.resolveCommit(ctx, dir, hash)
	if err != nil {
		return nil, err
	}
	paths := []string{path}
	if oldPath != "" && oldPath != path {
		paths = append(paths, oldPath)
	}
	paths, err = CleanPaths(paths)
	if err != nil {
		return nil, err
	}
	if paths[0] == "." {
		return nil, invalid("diff needs a file path")
	}
	out, err := r.RunEnv(ctx, dir, readOnlyEnv, "rev-list", "--parents", "-n", "1", full)
	if err != nil {
		return nil, err
	}
	f := strings.Fields(out)
	e := &LogEntry{Hash: full}
	if len(f) > 1 {
		e.Parents = f[1:]
	}
	env := append(append([]string{}, readOnlyEnv...), literalEnv...)
	args := append([]string{"diff-tree", "-p", "-M", "--no-color", "--no-ext-diff", "--no-commit-id"}, diffBase(e)...)
	text, err := r.RunEnv(ctx, dir, env, withPaths(args, paths)...)
	if err != nil {
		return nil, err
	}
	d := &Diff{Path: path, Text: text}
	if len(text) > MaxDiff {
		d.Text, d.Truncated = text[:MaxDiff], true
	}
	return d, nil
}
