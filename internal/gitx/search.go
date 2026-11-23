package gitx

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Ограничения быстрого открытия и поиска: ответ не растёт без предела на больших репозиториях.
const (
	MaxFiles       = 50000 // путей в списке файлов
	MaxMatches     = 2000  // совпадений в ответе поиска
	MaxMatchLine   = 400   // байт строки совпадения в ответе
	MaxQuery       = 1000  // длина строки поиска
	MaxSearchGlobs = 20
)

// FileList это файлы рабочей папки: отслеживаемые и новые, без игнорируемых.
type FileList struct {
	Files     []string `json:"files"`
	Truncated bool     `json:"truncated,omitempty"`
}

// ListFiles возвращает пути файлов для быстрого открытия. Удалённые с диска файлы
// (ещё в индексе) не попадают в список, повторы при конфликте слияния убираются.
func (r *Runner) ListFiles(ctx context.Context, dir string) (*FileList, error) {
	gone := map[string]bool{}
	err := r.Stream(ctx, dir, readOnlyEnv, 0, nil, func(rec []byte) bool {
		if len(rec) > 0 {
			gone[string(rec)] = true
		}
		return true
	}, "-c", "core.quotepath=false", "ls-files", "-z", "--deleted")
	if err != nil {
		return nil, err
	}
	res := &FileList{Files: []string{}}
	seen := map[string]bool{}
	err = r.Stream(ctx, dir, readOnlyEnv, 0, nil, func(rec []byte) bool {
		p := string(rec)
		if p == "" || seen[p] || gone[p] {
			return true
		}
		if len(res.Files) >= MaxFiles {
			res.Truncated = true
			return false
		}
		seen[p] = true
		res.Files = append(res.Files, p)
		return true
	}, "-c", "core.quotepath=false", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	return res, nil
}

// SearchOptions это параметры поиска по рабочей папке.
type SearchOptions struct {
	Query string
	Regex bool     // расширенное регулярное выражение (ERE); иначе обычный текст
	Case  bool     // учитывать регистр
	Word  bool     // только целые слова
	Globs []string // какие файлы: "*.go", "src/", "!vendor/" (с ! исключить)
}

// Match это одна строка с совпадением. Col это номер байта начала совпадения (с 1).
type Match struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Col  int    `json:"col"`
	Text string `json:"text"`
}

// SearchResult это ответ поиска.
type SearchResult struct {
	Matches   []Match `json:"matches"`
	Files     int     `json:"files"`               // файлов с совпадениями в ответе
	Truncated bool    `json:"truncated,omitempty"` // упёрлись в предел или во время
}

// globSpec превращает фильтр файлов в pathspec git: "*.go" ищется в любой папке,
// "src/" это папка от корня, "!x" исключает. Магия pathspec (":(...)") из запроса запрещена.
func globSpec(g string) (string, error) {
	g = strings.TrimSpace(g)
	exclude := strings.HasPrefix(g, "!")
	g = strings.TrimSpace(strings.TrimPrefix(g, "!"))
	if g == "" || len(g) > 256 || strings.ContainsAny(g, "\x00\\:") || strings.HasPrefix(g, "/") {
		return "", invalidf("invalid file filter %q", g)
	}
	for _, seg := range strings.Split(g, "/") {
		if seg == ".." || seg == ".git" {
			return "", invalidf("invalid file filter %q", g)
		}
	}
	switch {
	case strings.HasSuffix(g, "/"):
		g += "**"
	case !strings.Contains(g, "/"):
		g = "**/" + g
	}
	magic := "glob"
	if exclude {
		magic = "glob,exclude"
	}
	return ":(" + magic + ")" + g, nil
}

// Search ищет текст в файлах рабочей папки через git grep: отслеживаемые и новые файлы,
// без игнорируемых и двоичных. Остановка на MaxMatches или по сроку контекста; в обоих
// случаях возвращается найденное с Truncated.
func (r *Runner) Search(ctx context.Context, dir string, o SearchOptions) (*SearchResult, error) {
	q := o.Query
	switch {
	case strings.TrimSpace(q) == "":
		return nil, invalid("empty search query")
	case len(q) > MaxQuery:
		return nil, invalid("search query is too long")
	case strings.ContainsAny(q, "\x00\r\n"):
		return nil, invalid("search query must be a single line")
	case len(o.Globs) > MaxSearchGlobs:
		return nil, invalidf("too many file filters (max %d)", MaxSearchGlobs)
	}
	args := []string{"-c", "core.quotepath=false", "-c", "grep.fallbackToNoIndex=false",
		"grep", "-n", "--column", "-I", "--null", "--no-color", "--untracked", "--full-name"}
	if o.Regex {
		args = append(args, "-E")
	} else {
		args = append(args, "-F")
	}
	if !o.Case {
		args = append(args, "-i")
	}
	if o.Word {
		args = append(args, "-w")
	}
	args = append(args, "-e", q, "--") // "-e": строка поиска не станет опцией
	var specs []string
	include := false
	for _, g := range o.Globs {
		if strings.TrimSpace(g) == "" {
			continue
		}
		s, err := globSpec(g)
		if err != nil {
			return nil, err
		}
		if !strings.Contains(s, "exclude") {
			include = true
		}
		specs = append(specs, s)
	}
	if len(specs) > 0 && !include {
		specs = append(specs, ".") // одни исключения: остальное ищем везде
	}
	args = append(args, specs...)

	res := &SearchResult{Matches: []Match{}}
	last := ""
	err := r.Stream(ctx, dir, readOnlyEnv, '\n', []int{1}, func(rec []byte) bool {
		// запись: путь \0 строка \0 колонка \0 текст
		p := bytes.SplitN(rec, []byte{0}, 4)
		if len(p) != 4 {
			return true
		}
		if len(res.Matches) >= MaxMatches {
			res.Truncated = true
			return false
		}
		line, _ := strconv.Atoi(string(p[1]))
		col, _ := strconv.Atoi(string(p[2]))
		m := Match{Path: string(p[0]), Line: line, Col: col, Text: clipLine(p[3], col)}
		if m.Path != last {
			res.Files++
			last = m.Path
		}
		res.Matches = append(res.Matches, m)
		return true
	}, args...)
	if err != nil {
		var ge *Error
		if ctx.Err() != nil && len(res.Matches) > 0 {
			res.Truncated = true // время вышло: отдаём, что успели
			return res, nil
		}
		if o.Regex && errors.As(err, &ge) && ge.Code == 128 && strings.Contains(ge.Output, "-e option") {
			// fatal: -e option, '(': Unmatched ( or \(
			msg := ge.Output
			if i := strings.LastIndex(msg, ": "); i >= 0 {
				msg = msg[i+2:]
			}
			return nil, invalidf("invalid regular expression: %s", msg)
		}
		return nil, err
	}
	return res, nil
}

// clipLine обрезает длинную строку вокруг совпадения и чинит неполные символы UTF-8 на краях.
// Колонка в ответе остаётся от начала строки; интерфейс подсвечивает совпадение сам.
func clipLine(b []byte, col int) string {
	b = bytes.TrimRight(b, "\r")
	if len(b) <= MaxMatchLine {
		return strings.ToValidUTF8(string(b), "�")
	}
	start := 0
	if col-1 > MaxMatchLine/4 {
		start = col - 1 - MaxMatchLine/4
	}
	end := min(start+MaxMatchLine, len(b))
	for start > 0 && start < len(b) && !utf8.RuneStart(b[start]) {
		start++
	}
	s := strings.ToValidUTF8(string(b[start:end]), "")
	if start > 0 {
		s = "…" + s
	}
	if end < len(b) {
		s += "…"
	}
	return s
}
